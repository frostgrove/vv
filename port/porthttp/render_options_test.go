package porthttp

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/shardit-io/vv/errs"
)

// An option a consumer sets and nothing asserts is an option that can be
// dropped in a refactor without a test noticing, so every test here measures
// the answer changing rather than the option being constructible.

// retryable is the fault a 503 is rendered from.
func retryable() error {
	return errs.Retryable().Code(errs.CodeDeadlock).Fault()
}

// The framework does not retry on the caller's behalf ([[D-040]]); the header
// is the whole of the hint, so the number a consumer sets has to be the number
// on the wire.
func TestA503AdvertisesTheRetryAfterTheConsumerSet(t *testing.T) {
	status, h, _ := NewRenderer(WithRetryAfter(30)).Render(context.Background(), retryable())
	if status != http.StatusServiceUnavailable {
		t.Fatalf("a retryable fault rendered %d, want 503", status)
	}
	if got := h.Get("Retry-After"); got != "30" {
		t.Fatalf("the consumer set Retry-After to 30 and the response carries %q", got)
	}

	// The control: unconfigured, the same 503 still carries the default. A
	// renderer that had stopped setting the header at all would pass the leg
	// above only if WithRetryAfter were the thing that turned it on, and this
	// says it is not.
	_, h, _ = NewRenderer().Render(context.Background(), retryable())
	if got, want := h.Get("Retry-After"), strconv.Itoa(DefaultRetryAfter); got != want {
		t.Fatalf("an unconfigured 503 carries Retry-After %q, want the default %q", got, want)
	}

	// Zero is how a consumer says nothing honest can be advertised, so no
	// header is written rather than one saying "retry immediately".
	if _, h, _ = NewRenderer(WithRetryAfter(0)).Render(context.Background(), retryable()); h.Get("Retry-After") != "" {
		t.Fatalf("Retry-After 0 was advertised as %q", h.Get("Retry-After"))
	}

	// And the header belongs to the status, not to the renderer: a conflict
	// from the same renderer carries none.
	conflict := errs.Conflict().Code(errs.CodeUnique).Field("email").Code(errs.CodeUnique).Fault()
	status, h, _ = NewRenderer(WithRetryAfter(30)).Render(context.Background(), conflict)
	if status != http.StatusConflict || h.Get("Retry-After") != "" {
		t.Fatalf("a %d carries Retry-After %q; the header is not tied to the status", status, h.Get("Retry-After"))
	}
}

// tooYoung is a code the standard vocabulary has never heard of — a service's
// own, which is the case WithCodes exists for.
const tooYoung errs.Code = "too_young"

func ownCodes(t *testing.T) *errs.Codes {
	t.Helper()
	c := errs.NewCodes()
	if err := c.Add(tooYoung, errs.KindConflict, "you are not old enough for this"); err != nil {
		t.Fatalf("declaring the service's own code: %v", err)
	}
	return c
}

// A service's own vocabulary decides both halves of the answer: which status
// the code is worth, and what the violation says when no catalogue matched.
func TestAConsumersVocabularyDecidesTheStatusAndTheDefaultMessage(t *testing.T) {
	// The fault's own kind is the weaker of the two, so the code is what moves
	// the answer — and a renderer that ignored WithCodes would leave it at 400.
	f := errs.BadRequest().Code(errs.CodeBadQuery).
		Field("age").Code(tooYoung).Fault()

	codes := ownCodes(t)
	status, _, body := NewRenderer(WithCodes(codes)).Render(context.Background(), f)
	if status != http.StatusConflict {
		t.Fatalf("a code the consumer declared a conflict rendered %d, want 409", status)
	}
	env, _ := body.(Envelope)
	if len(env.Errors.Validation) != 1 {
		t.Fatalf("the body carries %+v, want the one violation", env.Errors)
	}
	if got := env.Errors.Validation[0].Message; got != "you are not old enough for this" {
		t.Fatalf("the violation says %q, want the message the consumer declared for the code", got)
	}

	// The control. The same fault through the standard vocabulary: the code is
	// undeclared, so it contributes no kind and has no message — 400, and the
	// code itself as the sentence. Without this leg the assertions above would
	// pass for a renderer that answered 409 for everything.
	status, _, body = NewRenderer().Render(context.Background(), f)
	if status != http.StatusBadRequest {
		t.Fatalf("an undeclared code rendered %d, want the fault's own 400", status)
	}
	env, _ = body.(Envelope)
	if got := env.Errors.Validation[0].Message; got != string(tooYoung) {
		t.Fatalf("an undeclared code says %q, want the code itself", got)
	}

	// Status answers without building a body, and it has to answer the same
	// thing — a binding that decides before it renders must not decide
	// differently ([[D-045]]: the kind is port's, the table is this package's).
	if got := NewRenderer(WithCodes(codes)).Status(f); got != http.StatusConflict {
		t.Fatalf("Status answered %d for a fault Render calls 409", got)
	}
	if got := NewRenderer().Status(f); got != http.StatusBadRequest {
		t.Fatalf("Status answered %d without the vocabulary, want 400", got)
	}
}

// Status is what a binding calls when it has to know the status before it has a
// body — the streaming case, and the one that decides whether to render at all.
func TestStatusAnswersWhatRenderWouldWithoutABody(t *testing.T) {
	r := NewRenderer()
	for _, tc := range []struct {
		what string
		err  error
	}{
		{"a miss", errs.NotFound().Code(errs.CodeNotFound).Fault()},
		{"a validation failure", errs.Validation().Field("name").Code(errs.CodeRequired).Fault()},
		{"a conflict", errs.Conflict().Code(errs.CodeUnique).Fault()},
		{"a classification that failed", errs.Internal().Fault()},
		{"a retryable failure", retryable()},
	} {
		want, _, _ := r.Render(context.Background(), tc.err)
		if got := r.Status(tc.err); got != want {
			t.Fatalf("Status calls %s %d and Render calls it %d", tc.what, got, want)
		}
	}

	// The control: no error is 200, so the agreement above is not a pair of
	// functions that both answer one constant.
	if got := r.Status(nil); got != http.StatusOK {
		t.Fatalf("Status answered %d for no error at all", got)
	}
	if status, _, body := r.Render(context.Background(), nil); status != http.StatusOK || body != nil {
		t.Fatalf("Render answered %d with %v for no error at all, want 200 and no body", status, body)
	}
}
