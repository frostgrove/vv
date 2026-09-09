package i18n

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func artifactFixture(t testing.TB) (*Snapshot, []byte) {
	t.Helper()
	message := simpleMessage("welcome", "Hello {$name}", ArgumentSpec{Name: "name", Type: TypeText, Required: true})
	message.Override = OverrideAny
	message.Public = true
	message.Translations = []Translation{{
		Locale: "fr", Text: "Bonjour {$name}", Review: ReviewApproved, ContractRevision: message.Revision,
	}}
	spec := testCatalog(message)
	spec.Supported = []string{"en", "fr", "fr-CA"}
	spec.Required = []string{"en", "fr"}
	spec.Parents = []LocaleEdge{{Locale: "fr-CA", Parent: "fr"}}
	spec = reviewedCatalog(t, spec)
	snapshot, err := New(spec)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = snapshot.Overlay(TenantOverlay("tenant-release", reviewedOverride(t, snapshot, Override{
		Key: Qualify("app", "welcome"), Locale: "fr", Text: "Salut {$name}", ContractRevision: message.Revision, Review: ReviewApproved,
	})))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, raw
}

func TestArtifactRoundTripIsCanonicalAndPreservesRenderingSemantics(t *testing.T) {
	snapshot, raw := artifactFixture(t)
	second, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, second) || bytes.HasSuffix(raw, []byte("\n")) {
		t.Fatal("encoding is not deterministic canonical JSON")
	}
	loaded, err := Load(context.Background(), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Reference() != snapshot.Reference() || loaded.Profile() != snapshot.Profile() || !slices.Equal(loaded.Supported(), snapshot.Supported()) {
		t.Fatalf("round trip identity changed: loaded=%+v original=%+v", loaded.Reference(), snapshot.Reference())
	}
	message, err := loaded.Bind(Qualify("app", "welcome"), Text("name", "Zoë"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := loaded.For("fr-CA")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := view.Render(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Text != "Salut \u2068Zoë\u2069" || rendered.TemplateLocale != "fr" || rendered.Layer != LayerTenant {
		t.Fatalf("rendered = %+v", rendered)
	}
	reencoded, err := Encode(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reencoded, raw) {
		t.Fatal("load and re-encode changed canonical bytes")
	}
}

func TestCompileMatchesNewThenEncode(t *testing.T) {
	spec := reviewedCatalog(t, testCatalog(simpleMessage("plain", "Ready")))
	compiled, err := Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := New(spec)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(compiled, encoded) {
		t.Fatal("Compile and Encode disagree")
	}
}

func TestSnapshotExactMaterialLimitsRoundTripThroughItsArtifact(t *testing.T) {
	spec := testCatalog(simpleMessage("exact", "Exact"))
	probe := mustSnapshot(t, spec)
	spec.Limits.MaxCatalogBytes = snapshotCatalogBytes(probe)
	spec.Limits.MaxCatalogItems = snapshotCatalogItems(probe)
	snapshot := mustSnapshot(t, spec)
	raw, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(context.Background(), bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("loader rejected the snapshot's exact declared material limits: %v", err)
	}
	if loaded.Digest() != snapshot.Digest() {
		t.Fatalf("round-trip digest = %q, want %q", loaded.Digest(), snapshot.Digest())
	}
}

func TestCompileArtifactFloorAllowsShortInitialOverride(t *testing.T) {
	message := simpleMessage("replaceable", strings.Repeat("long source ", 4096))
	message.Override = OverrideApplication
	spec := reviewedCatalog(t, testCatalog(message))
	base := mustSnapshot(t, spec)
	spec.Overrides = []Override{reviewedOverride(t, base, Override{
		Key: "app.replaceable", Locale: "en", Text: "short", ContractRevision: message.Revision, Review: ReviewApproved,
	})}
	effective, err := New(spec)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Encode(effective)
	if err != nil {
		t.Fatal(err)
	}
	got, err := (Compiler{Limits: ArtifactLimits{MaxBytes: len(want)}}).Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("compile with a replacing initial override changed the effective artifact")
	}
}

func TestSnapshotMaterialAccountingMatchesArtifactPreflight(t *testing.T) {
	snapshot, _ := artifactFixture(t)
	materialBytes, materialItems := snapshotCatalogMaterial(snapshot)
	limits := snapshot.limits
	limits.MaxCatalogBytes = materialBytes
	limits.MaxCatalogItems = materialItems
	encoded := artifactFromSnapshot(snapshot).Snapshot
	if err := preflightArtifactSnapshot(context.Background(), encoded, limits); err != nil {
		t.Fatalf("exact snapshot material was rejected: %v", err)
	}
	tooFewBytes := limits
	tooFewBytes.MaxCatalogBytes--
	if err := preflightArtifactSnapshot(context.Background(), encoded, tooFewBytes); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("artifact byte accounting accepted one byte too few: %v", err)
	}
	tooFewItems := limits
	tooFewItems.MaxCatalogItems--
	if err := preflightArtifactSnapshot(context.Background(), encoded, tooFewItems); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("artifact item accounting accepted one item too few: %v", err)
	}
}

