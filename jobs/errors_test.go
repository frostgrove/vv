package jobs

import (
	"errors"
	"strings"
	"testing"
)

func TestJobErrorsRemainDistinctAndWrappable(t *testing.T) {
	sentinels := []error{
		ErrInvalid,
		ErrTooLarge,
		ErrConflict,
		ErrSaturated,
		ErrUnsupported,
		ErrNotActivated,
		ErrCorrupt,
		ErrCancelled,
		ErrTerminated,
		ErrLeaseLost,
		ErrAmbiguous,
	}
	for left := range sentinels {
		if sentinels[left] == nil || sentinels[left].Error() == "" {
			t.Fatalf("sentinel %d is empty", left)
		}
		for right := left + 1; right < len(sentinels); right++ {
			if errors.Is(sentinels[left], sentinels[right]) {
				t.Fatalf("sentinels %d and %d are not distinct", left, right)
			}
		}
	}
	if err := invalid("field"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid helper = %v", err)
	}
	if err := tooLarge("field"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too-large helper = %v", err)
	}
	if strings.Contains(invalid("field").Error(), "secret-value") || strings.Contains(tooLarge("field").Error(), "secret-value") {
		t.Fatal("bound errors disclosed an unrelated value")
	}
}
