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

type Renderer interface {
	Render(ctx context.Context, err error) *status.Status
}

const MaxViolations = port.MaxViolations

const DefaultRetryDelay = time.Second

const ErrorDomain = "vv"

const PartialKey = "partial"

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
	case errs.KindMethodNotAllowed:
		return codes.Unimplemented
	default:
		return codes.Internal
	}
}

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
	case codes.Unimplemented:
		return errs.KindMethodNotAllowed
	default:
		return errs.KindInternal
	}
}

func Code(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	return CodeFor(port.KindOf(err))
}

type StatusRenderer struct {
	codes      *errs.Codes
	messages   errs.MessageSource
	resolvers  []errs.Resolver
	max        int
	retryDelay time.Duration
}

type RenderOption func(*StatusRenderer)

func WithCodes(c *errs.Codes) RenderOption {
	return func(r *StatusRenderer) { r.codes = c }
}

func WithMessages(m errs.MessageSource) RenderOption {
	return func(r *StatusRenderer) { r.messages = m }
}

func WithResolvers(rs ...errs.Resolver) RenderOption {
	return func(r *StatusRenderer) { r.resolvers = append(r.resolvers, rs...) }
}

func WithMaxViolations(n int) RenderOption {
	return func(r *StatusRenderer) { r.max = n }
}

func WithRetryDelay(d time.Duration) RenderOption {
	return func(r *StatusRenderer) { r.retryDelay = d }
}

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

func (this *StatusRenderer) codesOrNil() *errs.Codes {
	if this == nil {
		return nil
	}
	return this.codes
}

func (this *StatusRenderer) Code(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	return CodeFor(port.KindOfWith(err, this.codesOrNil()))
}

func (this *StatusRenderer) Render(ctx context.Context, err error) *status.Status {
	if err == nil {
		return nil
	}
	code := CodeFor(port.KindOfWith(err, this.codesOrNil()))
	if code == codes.Internal {
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
		port.Logger(ctx).Error("crudgrpc: attaching the error details", "err", attachErr)
		return st
	}
	return full
}

func headline(vs []errs.Violation) string {
	if len(vs) == 0 {
		return string(errs.CodeInternal)
	}
	if vs[0].Message != "" {
		return vs[0].Message
	}
	return string(vs[0].Code)
}

func (this *StatusRenderer) details(ctx context.Context, f *errs.Fault, vs []errs.Violation, code codes.Code) []protoadapt.MessageV1 {
	locale := port.LocaleFrom(ctx)
	br := &errdetails.BadRequest{FieldViolations: make([]*errdetails.BadRequest_FieldViolation, 0, len(vs))}
	for _, v := range vs {
		fv := &errdetails.BadRequest_FieldViolation{
			Field:       v.Path.String(),
			Description: v.Message,

			Reason: string(v.Code),
		}

		if locale != "" {
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
		info.Metadata = map[string]string{PartialKey: "true"}
	}

	out := []protoadapt.MessageV1{br, info}
	if code == codes.Unavailable && this.retryDelay > 0 {
		out = append(out, &errdetails.RetryInfo{RetryDelay: durationpb.New(this.retryDelay)})
	}
	return out
}