func TestArtifactPreflightRejectsDescriptorLimitsBeforeTemplateCompilation(t *testing.T) {
	_, raw := artifactFixture(t)
	var document artifactDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	limits := decodeArtifactLimits(document.Snapshot.Limits)
	document.Snapshot.Messages[0].Description = strings.Repeat("x", limits.MaxDescriptionBytes+1)
	document.Snapshot.Messages[0].Templates[0].Text = "{{"
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(context.Background(), bytes.NewReader(mutated))
	if err == nil || !strings.Contains(err.Error(), "description") || strings.Contains(err.Error(), "invalid_syntax") {
		t.Fatalf("artifact preflight error = %v", err)
	}
}

func TestArtifactLoaderRejectsNonCanonicalAndStructurallyHostileJSON(t *testing.T) {
	_, raw := artifactFixture(t)
	cases := map[string][]byte{
		"leading whitespace": append([]byte(" "), raw...),
		"trailing value":     append(slices.Clone(raw), []byte("{}")...),
		"unknown member":     bytes.Replace(raw, []byte(`{"artifact":`), []byte(`{"unknown":true,"artifact":`), 1),
		"duplicate member":   bytes.Replace(raw, []byte(`{"artifact":`), []byte(`{"artifact":"duplicate","artifact":`), 1),
		"invalid utf8":       append(slices.Clone(raw), 0xff),
		"non object":         []byte(`[]`),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(context.Background(), bytes.NewReader(input)); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if _, err := (Loader{Limits: ArtifactLimits{MaxDepth: 1}}).Load(context.Background(), bytes.NewReader(raw)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("depth error = %v", err)
	}
	if _, err := (Loader{Limits: ArtifactLimits{MaxMembers: 1}}).Load(context.Background(), bytes.NewReader(raw)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("member error = %v", err)
	}
	if _, err := (Loader{Limits: ArtifactLimits{MaxBytes: len(raw) - 1}}).Load(context.Background(), bytes.NewReader(raw)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("byte error = %v", err)
	}
}

func TestArtifactLoaderRejectsCompatibilityAndSemanticTampering(t *testing.T) {
	_, raw := artifactFixture(t)
	var document artifactDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*artifactDocument){
		"artifact version": func(document *artifactDocument) { document.Artifact = "frostgrove.i18n.catalog/v2" },
		"grammar profile":  func(document *artifactDocument) { document.Profile = "other/v1" },
		"engine":           func(document *artifactDocument) { document.Engine = "other/v1" },
		"locale data":      func(document *artifactDocument) { document.LocaleData = "other/v1" },
		"timezone model":   func(document *artifactDocument) { document.TimeZoneDataModel = "other/v1" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := document
			mutate(&candidate)
			encoded, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Load(context.Background(), bytes.NewReader(encoded)); !errors.Is(err, ErrIncompatibleArtifact) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	semantic := document
	semantic.Snapshot.Messages = slices.Clone(document.Snapshot.Messages)
	semantic.Snapshot.Messages[0].Templates = slices.Clone(document.Snapshot.Messages[0].Templates)
	semantic.Snapshot.Messages[0].Templates[0].Text += "!"
	semanticRaw, err := json.Marshal(semantic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), bytes.NewReader(semanticRaw)); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("semantic tamper = %v", err)
	}
	contract := document
	contract.Snapshot.Messages = slices.Clone(document.Snapshot.Messages)
	contract.Snapshot.Messages[0].ContractDigest = strings.Repeat("0", 64)
	contractRaw, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), bytes.NewReader(contractRaw)); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("contract tamper = %v", err)
	}
}

