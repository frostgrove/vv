package crudhttp

import (
	"context"
	"net/http"
	"strconv"

	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/port"
)

// A Renderer turns an error into a response. It is the seam a consumer replaces
// to ship RFC 9457, or a legacy shape, or nothing at all.
//
// It lives here rather than in errs, and that is [[D-045]]'s test made concrete:
// an interface returning http.Header cannot be implemented by a gRPC binding, so
// it is not part of the transport-neutral half. errs/spi.go says so where the
// absence would otherwise look like an oversight.
type Renderer interface {
	// Render answers the status, any headers the status requires, and the body
	// value to marshal. A nil body means "write no body".
	Render(ctx context.Context, err error) (int, http.Header, any)
}

// MaxViolations is how many violations one body carries before the rest are
// dropped and Partial is set. A response body is not a log.
const MaxViolations = 100

// DefaultRetryAfter is the Retry-After a 503 carries, in seconds. The framework
// does not retry on the caller's behalf ([[D-040]]); this is the smallest
// honest hint that retrying is the right thing at all.
const DefaultRetryAfter = 1

// An EnvelopeRenderer renders [Envelope]. It is the default in every binding.
type EnvelopeRenderer struct {
	codes      *errs.Codes
	messages   errs.MessageSource
	resolvers  []errs.Resolver
	max        int
	retryAfter int
}

// A RenderOption wires one part of an [EnvelopeRenderer].
type RenderOption func(*EnvelopeRenderer)

// WithCodes replaces the vocabulary the kind and the default messages are
// resolved through — the place a service's own codes are declared.
func WithCodes(c *errs.Codes) RenderOption {
	return func(r *EnvelopeRenderer) { r.codes = c }
}

// WithMessages wires the message catalogue. Without one, a violation carries
// the code's declared default.
func WithMessages(m errs.MessageSource) RenderOption {
	return func(r *EnvelopeRenderer) { r.messages = m }
}

// WithResolvers wires the path-translation hops this layer applies, in order.
// The raw-body fallback runs after them, so a generated mapper always wins over
// a guess ([[D-043]]).
func WithResolvers(rs ...errs.Resolver) RenderOption {
	return func(r *EnvelopeRenderer) { r.resolvers = append(r.resolvers, rs...) }
}

// WithMaxViolations caps how many violations one body carries.
func WithMaxViolations(n int) RenderOption {
	return func(r *EnvelopeRenderer) { r.max = n }
}

// WithRetryAfter sets the seconds a 503 advertises.
func WithRetryAfter(seconds int) RenderOption {
	return func(r *EnvelopeRenderer) { r.retryAfter = seconds }
}

// NewRenderer builds the default renderer.
func NewRenderer(opts ...RenderOption) *EnvelopeRenderer {
	r := &EnvelopeRenderer{max: MaxViolations, retryAfter: DefaultRetryAfter}
	for _, o := range opts {
		if o != nil {
			o(r)
		}
	}
	return r
}

// codes is the vocabulary this renderer resolves through, or nil for the
// standard one. It is never a value: port answers the kind and the default
// message through behaviour, so a *errs.Codes a caller could reach never
// exists to be fought over.
func (r *EnvelopeRenderer) codesOrNil() *errs.Codes {
	if r == nil {
		return nil
	}
	return r.codes
}

// Status answers the status this renderer would give the error, without
// building a body — for a binding that has to decide before it renders.
func (r *EnvelopeRenderer) Status(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return StatusFor(port.KindOfWith(err, r.codesOrNil()))
}

// Render implements [Renderer].
//
// The order of the steps is load-bearing:
//
//  1. the fault, or one synthesised from the sentinel;
//  2. the status, from the kind and the precedence table;
//  3. **internal short-circuits here**, before anything is copied out of the
//     fault, so a 500 cannot carry a violation, a param or a path;
//  4. path translation, then the sort, then the cap, then the messages.
//
// Messages come after path translation because the message ladder is derived
// from the path. Expanding first would key a catalogue entry on the model's
// field name on one deployment and the client's on another, for the same
// violation.
func (r *EnvelopeRenderer) Render(ctx context.Context, err error) (int, http.Header, any) {
	if err == nil {
		return http.StatusOK, nil, nil
	}
	f := port.FaultOf(err)
	status := StatusFor(port.KindOfWith(err, r.codesOrNil()))
	if status == http.StatusInternalServerError {
		return status, nil, Internal()
	}

	vs := r.violations(ctx, f)
	env := Envelope{Type: "error", Partial: f.Partial, Errors: group(vs)}
	if len(vs) < len(f.Violations) {
		env.Partial = true
	}

	var h http.Header
	if status == http.StatusServiceUnavailable && r.retryAfter > 0 {
		h = http.Header{"Retry-After": []string{strconv.Itoa(r.retryAfter)}}
	}
	return status, h, env
}

// violations is the copy every later step works on. The fault is a value two
// goroutines may render at once ([[D-042]]), so nothing here writes through to
// it — a resolved path or an expanded message landing on the shared fault would
// make the second render depend on the first.
func (r *EnvelopeRenderer) violations(ctx context.Context, f *errs.Fault) []errs.Violation {
	vs := make([]errs.Violation, 0, max(len(f.Violations), 1))
	vs = append(vs, f.Violations...)
	if len(vs) == 0 {
		// A 404 and a bare 403 carry none. The status alone is not a thing a
		// client can branch on, so the fault's own code becomes one violation.
		code := f.Code
		if code == "" {
			code = port.CodeForKind(f.Kind)
		}
		vs = append(vs, errs.Violation{Code: code, Message: f.Message})
	}

	hops := errs.Chain(append(append([]errs.Resolver{}, r.resolvers...), bodyResolverFrom(ctx))...)
	for i := range vs {
		p, ok := hops.Resolve(vs[i].Path)
		vs[i].Path = p
		if !ok {
			vs[i].Approximate = true
		}
	}

	errs.SortViolations(vs)
	if r.max > 0 && len(vs) > r.max {
		vs = vs[:r.max]
	}
	locale := LocaleFrom(ctx)
	for i := range vs {
		vs[i].Message = r.message(ctx, vs[i], locale)
	}
	return vs
}

// message is §9's ladder: the catalogue, then the code's declared default, then
// the code itself. Never the driver's text, and never a template with an
// unexpanded placeholder still in it — errs.Messages falls back one level up
// rather than emitting {max}.
func (r *EnvelopeRenderer) message(ctx context.Context, v errs.Violation, locale string) string {
	if r.messages != nil {
		if m, ok := r.messages.Message(ctx, v, locale); ok && m != "" {
			return m
		}
	}
	if v.Message != "" {
		return v.Message
	}
	if m, ok := r.defaultMessage(v.Code); ok {
		return m
	}
	return string(v.Code)
}

// defaultMessage is the rung below the catalogue: what the vocabulary declares
// for the code.
func (r *EnvelopeRenderer) defaultMessage(code errs.Code) (string, bool) {
	if c := r.codesOrNil(); c != nil {
		return c.MessageFor(code)
	}
	return port.DefaultMessage(code)
}
