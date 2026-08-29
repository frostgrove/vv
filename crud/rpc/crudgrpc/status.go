package crudgrpc

import (
	"context"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

// A Renderer turns an error into a status. It is the seam a consumer replaces
// to ship its own codes or its own details.
//
// It is not crudhttp.Renderer and cannot be: that interface answers an
// http.Header. This is [[D-045]]'s split made concrete on a second protocol —
// the renderer is transport-shaped and the pipeline behind it is not.
type Renderer interface {
	// Render answers the status a failed call carries. A nil error answers nil.
	Render(ctx context.Context, err error) *status.Status
}

// MaxViolations is how many violations one status carries before the rest are
// dropped and the partial marker is set. The number is port's, so a status and
// an envelope are capped by the same rule.
const MaxViolations = port.MaxViolations

// DefaultRetryDelay is the RetryInfo an Unavailable status carries. The
// framework does not retry on the caller's behalf ([[D-040]]); this is the
// smallest honest hint that retrying is the right thing at all. It is the
// counterpart of crudhttp.DefaultRetryAfter, which says one second in the
// header a 503 carries.
const DefaultRetryDelay = time.Second

// ErrorDomain is the domain an ErrorInfo detail reports, which is what scopes
// the reason a client branches on. It names the library and nothing about the
// deployment: a domain built from the service, the schema or the host would be
// the one field on the detail that carries something internal ([[D-044]]).
const ErrorDomain = "vv"

// PartialKey marks a detail list the cap cut short. A status that carries four
// violations without it is claiming there were four.
const PartialKey = "partial"

// CodeFor is `ROADMAP-errors.md` §2's table in gRPC's vocabulary, arm by arm.
//
// Written out rather than derived from the Kind's numeric value, for the reason
// crudhttp.StatusFor gives: errs.Kind documents those values as "not API", and
// a table indexed by them would make a declaration order part of the wire
// contract the first time somebody inserted a kind.
//
// Two answers differ from the HTTP table in a way a client can see, and both
// are costs rather than oversights ([[FL-013]]).
//
// A validation failure and a malformed request are both InvalidArgument. HTTP
// tells them apart with 422 and 400; gRPC has one code for "the request was
// wrong", so on this transport the machine code in the details is what
// separates them. That is the code doing the job it was always meant to do.
//
// A body past the cap is ResourceExhausted, which is gRPC's own answer for a
// message over its receive limit and therefore the code a client already has a
// branch for. HTTP says 413.
//
// Every conflict is AlreadyExists, including a restrict violation and a stale
// version, where FailedPrecondition and Aborted would each read better.
// Refining per code would be a second table keyed on something [[D-049]] says
// must not decide a response, and a service that declared fifty codes of its
// own would then have fifty rows to keep. The kind decides.
func CodeFor(k errs.Kind) codes.Code {
	switch k {
	case errs.KindNotFound:
		return codes.NotFound
	case errs.KindUnauthorized:
		return codes.Unauthenticated
	case errs.KindForbidden:
		return codes.PermissionDenied
	case errs.KindRetryable:
		return codes.Unavailable
	case errs.KindConflict:
		return codes.AlreadyExists
	case errs.KindValidation:
		return codes.InvalidArgument
	case errs.KindBadRequest:
		return codes.InvalidArgument
	case errs.KindTooLarge:
		return codes.ResourceExhausted
	default:
		return codes.Internal
	}
}

// KindForCode is [CodeFor] read backwards: the kind a client recovers from the
// code a service answered.
//
// It lives beside the table it inverts because the two have to agree, and two
// files would agree until the first time one of them gained a row.
//
// InvalidArgument is where the forward table is not injective — CodeFor sends
// both KindValidation and KindBadRequest to it ([[D-052]]) — so this answers
// the coarser of the two and the decoder sharpens it with the code the status
// carried in its ErrorInfo. That is the machine code doing the job D-052 gave
// it when it accepted the collapse: on this transport the code carries the
// distinction the status carries over HTTP.
//
// Every other arm is exact. A code outside the table is [errs.KindInternal],
// which is what an unrecognised failure means on this side too.
func KindForCode(c codes.Code) errs.Kind {
	switch c {
	case codes.NotFound:
		return errs.KindNotFound
	case codes.Unauthenticated:
		return errs.KindUnauthorized
	case codes.PermissionDenied:
		return errs.KindForbidden
	case codes.Unavailable:
		return errs.KindRetryable
	case codes.AlreadyExists:
		return errs.KindConflict
	case codes.InvalidArgument:
		return errs.KindBadRequest
	case codes.ResourceExhausted:
		return errs.KindTooLarge
	default:
		return errs.KindInternal
	}
}

// Code maps a repository or query error to a gRPC code, using the standard
// vocabulary.
//
// It is exported so an application answering its own calls gets the same codes
// without reimplementing the table, which is [[UC-015]] guarantee 8 on a fourth
// transport. The kind is port's answer and the code is this package's table —
// [[D-045]]'s split in one line.
func Code(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	return CodeFor(port.KindOf(err))
}

// A StatusRenderer is the default [Renderer]: the code from the kind, a
// sentence from the message ladder, and the violations as details.
type StatusRenderer struct {
	codes      *errs.Codes
	messages   errs.MessageSource
	resolvers  []errs.Resolver
	max        int
	retryDelay time.Duration
}

// A RenderOption wires one part of a [StatusRenderer].
type RenderOption func(*StatusRenderer)

// WithCodes replaces the vocabulary the kind and the default messages are
// resolved through — the place a service's own codes are declared.
func WithCodes(c *errs.Codes) RenderOption {
	return func(r *StatusRenderer) { r.codes = c }
}

// WithMessages wires the message catalogue. Without one, a violation carries
// the code's declared default.
func WithMessages(m errs.MessageSource) RenderOption {
	return func(r *StatusRenderer) { r.messages = m }
}

// WithResolvers wires the path-translation hops this layer applies, in order.
func WithResolvers(rs ...errs.Resolver) RenderOption {
	return func(r *StatusRenderer) { r.resolvers = append(r.resolvers, rs...) }
}

// WithMaxViolations caps how many violations one status carries.
func WithMaxViolations(n int) RenderOption {
	return func(r *StatusRenderer) { r.max = n }
}

// WithRetryDelay sets the delay an Unavailable status advertises.
func WithRetryDelay(d time.Duration) RenderOption {
	return func(r *StatusRenderer) { r.retryDelay = d }
}

// NewRenderer builds the default renderer.
func NewRenderer(options ...RenderOption) *StatusRenderer {
	r := &StatusRenderer{max: MaxViolations, retryDelay: DefaultRetryDelay}
	for _, o := range options {
		if o != nil {
			o(r)
		}
	}
	return r
}

var _ Renderer = (*StatusRenderer)(nil)

// codesOrNil is the vocabulary this renderer resolves through, or nil for the
// standard one. It is never a value: port answers the kind and the default
// message through behaviour, so a *errs.Codes a caller could reach never exists
// to be fought over.
func (this *StatusRenderer) codesOrNil() *errs.Codes {
	if this == nil {
		return nil
	}
	return this.codes
}

// Code answers the code this renderer would give the error, without building
// the details — for a caller that has to decide before it renders.
func (this *StatusRenderer) Code(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	return CodeFor(port.KindOfWith(err, this.codesOrNil()))
}

// Render implements [Renderer].
//
// The order of the steps is the same as the envelope's, and load-bearing for
// the same reason:
//
//  1. the fault, or one synthesised from the sentinel;
//  2. the code, from the kind and the precedence table;
//  3. **internal short-circuits here**, before anything is copied out of the
//     fault, so an Internal status cannot carry a violation, a param or a path;
//  4. the pipeline — translate, sort, cap, then the messages — and the details.
//
// The status message is never err.Error(). A fault's own text names the entity
// it happened on ("errs: Save users: conflict: unique"), and a table name in a
// status message is exactly the disclosure [[D-044]] closes. What is used
// instead is the message ladder's answer: for a fault carrying violations, the
// first one in the rendered order; for a fault carrying none, the ladder's
// answer for the fault's own code, which the pipeline synthesises.
func (this *StatusRenderer) Render(ctx context.Context, err error) *status.Status {
	if err == nil {
		return nil
	}
	code := CodeFor(port.KindOfWith(err, this.codesOrNil()))
	if code == codes.Internal {
		// The one status an internal failure ever has. It carries no details,
		// so the silence holds by construction rather than by a case in a
		// switch somebody may edit: there is nowhere here for a driver's
		// sentence to go.
		return status.New(codes.Internal, string(errs.CodeInternal))
	}

	f := port.FaultOf(err)
	vs := port.Violations(ctx, f, &port.ViolationOptions{
		Resolvers: this.resolvers,
		Messages:  this.messages,
		Codes:     this.codesOrNil(),
		Max:       this.max,
	})

	st := status.New(code, headline(vs))
	full, attachErr := st.WithDetails(this.details(ctx, f, vs, code)...)
	if attachErr != nil {
		// A detail that will not marshal must not cost the caller its status.
		// The code and the sentence are what a client branches on; the details
		// are what it renders a form from.
		port.Logger(ctx).Error("crudgrpc: attaching the error details", "err", attachErr)
		return st
	}
	return full
}

// headline is the sentence the status carries. The pipeline never leaves a
// violation without one — the last rung of the ladder is the code itself — so
// the empty case is a fault with no violations and no code, which cannot
// happen and is handled anyway.
func headline(vs []errs.Violation) string {
	if len(vs) == 0 {
		return string(errs.CodeInternal)
	}
	if vs[0].Message != "" {
		return vs[0].Message
	}
	return string(vs[0].Code)
}

// details is the structured half: every violation as one field violation, the
// fault's own code as the reason a client branches on, and a retry hint when
// the answer is "try again".
//
// One list and one detail type, whatever the code is. `ROADMAP-errors.md` §16
// settled that a pathed and a pathless violation are never split into two
// lists, and a second detail type for the pathless ones would be exactly that
// split under another name. A violation that names no field becomes a field
// violation with an empty Field, which is lossless and is what the envelope's
// `general` group says in its own shape.
func (this *StatusRenderer) details(ctx context.Context, f *errs.Fault, vs []errs.Violation, code codes.Code) []protoadapt.MessageV1 {
	locale := port.LocaleFrom(ctx)
	br := &errdetails.BadRequest{FieldViolations: make([]*errdetails.BadRequest_FieldViolation, 0, len(vs))}
	for _, v := range vs {
		fv := &errdetails.BadRequest_FieldViolation{
			Field:       v.Path.String(),
			Description: v.Message,
			// Lowercase, against AIP's UPPER_SNAKE_CASE convention, and
			// deliberately. `ROADMAP-errors.md` §11 asks for *a stable machine
			// code*, and a code spelled `unique` in an envelope and `UNIQUE`
			// here is not stable — a client with two transports would need two
			// tables. Reason is a free-form string and the convention is a
			// style rule; the identity is the thing worth keeping ([[D-052]]).
			Reason: string(v.Code),
		}
		// Only when the client asked for a language. A LocalizedMessage with an
		// empty locale claims a translation nobody requested.
		if locale != "" {
			// The *requested* locale, not the one that answered.
			// errs.MessageSource returns the string and not the rung that won,
			// and extending that interface to report it would be a change to
			// errs — which is precisely what this phase claims not to need.
			fv.LocalizedMessage = &errdetails.LocalizedMessage{Locale: locale, Message: v.Message}
		}
		br.FieldViolations = append(br.FieldViolations, fv)
	}

	reason := f.Code
	if reason == "" {
		reason = port.CodeForKind(f.Kind)
	}
	info := &errdetails.ErrorInfo{Reason: string(reason), Domain: ErrorDomain}
	if f.Partial || len(vs) < len(f.Violations) {
		// The only key this map ever carries. Anything else is both a
		// determinism hazard — a proto map has no order — and the obvious place
		// an internal name would end up.
		info.Metadata = map[string]string{PartialKey: "true"}
	}

	out := []protoadapt.MessageV1{br, info}
	if code == codes.Unavailable && this.retryDelay > 0 {
		out = append(out, &errdetails.RetryInfo{RetryDelay: durationpb.New(this.retryDelay)})
	}
	return out
}