func TestArtifactLoaderLimitsCannotBeWidenedByArtifactMetadata(t *testing.T) {
	_, raw := artifactFixture(t)
	var document artifactDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document.Snapshot.Limits.MaxCatalogBytes = maximumArtifactBytes
	document.SemanticDigest = strings.Repeat("0", 64)
	widened, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	loader := Loader{Limits: ArtifactLimits{MaxBytes: len(widened) - 1}}
	if _, err := loader.Load(context.Background(), bytes.NewReader(widened)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("artifact widened loader byte cap: %v", err)
	}
	if _, err := (Loader{Limits: ArtifactLimits{MaxBytes: -1}}).Load(context.Background(), bytes.NewReader(raw)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("invalid loader limits = %v", err)
	}
}

func TestArtifactCatalogLimitsRequireMatchingLocalAuthority(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxOutputBytes++
	spec := reviewedCatalog(t, testCatalog(simpleMessage("plain", "Ready")))
	spec.Limits = limits
	if _, err := Compile(spec); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("default compiler accepted widened catalog metadata: %v", err)
	}
	raw, err := (Compiler{CatalogLimits: limits}).Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), bytes.NewReader(raw)); !errors.Is(err, ErrInvalidArtifact) || !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("default loader accepted widened catalog metadata: %v", err)
	}
	loaded, err := (Loader{CatalogLimits: limits}).Load(context.Background(), bytes.NewReader(raw))
	if err != nil || loaded == nil {
		t.Fatalf("authorized catalog limits failed: snapshot=%p err=%v", loaded, err)
	}
}

