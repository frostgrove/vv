package auth_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port/porthttp"
)

const reason = "signature does not verify"

func render(t *testing.T, err error) (int, string) {
	t.Helper()
	status, _, body := porthttp.NewRenderer().Render(t.Context(), err)
	b, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		t.Fatalf("the envelope did not marshal: %v", marshalErr)
	}
	return status, string(b)
}

func wrappedText(t *testing.T, err error) string {
	t.Helper()
	var f *errs.Fault
	if !errors.As(err, &f) {
		t.Fatalf("the failure is not a fault, so nothing can classify it: %v", err)
	}
	var b strings.Builder
	for _, w := range f.Unwrap() {
		b.WriteString(w.Error())
	}
	return b.String()
}

func TestAnAuthenticationFailureIsA401ThatWrapsTheSentinel(t *testing.T) {
	err := auth.Unauthenticated(reason)

	t.Run("errors.Is reaches the sentinel", func(t *testing.T) {
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatal("the failure does not match auth.ErrUnauthenticated, so no caller can branch on it")
		}
	})

	t.Run("the reason survives for a log", func(t *testing.T) {
		if !strings.Contains(wrappedText(t, err), reason) {
			t.Fatalf("the reason is gone from the wrapped error too, so nothing can diagnose the refusal")
		}
	})

	t.Run("the status is 401", func(t *testing.T) {
		status, _ := render(t, err)
		if status != http.StatusUnauthorized {
			t.Fatalf("an authentication failure rendered %d, want 401", status)
		}
	})

	t.Run("the body carries the code and its declared message", func(t *testing.T) {
		_, body := render(t, err)
		if !strings.Contains(body, string(errs.CodeUnauthenticated)) {
			t.Fatalf("the body names no code, so a client cannot branch: %s", body)
		}
		if !strings.Contains(body, "authentication is required") {
			t.Fatalf("the body carries no message: %s", body)
		}
	})
}

func TestTheReasonForA401NeverReachesTheBody(t *testing.T) {
	t.Run("Unauthenticated keeps the reason out", func(t *testing.T) {
		_, body := render(t, auth.Unauthenticated(reason))
		if strings.Contains(body, reason) {
			t.Fatalf("the reason reached the client and says which half of the token to fix: %s", body)
		}
	})

	t.Run("control: the same reason in Fault.Message does reach the body", func(t *testing.T) {
		leaky := errs.Unauthorized().
			Code(errs.CodeUnauthenticated).
			Message(reason).
			Wrapping(auth.ErrUnauthenticated).
			Fault()
		_, body := render(t, leaky)
		if !strings.Contains(body, reason) {
			t.Fatalf("Fault.Message no longer renders, so the test above proves nothing: %s", body)
		}
	})
}

func TestUnauthenticatedfFormatsTheReasonAndStillHidesIt(t *testing.T) {
	err := auth.Unauthenticatedf("token expired at %s", "2026-01-01")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("the formatted failure does not match the sentinel")
	}
	if !strings.Contains(wrappedText(t, err), "token expired at 2026-01-01") {
		t.Fatal("the format arguments were not expanded into the wrapped error")
	}
	if _, body := render(t, err); strings.Contains(body, "2026-01-01") {
		t.Fatalf("the formatted reason reached the client: %s", body)
	}
}
