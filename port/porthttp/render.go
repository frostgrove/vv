package porthttp

import (
	"context"
	"net/http"
	"strconv"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type Renderer interface {
	Render(ctx context.Context, err error) (int, http.Header, any)
}

const MaxViolations = port.MaxViolations

const DefaultRetryAfter = 1

type EnvelopeRenderer struct {
	codes      *errs.Codes
	messages   errs.MessageSource
	resolvers  []errs.Resolver
	max        int
	retryAfter int
}

type RenderOption func(*EnvelopeRenderer)

func WithCodes(c *errs.Codes) RenderOption {
	return func(r *EnvelopeRenderer) { r.codes = c }
}

func WithMessages(m errs.MessageSource) RenderOption {
	return func(r *EnvelopeRenderer) { r.messages = m }
}

func WithResolvers(rs ...errs.Resolver) RenderOption {
	return func(r *EnvelopeRenderer) { r.resolvers = append(r.resolvers, rs...) }
}

func WithMaxViolations(n int) RenderOption {
	return func(r *EnvelopeRenderer) { r.max = n }
}

func WithRetryAfter(seconds int) RenderOption {
	return func(r *EnvelopeRenderer) { r.retryAfter = seconds }
}

func NewRenderer(options ...RenderOption) *EnvelopeRenderer {
	r := &EnvelopeRenderer{max: MaxViolations, retryAfter: DefaultRetryAfter}
	for _, o := range options {
		if o != nil {
			o(r)
		}
	}
	return r
}

func (this *EnvelopeRenderer) codesOrNil() *errs.Codes {
	if this == nil {
		return nil
	}
	return this.codes
}

func (this *EnvelopeRenderer) Status(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return StatusFor(port.KindOfWith(err, this.codesOrNil()))
}

func (this *EnvelopeRenderer) Render(ctx context.Context, err error) (int, http.Header, any) {
	if err == nil {
		return http.StatusOK, nil, nil
	}
	f := port.FaultOf(err)
	status := StatusFor(port.KindOfWith(err, this.codesOrNil()))
	if status == http.StatusInternalServerError {
		return status, nil, Internal()
	}

	vs := port.Violations(ctx, f, &port.ViolationOptions{
		Resolvers: this.resolvers,
		Fallback:  bodyResolverFrom(ctx),
		Messages:  this.messages,
		Codes:     this.codesOrNil(),
		Max:       this.max,
	})
	env := Envelope{Type: "error", Partial: f.Partial, Errors: group(vs)}
	if len(vs) < len(f.Violations) {
		env.Partial = true
	}

	var h http.Header
	if status == http.StatusServiceUnavailable && this.retryAfter > 0 {
		h = http.Header{"Retry-After": []string{strconv.Itoa(this.retryAfter)}}
	}
	return status, h, env
}