func TestArtifactStructuralLimitsUseEffectiveValuesAndLocalCeilings(t *testing.T) {
	defaults := DefaultLimits()
	defaultSnapshot := mustSnapshot(t, testCatalog(simpleMessage("plain", "Ready")))
	defaultRaw, err := Encode(defaultSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	var defaultDocument artifactDocument
	if err := json.Unmarshal(defaultRaw, &defaultDocument); err != nil {
		t.Fatal(err)
	}
	if got := decodeArtifactLimits(defaultDocument.Snapshot.Limits); got.MaxDeclarations != defaults.MaxDeclarations || got.MaxVariants != defaults.MaxVariants || got.MaxLocalDepth != defaults.MaxLocalDepth {
		t.Fatalf("encoded zero-value structural limits = %+v", got)
	}

	custom := defaults
	custom.MaxDeclarations = 7
	custom.MaxVariants = 11
	custom.MaxLocalDepth = 3
	customSpec := reviewedCatalog(t, testCatalog(simpleMessage("plain", "Ready")))
	customSpec.Limits = custom
	customRaw, err := Compile(customSpec)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(context.Background(), bytes.NewReader(customRaw))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.limits.MaxDeclarations != 7 || loaded.limits.MaxVariants != 11 || loaded.limits.MaxLocalDepth != 3 {
		t.Fatalf("round-trip structural limits = %+v", loaded.limits)
	}
	if reencoded, err := Encode(loaded); err != nil || !bytes.Equal(reencoded, customRaw) {
		t.Fatalf("custom structural limits were not canonical after round trip: equal=%v err=%v", bytes.Equal(reencoded, customRaw), err)
	}

	for name, widen := range map[string]func(*Limits){
		"declarations": func(limits *Limits) { limits.MaxDeclarations++ },
		"variants":     func(limits *Limits) { limits.MaxVariants++ },
		"local depth":  func(limits *Limits) { limits.MaxLocalDepth++ },
	} {
		t.Run(name+" ceiling", func(t *testing.T) {
			widened := defaults
			widen(&widened)
			widenedSpec := reviewedCatalog(t, testCatalog(simpleMessage("plain", "Ready")))
			widenedSpec.Limits = widened
			widenedRaw, err := (Compiler{CatalogLimits: widened}).Compile(widenedSpec)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Load(context.Background(), bytes.NewReader(widenedRaw)); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("default loader accepted widened structural limit: %v", err)
			}
			if _, err := (Loader{CatalogLimits: widened}).Load(context.Background(), bytes.NewReader(widenedRaw)); err != nil {
				t.Fatalf("authorized structural limit failed: %v", err)
			}
		})
	}

	for name, corrupt := range map[string]func(*artifactLimits){
		"declarations": func(limits *artifactLimits) { limits.MaxDeclarations = -1 },
		"variants":     func(limits *artifactLimits) { limits.MaxVariants = 65537 },
		"local depth":  func(limits *artifactLimits) { limits.MaxLocalDepth = 257 },
	} {
		t.Run(name+" invalid", func(t *testing.T) {
			document := defaultDocument
			corrupt(&document.Snapshot.Limits)
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Load(context.Background(), bytes.NewReader(raw)); !errors.Is(err, ErrInvalidArtifact) || !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("invalid structural artifact limit error = %v", err)
			}
		})
	}
}

func TestCompatibilityIdentityPinsLocaleDataAndNamesTheRuntimeTimeZoneModel(t *testing.T) {
	if LocaleDataVersion != "go-intl/v0.4.1:cldr/48.1.0+icu/78;x-text/v0.41.0" {
		t.Fatalf("LocaleDataVersion = %q", LocaleDataVersion)
	}
	if TimeZoneDataModel != "go/time.LoadLocation:runtime-selected" {
		t.Fatalf("TimeZoneDataModel = %q", TimeZoneDataModel)
	}
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("plain", "Ready")))
	document := artifactFromSnapshot(snapshot)
	if document.LocaleData != LocaleDataVersion || document.TimeZoneDataModel != TimeZoneDataModel {
		t.Fatalf("artifact compatibility identity = %q/%q", document.LocaleData, document.TimeZoneDataModel)
	}
}

func TestArtifactTranslatorDescriptionMatchesCatalogValidation(t *testing.T) {
	message := simpleMessage("context", "Ready")
	message.Description = "Translator context \u202eis not rendered"
	snapshot := mustSnapshot(t, testCatalog(message))
	raw, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), bytes.NewReader(raw)); err != nil {
		t.Fatalf("bounded non-rendered directional context was rejected: %v", err)
	}

	var document artifactDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document.Snapshot.Messages[0].Description = " \t\n"
	blank, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), bytes.NewReader(blank)); !errors.Is(err, ErrInvalidArtifact) || !strings.Contains(err.Error(), "description") {
		t.Fatalf("blank artifact description error = %v", err)
	}
}

func TestCanonicalArtifactEncoderMatchesStandardJSONWithinPreflightLimits(t *testing.T) {
	snapshot, raw := artifactFixture(t)
	document := artifactFromSnapshot(snapshot)
	want, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want) {
		t.Fatal("bounded canonical encoder differs from the standard JSON representation")
	}
	document.Snapshot.Revision = "<release>&\"\\\b\f\n\r\t\x00\x1f\u2028\u2029"
	want, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	got, err := marshalCanonicalArtifact(document, DefaultArtifactLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("special-string encoding differs:\n got: %s\nwant: %s", got, want)
	}
	for name, limits := range map[string]ArtifactLimits{
		"bytes":   {MaxBytes: len(raw) - 1},
		"depth":   {MaxDepth: 1},
		"members": {MaxMembers: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := (Compiler{Limits: limits}).Encode(snapshot); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("encoder limit = %v", err)
			}
		})
	}
}

