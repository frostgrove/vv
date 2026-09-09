package i18n

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type cancelOnPollContext struct {
	mu        sync.Mutex
	remaining int
	done      chan struct{}
}

func newCancelOnPollContext(polls int) *cancelOnPollContext {
	return &cancelOnPollContext{remaining: polls, done: make(chan struct{})}
}

func (c *cancelOnPollContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *cancelOnPollContext) Done() <-chan struct{} { return c.done }

func (c *cancelOnPollContext) Err() error {
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

func (c *cancelOnPollContext) Value(any) any { return nil }

func TestDerivedOutputLimitsRefuseBeforeFormatterAndAddressWork(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("message", "Message")))
	formatted := false
	_, err := generateGoContext(context.Background(), snapshot, GoGeneratorSpec{Package: "messages", MaxOutputBytes: 32}, func([]byte) ([]byte, error) {
		formatted = true
		return nil, nil
	})
	if !errors.Is(err, ErrLimitExceeded) || formatted {
		t.Fatalf("bounded Go generation = formatted %v, error %v", formatted, err)
	}

	addressed := false
	_, err = exportPublicContext(context.Background(), snapshot, PublicExportSpec{MaxOutputBytes: 32}, func(context.Context, []byte, []byte) (string, error) {
		addressed = true
		return "", nil
	})
	if !errors.Is(err, ErrLimitExceeded) || addressed {
		t.Fatalf("bounded public export = addressed %v, error %v", addressed, err)
	}
}

func TestDerivedOutputLimitsAcceptExactFilesAndRejectOneByteLess(t *testing.T) {
	message := simpleMessage("public", "Public")
	message.Public = true
	snapshot := mustSnapshot(t, testCatalog(message))

	generated, err := GenerateGo(snapshot, GoGeneratorSpec{Package: "messages"})
	if err != nil {
		t.Fatal(err)
	}
	exactGo, err := GenerateGoContext(context.Background(), snapshot, GoGeneratorSpec{Package: "messages", MaxOutputBytes: len(generated)})
	if err != nil || string(exactGo) != string(generated) {
		t.Fatalf("exact Go output = %d bytes, error %v", len(exactGo), err)
	}
	if _, err := GenerateGoContext(context.Background(), snapshot, GoGeneratorSpec{Package: "messages", MaxOutputBytes: len(generated) - 1}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("undersized Go output error = %v", err)
	}

	exported, err := ExportPublic(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	maximum := max(len(exported.Manifest), len(exported.TypeScript))
	exactExport, err := ExportPublicContext(context.Background(), snapshot, PublicExportSpec{MaxOutputBytes: maximum})
	if err != nil || exactExport.Address != exported.Address {
		t.Fatalf("exact public export = %q, error %v", exactExport.Address, err)
	}
	if _, err := ExportPublicContext(context.Background(), snapshot, PublicExportSpec{MaxOutputBytes: maximum - 1}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("undersized public export error = %v", err)
	}
}

func TestBuildTransformsPollContextDuringMeaningfulLoops(t *testing.T) {
	spec := contextGenerationCatalog(256)
	snapshot := mustSnapshot(t, spec)
	tests := []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "new", run: func(ctx context.Context) error { _, err := NewContext(ctx, spec); return err }},
		{name: "compile", run: func(ctx context.Context) error { _, err := (Compiler{}).CompileContext(ctx, spec); return err }},
		{name: "encode artifact", run: func(ctx context.Context) error { _, err := (Compiler{}).EncodeContext(ctx, snapshot); return err }},
		{name: "encode source", run: func(ctx context.Context) error { _, err := (SourceCodec{}).EncodeContext(ctx, spec); return err }},
		{name: "generate Go", run: func(ctx context.Context) error {
			_, err := GenerateGoContext(ctx, snapshot, GoGeneratorSpec{Package: "messages"})
			return err
		}},
		{name: "export public", run: func(ctx context.Context) error {
			_, err := ExportPublicContext(ctx, snapshot, PublicExportSpec{})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := newCancelOnPollContext(40)
			if err := test.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation error = %v", err)
			}
		})
	}
}

func TestPseudoPollsContextAndHonorsSourceOutputLimit(t *testing.T) {
	spec := contextGenerationCatalog(64)
	spec.Supported = []string{"en", "en-XA"}
	ctx := newCancelOnPollContext(80)
	if _, err := PseudoContext(ctx, spec, PseudoSpec{Locale: "en-XA", Mode: PseudoAccent}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pseudolocale cancellation error = %v", err)
	}
	if _, err := PseudoContext(context.Background(), spec, PseudoSpec{Locale: "en-XA", Mode: PseudoAccent, MaxOutputBytes: 32}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("pseudolocale output limit error = %v", err)
	}
}

func TestPublicAddressHashPollsContextBetweenChunks(t *testing.T) {
	ctx := newCancelOnPollContext(12)
	if _, err := expectedPublicExportAddressContext(ctx, make([]byte, 1<<20), make([]byte, 1<<20)); !errors.Is(err, context.Canceled) {
		t.Fatalf("public address cancellation error = %v", err)
	}
}

func contextGenerationCatalog(count int) CatalogSpec {
	messages := make([]MessageSpec, count)
	for index := range messages {
		messages[index] = simpleMessage(fmt.Sprintf("m%05d", index), "Message")
		messages[index].Public = true
	}
	limits := DefaultLimits()
	limits.MaxMessages = count
	return CatalogSpec{
		Revision: "context-v1", Profile: GrammarProfile, SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"en"}, Required: []string{"en"}, DefaultTimeZone: "UTC", Limits: limits,
		Modules: []Module{{Name: "app", Messages: messages}},
	}
}
