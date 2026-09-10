package i18n

import (
	"context"
	"errors"
	"fmt"
	"go/format"
	"strings"
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

func TestPublicExportExactPreflightPrecedesMaterialization(t *testing.T) {
	privateSnapshot := mustSnapshot(t, testCatalog(simpleMessage("private", "Private")))
	materialized := false
	addressed := false
	_, err := exportPublicContextWithMessages(context.Background(), privateSnapshot, PublicExportSpec{MaxOutputBytes: 64}, func(context.Context, []byte, []byte) (string, error) {
		addressed = true
		return "", nil
	}, func(context.Context, *Snapshot) ([]publicContractMessage, error) {
		materialized = true
		return nil, nil
	})
	if !errors.Is(err, ErrLimitExceeded) || materialized || addressed {
		t.Fatalf("fixed export preflight = materialized %v, addressed %v, error %v", materialized, addressed, err)
	}

	message := simpleMessage("public", "Public")
	message.Public = true
	snapshot := mustSnapshot(t, testCatalog(message))
	exported, err := ExportPublic(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	maximum := max(len(exported.Manifest), len(exported.TypeScript))
	materialized = false
	addressed = false
	_, err = exportPublicContextWithMessages(context.Background(), snapshot, PublicExportSpec{MaxOutputBytes: maximum - 1}, func(context.Context, []byte, []byte) (string, error) {
		addressed = true
		return "", nil
	}, func(ctx context.Context, value *Snapshot) ([]publicContractMessage, error) {
		materialized = true
		return publicContractMessagesContext(ctx, value)
	})
	if !errors.Is(err, ErrLimitExceeded) || materialized || addressed {
		t.Fatalf("undersized export preflight = materialized %v, addressed %v, error %v", materialized, addressed, err)
	}
}

func TestArtifactAndSourceOutputLimitsRefuseBeforeSemanticBuild(t *testing.T) {
	spec := testCatalog(simpleMessage("message", "Message"))
	spec.Profile = "unsupported-profile"
	artifactLimit := ArtifactLimits{MaxBytes: 1, MaxDepth: maximumArtifactDepth, MaxMembers: maximumArtifactMembers}
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "compile", run: func() error {
			_, err := (Compiler{Limits: artifactLimit}).CompileContext(context.Background(), spec)
			return err
		}},
		{name: "source", run: func() error {
			_, err := (SourceCodec{Limits: artifactLimit}).EncodeContext(context.Background(), spec)
			return err
		}},
		{name: "pseudo", run: func() error {
			_, err := PseudoContext(context.Background(), spec, PseudoSpec{Locale: "en-XA", Mode: PseudoAccent, MaxOutputBytes: 1})
			return err
		}},
		{name: "merge", run: func() error {
			_, err := (SourceMerger{SourceLimits: artifactLimit}).MergeContext(context.Background(), spec, spec, SourceMergePolicy{})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("fail-early output limit error = %v", err)
			}
		})
	}
}

func TestArtifactExactLimitRefusesBeforeSnapshotValidation(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("message", "Message")))
	raw, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultArtifactLimits()
	limits.MaxBytes = len(raw)
	validated := false
	exact, err := (Compiler{Limits: limits}).encodeWithSnapshotValidation(context.Background(), snapshot, func(context.Context, *Snapshot) error {
		validated = true
		return nil
	})
	if err != nil || !validated || string(exact) != string(raw) {
		t.Fatalf("exact artifact = %d bytes, validated %v, error %v", len(exact), validated, err)
	}
	limits.MaxBytes--
	validated = false
	snapshot.digest = strings.Repeat("0", 64)
	_, err = (Compiler{Limits: limits}).encodeWithSnapshotValidation(context.Background(), snapshot, func(context.Context, *Snapshot) error {
		validated = true
		return errors.New("validation called")
	})
	if !errors.Is(err, ErrLimitExceeded) || validated {
		t.Fatalf("undersized artifact = validated %v, error %v", validated, err)
	}
	if _, err := (Compiler{Limits: limits}).EncodeContext(context.Background(), snapshot); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("undersized corrupt artifact error = %v", err)
	}
}

func TestSourceEncodedSizeMatchesExactWireBoundary(t *testing.T) {
	spec := contextGenerationCatalog(8)
	raw, err := (SourceCodec{CatalogLimits: spec.Limits}).Encode(spec)
	if err != nil {
		t.Fatal(err)
	}
	codec := SourceCodec{Limits: ArtifactLimits{MaxBytes: len(raw)}, CatalogLimits: spec.Limits}
	size, err := codec.EncodedSizeContext(context.Background(), spec)
	if err != nil || size != len(raw) {
		t.Fatalf("encoded source size = %d, want %d, error %v", size, len(raw), err)
	}
	exact, err := codec.EncodeContext(context.Background(), spec)
	if err != nil || string(exact) != string(raw) {
		t.Fatalf("exact source = %d bytes, error %v", len(exact), err)
	}
	codec.Limits.MaxBytes--
	if _, err := codec.EncodedSizeContext(context.Background(), spec); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("undersized source size error = %v", err)
	}
}