func TestArtifactLoadingHonorsCancellationReaderErrorsAndNoProgress(t *testing.T) {
	_, raw := artifactFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Load(ctx, bytes.NewReader(raw)); !errors.Is(err, context.Canceled) || errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("canceled load = %v", err)
	}
	sentinel := errors.New("read failed")
	if _, err := Load(context.Background(), failingArtifactReader{err: sentinel}); !errors.Is(err, sentinel) || !errors.Is(err, ErrArtifactIO) || errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("reader failure = %v", err)
	}
	if _, err := Load(context.Background(), noProgressArtifactReader{}); !errors.Is(err, io.ErrNoProgress) || !errors.Is(err, ErrArtifactIO) {
		t.Fatalf("no-progress reader = %v", err)
	}
	var reader *bytes.Reader
	if _, err := Load(context.Background(), reader); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("typed nil reader = %v", err)
	}
}

type failingArtifactReader struct {
	err error
}

func (reader failingArtifactReader) Read([]byte) (int, error) {
	return 0, reader.err
}

type noProgressArtifactReader struct{}

func (noProgressArtifactReader) Read([]byte) (int, error) {
	return 0, nil
}

func TestArtifactLoadFSClosesFilesAndPreservesFailures(t *testing.T) {
	_, raw := artifactFixture(t)
	loaded, err := LoadFS(context.Background(), fstest.MapFS{"catalog.json": {Data: raw}}, "catalog.json")
	if err != nil || loaded == nil {
		t.Fatalf("map FS load = %v", err)
	}
	if _, err := LoadFS(context.Background(), fstest.MapFS{}, "missing.json"); !errors.Is(err, ErrArtifactIO) || errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("missing file = %v", err)
	}
	closeFailure := errors.New("close failed")
	filesystem := &controlledArtifactFS{raw: raw, closeErr: closeFailure}
	if _, err := LoadFS(context.Background(), filesystem, "catalog.json"); !errors.Is(err, closeFailure) || !errors.Is(err, ErrArtifactIO) || !filesystem.closed {
		t.Fatalf("close failure = %v, closed=%v", err, filesystem.closed)
	}
	readFailure := errors.New("read failed")
	filesystem = &controlledArtifactFS{readErr: readFailure, closeErr: closeFailure}
	if _, err := LoadFS(context.Background(), filesystem, "catalog.json"); !errors.Is(err, readFailure) || !errors.Is(err, closeFailure) || !errors.Is(err, ErrArtifactIO) || !filesystem.closed {
		t.Fatalf("joined read/close failure = %v, closed=%v", err, filesystem.closed)
	}
	if _, err := LoadFS(context.Background(), filesystem, "../catalog.json"); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("invalid path = %v", err)
	}
}

type controlledArtifactFS struct {
	raw      []byte
	readErr  error
	closeErr error
	closed   bool
	opens    int
}

func (filesystem *controlledArtifactFS) Open(string) (fs.File, error) {
	filesystem.opens++
	return &controlledArtifactFile{filesystem: filesystem, reader: bytes.NewReader(filesystem.raw)}, nil
}

type controlledArtifactFile struct {
	filesystem *controlledArtifactFS
	reader     *bytes.Reader
}

func (file *controlledArtifactFile) Read(buffer []byte) (int, error) {
	if file.filesystem.readErr != nil {
		return 0, file.filesystem.readErr
	}
	return file.reader.Read(buffer)
}

func (file *controlledArtifactFile) Close() error {
	file.filesystem.closed = true
	return file.filesystem.closeErr
}

func (file *controlledArtifactFile) Stat() (fs.FileInfo, error) {
	return artifactFileInfo{}, nil
}

