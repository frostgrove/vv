package main

import (
	"context"
	"errors"
	"strconv"
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

func TestReviewStampedOutputLimitPrecedesCloneAtExactBoundary(t *testing.T) {
	spec := commandSourceFixture(t)
	translation := &spec.Modules[0].Messages[0].Translations[0]
	translation.ContractRevision = ""
	translation.SourceDigest = ""
	translation.ReviewDigest = ""
	selector := reviewSelector{locale: translation.Locale, key: "app.welcome", scope: "translations", state: i18n.ReviewRejected}
	updated, _, err := reviewCatalog(spec, selector)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := (i18n.SourceCodec{CatalogLimits: spec.Limits}).Encode(updated)
	if err != nil {
		t.Fatal(err)
	}
	maximum := len(raw)
	cloned := false
	codec := i18n.SourceCodec{Limits: i18n.ArtifactLimits{MaxBytes: maximum - 1}, CatalogLimits: spec.Limits}
	_, _, err = reviewCatalogBoundedWithClone(context.Background(), spec, selector, codec, maximum-1, func(context.Context, i18n.CatalogSpec) (i18n.CatalogSpec, error) {
		cloned = true
		return i18n.CatalogSpec{}, errors.New("clone called")
	})
	if !errors.Is(err, i18n.ErrLimitExceeded) || cloned {
		t.Fatalf("undersized reviewed source = cloned %v, error %v", cloned, err)
	}
	cloned = false
	codec.Limits.MaxBytes = maximum
	exact, count, err := reviewCatalogBoundedWithClone(context.Background(), spec, selector, codec, maximum, func(ctx context.Context, value i18n.CatalogSpec) (i18n.CatalogSpec, error) {
		cloned = true
		return cloneReviewCatalogContext(ctx, value)
	})
	if err != nil || !cloned || count != 1 {
		t.Fatalf("exact reviewed source = cloned %v, count %d, error %v", cloned, count, err)
	}
	exactRaw, err := codec.Encode(exact)
	if err != nil || len(exactRaw) != maximum {
		t.Fatalf("exact reviewed source bytes = %d, error %v", len(exactRaw), err)
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

func TestUsageOutputPreflightUsesExactWireBoundaryBeforeCloning(t *testing.T) {
	keys := make([]i18n.Key, 100)
	for index := range keys {
		keys[index] = i18n.Key("app.key" + strconv.Itoa(index))
	}
	for name, usage := range map[string]i18n.UsageManifest{
		"unscoped": {Keys: keys},
		"scoped":   testUsageDocumentManifest(),
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := encodeUsageBoundedContext(context.Background(), usage, maximumCommandOutputBytes)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Context(context.Background())
			if name == "unscoped" {
				ctx = newCommandCancelOnPollContext(104)
			}
			if _, err := encodeUsageBoundedContext(ctx, usage, len(raw)-1); !errors.Is(err, errCommandJSONTooLarge) {
				t.Fatalf("usage N-1 preflight error = %v", err)
			}
			if exact, err := encodeUsageBoundedContext(context.Background(), usage, len(raw)); err != nil || len(exact) != len(raw) {
				t.Fatalf("usage exact preflight bytes = %d, want %d, error %v", len(exact), len(raw), err)
			}
		})
	}
}
