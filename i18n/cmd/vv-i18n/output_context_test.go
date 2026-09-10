package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/i18n"
)

type commandCancelOnPollContext struct {
	mu        sync.Mutex
	remaining int
	done      chan struct{}
}

func newCommandCancelOnPollContext(polls int) *commandCancelOnPollContext {
	return &commandCancelOnPollContext{remaining: polls, done: make(chan struct{})}
}

func (c *commandCancelOnPollContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *commandCancelOnPollContext) Done() <-chan struct{} { return c.done }

func (c *commandCancelOnPollContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remaining == 0 {
		return context.Canceled
	}
	c.remaining--
	if c.remaining == 0 {
		close(c.done)
		return context.Canceled
	}
	return nil
}

func (c *commandCancelOnPollContext) Value(any) any { return nil }

func TestReviewValidatesSelectorBeforeCloningCatalog(t *testing.T) {
	tests := []reviewSelector{
		{locale: "not a locale", scope: "all", state: i18n.ReviewApproved},
		{locale: "en", scope: "unknown", state: i18n.ReviewApproved},
		{locale: "en", scope: "all", state: i18n.ReviewUnset},
	}
	for _, selector := range tests {
		cloned := false
		_, _, err := reviewCatalogWithClone(context.Background(), i18n.CatalogSpec{}, selector, func(context.Context, i18n.CatalogSpec) (i18n.CatalogSpec, error) {
			cloned = true
			return i18n.CatalogSpec{}, errors.New("clone called")
		})
		if err == nil || cloned {
			t.Fatalf("invalid selector %+v cloned=%v, error=%v", selector, cloned, err)
		}
	}
}

func TestCommandJSONEncodingPollsContext(t *testing.T) {
	values := make([]string, 100000)
	for index := range values {
		values[index] = "value"
	}
	ctx := newCommandCancelOnPollContext(8)
	if _, err := encodeCommandJSONBoundedContext(ctx, "values", values, maximumCommandInput); !errors.Is(err, context.Canceled) {
		t.Fatalf("JSON cancellation error = %v", err)
	}
}

func TestUsageOutputFloorRejectsBeforeProportionalEncoding(t *testing.T) {
	keys := make([]i18n.Key, 100000)
	if _, err := encodeUsageBoundedContext(context.Background(), i18n.UsageManifest{Keys: keys}, 64); !errors.Is(err, errCommandJSONTooLarge) {
		t.Fatalf("usage output floor error = %v", err)
	}
}