type artifactFileInfo struct{}

func (artifactFileInfo) Name() string       { return "catalog.json" }
func (artifactFileInfo) Size() int64        { return 0 }
func (artifactFileInfo) Mode() fs.FileMode  { return 0 }
func (artifactFileInfo) ModTime() time.Time { return time.Time{} }
func (artifactFileInfo) IsDir() bool        { return false }
func (artifactFileInfo) Sys() any           { return nil }

func TestLoadedArtifactUsesLoaderObserverWithoutChangingIdentity(t *testing.T) {
	snapshot, raw := artifactFixture(t)
	observed := make(chan Observation, 2)
	loaded, err := (Loader{Observer: func(_ context.Context, observation Observation) {
		observed <- observation
	}}).Load(context.Background(), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Reference() != snapshot.Reference() {
		t.Fatal("observer changed semantic identity")
	}
	select {
	case observation := <-observed:
		if observation.Operation != OperationLoad || observation.Outcome != OutcomeSuccess || observation.Reason != ReasonNone {
			t.Fatalf("load observation = %+v", observation)
		}
	case <-time.After(time.Second):
		t.Fatal("artifact load was not observed")
	}
	resolution := loaded.Resolve(Exact(SourceExplicit, "en"))
	if !resolution.Matched() {
		t.Fatalf("resolution = %+v", resolution)
	}
	select {
	case observation := <-observed:
		if observation.Operation != OperationResolve || observation.Outcome != OutcomeSuccess {
			t.Fatalf("observation = %+v", observation)
		}
	case <-time.After(time.Second):
		t.Fatal("loaded snapshot did not use the loader observer")
	}
}

func TestArtifactLifecycleObservationsCoverCompileAndLoadFailures(t *testing.T) {
	observations := make([]Observation, 0, 4)
	observer := func(_ context.Context, observation Observation) {
		observations = append(observations, observation)
	}
	spec := reviewedCatalog(t, testCatalog(simpleMessage("plain", "Ready")))
	if _, err := (Compiler{Observer: observer}).Compile(spec); err != nil {
		t.Fatal(err)
	}
	if _, err := (Compiler{Observer: observer}).Compile(CatalogSpec{}); err == nil {
		t.Fatal("invalid catalog compiled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Loader{Observer: observer}).Load(ctx, strings.NewReader("{}")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled load = %v", err)
	}
	if _, err := (Loader{Observer: observer}).Load(context.Background(), strings.NewReader("{")); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("invalid load = %v", err)
	}
	want := []struct {
		operation Operation
		outcome   Outcome
		reason    Reason
	}{
		{OperationCompile, OutcomeSuccess, ReasonNone},
		{OperationCompile, OutcomeInvalid, ReasonSchemaMismatch},
		{OperationLoad, OutcomeCanceled, ReasonContextCanceled},
		{OperationLoad, OutcomeInvalid, ReasonInvalidArtifact},
	}
	if len(observations) != len(want) {
		t.Fatalf("observations = %+v", observations)
	}
	for index, expected := range want {
		got := observations[index]
		if got.Operation != expected.operation || got.Outcome != expected.outcome || got.Reason != expected.reason || got.Count != 1 || got.Duration < 0 {
			t.Fatalf("observation %d = %+v, want %+v", index, got, expected)
		}
	}
	if _, err := (Compiler{Observer: func(context.Context, Observation) { panic("observer") }}).Compile(spec); err != nil {
		t.Fatalf("observer panic changed compile: %v", err)
	}
}

func FuzzArtifactLoaderNeverPanics(f *testing.F) {
	_, raw := artifactFixture(f)
	f.Add(raw)
	f.Add([]byte(`{"artifact":"frostgrove.i18n.catalog/v1"}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		loader := Loader{Limits: ArtifactLimits{MaxBytes: 1 << 20, MaxDepth: 64, MaxMembers: 1 << 16}}
		_, _ = loader.Load(context.Background(), bytes.NewReader(input))
	})
}
