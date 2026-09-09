package porthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func retryable() error {
	return errs.Retryable().Code(errs.CodeDeadlock).Fault()
}

func TestAnObserverSeesOneInternalCauseBeforeItIsRedacted(t *testing.T) {
	secret := errors.New("database password and host")
	key := struct{}{}
	ctx := context.WithValue(context.Background(), key, "request-42")
	var calls int
	var seenContext any
	var seenErr error

	status, _, body := NewRenderer(WithObserver(func(ctx context.Context, err error) {
		calls++
		seenContext = ctx.Value(key)
		seenErr = err
	})).Render(ctx, secret)
	if status != http.StatusInternalServerError || !isInternal(body) {
		t.Fatalf("an internal failure rendered %d with %#v", status, body)
	}
	if calls != 1 || seenContext != "request-42" || !errors.Is(seenErr, secret) {
		t.Fatalf("observer calls=%d context=%v error=%v", calls, seenContext, seenErr)
	}

	conflict := errs.Conflict().Code(errs.CodeConflict).Fault()
	_, _, _ = NewRenderer(WithObserver(func(context.Context, error) { calls++ })).Render(ctx, conflict)
	if calls != 1 {
		t.Fatalf("a non-internal failure called the observer; calls=%d", calls)
	}
}

func TestAPanickingObserverCannotChangeTheInternalResponse(t *testing.T) {
	secret := errors.New("private failure")
	status, header, body := NewRenderer(WithObserver(func(context.Context, error) {
		panic("observer failed")
	})).Render(context.Background(), secret)
	if status != http.StatusInternalServerError || len(header) != 0 || !isInternal(body) {
		t.Fatalf("a panicking observer changed the response to %d %#v %#v", status, header, body)
	}
}

func isInternal(body any) bool {
	env, ok := body.(Envelope)
	return ok && env.Type == "error" && len(env.Errors.Validation) == 0 && len(env.Errors.General) == 1 && env.Errors.General[0].Code == errs.CodeInternal
}

func TestA503AdvertisesTheRetryAfterTheConsumerSet(t *testing.T) {
	status, h, _ := NewRenderer(WithRetryAfter(30)).Render(context.Background(), retryable())
	if status != http.StatusServiceUnavailable {
		t.Fatalf("a retryable fault rendered %d, want 503", status)
	}
	if got := h.Get("Retry-After"); got != "30" {
		t.Fatalf("the consumer set Retry-After to 30 and the response carries %q", got)
	}

	_, h, _ = NewRenderer().Render(context.Background(), retryable())
	if got, want := h.Get("Retry-After"), strconv.Itoa(DefaultRetryAfter); got != want {
		t.Fatalf("an unconfigured 503 carries Retry-After %q, want the default %q", got, want)
	}

	if _, h, _ = NewRenderer(WithRetryAfter(0)).Render(context.Background(), retryable()); h.Get("Retry-After") != "" {
		t.Fatalf("Retry-After 0 was advertised as %q", h.Get("Retry-After"))
	}

	conflict := errs.Conflict().Code(errs.CodeUnique).Field("email").Code(errs.CodeUnique).Fault()
	status, h, _ = NewRenderer(WithRetryAfter(30)).Render(context.Background(), conflict)
	if status != http.StatusConflict || h.Get("Retry-After") != "" {
		t.Fatalf("a %d carries Retry-After %q; the header is not tied to the status", status, h.Get("Retry-After"))
	}
}

const tooYoung errs.Code = "too_young"

func ownCodes(t *testing.T) *errs.Codes {
	t.Helper()
	c := errs.NewCodes()
	if err := c.Add(tooYoung, errs.KindConflict, "you are not old enough for this"); err != nil {
		t.Fatalf("declaring the service's own code: %v", err)
	}
	return c
}

func TestAConsumersVocabularyDecidesTheStatusAndTheDefaultMessage(t *testing.T) {
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

	status, _, body = NewRenderer().Render(context.Background(), f)
	if status != http.StatusBadRequest {
		t.Fatalf("an undeclared code rendered %d, want the fault's own 400", status)
	}
	env, _ = body.(Envelope)
	if got := env.Errors.Validation[0].Message; got != string(tooYoung) {
		t.Fatalf("an undeclared code says %q, want the code itself", got)
	}

	if got := NewRenderer(WithCodes(codes)).Status(f); got != http.StatusConflict {
		t.Fatalf("Status answered %d for a fault Render calls 409", got)
	}
	if got := NewRenderer().Status(f); got != http.StatusBadRequest {
		t.Fatalf("Status answered %d without the vocabulary, want 400", got)
	}
}

func TestContentLanguageNamesTheTemplatesActuallyUsed(t *testing.T) {
	messages := errs.NewMessages(nil)
	if err := messages.Add("fr", "email.unique", "déjà pris"); err != nil {
		t.Fatal(err)
	}
	fault := errs.Conflict().Code(errs.CodeUnique).Field("email").Code(errs.CodeUnique).Fault()
	ctx := WithLocale(context.Background(), "fr-CA")

	_, header, body := NewRenderer(WithMessages(messages)).Render(ctx, fault)
	if got := header.Get("Content-Language"); got != "fr" {
		t.Fatalf("Content-Language is %q, want the actual fr fallback", got)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "MessageLocale") || strings.Contains(string(raw), "message_locale") {
		t.Fatalf("the internal message locale leaked into the stable envelope: %s", raw)
	}

	_, header, _ = NewRenderer(WithMessages(catalogueSource{})).Render(ctx, fault)
	if got := header.Get("Content-Language"); got != "" {
		t.Fatalf("a source that cannot prove its template locale emitted %q", got)
	}
}

type catalogueSource struct{}

func (catalogueSource) Message(context.Context, errs.Violation, string) (string, bool) {
	return "déjà pris", true
}

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

	if got := r.Status(nil); got != http.StatusOK {
		t.Fatalf("Status answered %d for no error at all", got)
	}
	if status, _, body := r.Render(context.Background(), nil); status != http.StatusOK || body != nil {
		t.Fatalf("Render answered %d with %v for no error at all, want 200 and no body", status, body)
	}
}
