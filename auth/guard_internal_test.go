package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestNewGuardDoesNotPublishTheOptionDraft(t *testing.T) {
	var retained *guardConfig
	option := guardOption(func(cfg *guardConfig) { retained = cfg })
	guard := NewGuard(AuthenticatorFunc(func(context.Context, Credential) (Principal, error) {
		return Claims{Sub: "u-1"}, nil
	}), option)
	if retained == nil {
		t.Fatal("the control option did not see the construction draft")
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 1000; i++ {
			retained.optional = i%2 == 0
			retained.header = "X-Mutated"
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 1000; i++ {
			_, err := guard.Authenticate(context.Background(), func(string) string { return "" })
			if !errors.Is(err, ErrUnauthenticated) {
				t.Errorf("a retained option draft changed a published guard: %v", err)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
}