func TestRequiredLocaleValidationPollsNestedWork(t *testing.T) {
	required := make([]string, 256)
	for index := range required {
		required[index] = fmt.Sprintf("x-%03d", index)
	}
	records := make(map[Key]*messageRecord, 256)
	for index := 0; index < 256; index++ {
		records[Key(fmt.Sprintf("app.m%03d", index))] = &messageRecord{templates: map[string]compiledTranslation{}}
	}
	snapshot := &Snapshot{records: records, required: required}
	ctx := newCancelOnPollContext(1024)
	if err := validateRequiredLocalesContext(ctx, snapshot, &problemSet{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("required-locale cancellation error = %v", err)
	}
}

func TestCompileCancellationEmitsOneTerminalObservation(t *testing.T) {
	spec := contextGenerationCatalog(256)
	spec.Required = append(spec.Required, strings.Repeat("x", 256))
	observations := make([]Observation, 0, 1)
	spec.Observer = func(_ context.Context, observation Observation) {
		observations = append(observations, observation)
	}
	ctx := newCancelOnPollContext(40)
	if _, err := (Compiler{}).CompileContext(ctx, spec); !errors.Is(err, context.Canceled) {
		t.Fatalf("compile cancellation error = %v", err)
	}
	if len(observations) != 1 || observations[0].Operation != OperationCompile || observations[0].Outcome != OutcomeCanceled || observations[0].Reason != ReasonContextCanceled || observations[0].Count != 1 {
		t.Fatalf("compile observations = %+v", observations)
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

func TestGeneratedGoWriterIsCanonicalAndLimitPrecedesFormatter(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("message", "Message")))
	var input []byte
	formatted, err := generateGoContext(context.Background(), snapshot, GoGeneratorSpec{Package: "messages"}, func(raw []byte) ([]byte, error) {
		input = append([]byte(nil), raw...)
		return format.Source(raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(input) != string(formatted) {
		t.Fatalf("generated input is not canonical:\ninput: %q\nformatted: %q", input, formatted)
	}
	calls := 0
	_, err = generateGoContext(context.Background(), snapshot, GoGeneratorSpec{Package: "messages", MaxOutputBytes: len(formatted) - 1}, func(raw []byte) ([]byte, error) {
		calls++
		return format.Source(raw)
	})
	if !errors.Is(err, ErrLimitExceeded) || calls != 0 {
		t.Fatalf("undersized Go output = formatter calls %d, error %v", calls, err)
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

func TestPseudoAddedTranslationsArePreflightedBeforeCatalogBuild(t *testing.T) {
	spec := testCatalog(simpleMessage("notice", "Notice"))
	spec.Supported = []string{"en", "en-XA"}
	spec.Profile = "unsupported-profile"
	limits, err := checkedLimits(spec.Limits)
	if err != nil {
		t.Fatal(err)
	}
	probeLimits := DefaultArtifactLimits()
	probeLimits.MaxBytes = limits.MaxCatalogBytes
	var floor artifactFloorCounter
	if err := checkSourceArtifactFloorIntoContext(context.Background(), spec, limits, probeLimits, &floor); err != nil {
		t.Fatal(err)
	}
	_, err = PseudoContext(context.Background(), spec, PseudoSpec{Locale: "en-XA", Mode: PseudoAccent, MaxOutputBytes: floor.written})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("added translation preflight error = %v", err)
	}
}

func TestPseudoOutputLimitIsExact(t *testing.T) {
	spec := testCatalog(simpleMessage("notice", "Notice"))
	spec.Supported = []string{"en", "en-XA"}
	result, err := Pseudo(spec, PseudoSpec{Locale: "en-XA", Mode: PseudoAccent})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeSource(result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PseudoContext(context.Background(), spec, PseudoSpec{Locale: "en-XA", Mode: PseudoAccent, MaxOutputBytes: len(raw)}); err != nil {
		t.Fatalf("exact pseudolocale output error = %v", err)
	}
	if _, err := PseudoContext(context.Background(), spec, PseudoSpec{Locale: "en-XA", Mode: PseudoAccent, MaxOutputBytes: len(raw) - 1}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("undersized pseudolocale output error = %v", err)
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
