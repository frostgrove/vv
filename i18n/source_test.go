package i18n

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func sourceCodecFixture(t testing.TB) CatalogSpec {
	t.Helper()
	alpha := simpleMessage("alpha", "Hello {$name}",
		ArgumentSpec{Name: "name", Type: TypeText, Required: true},
		ArgumentSpec{Name: "tone", Type: TypeEnum, Values: []string{"formal", "casual"}},
	)
	alpha.Override = OverrideApplication
	alpha.Public = true
	digest, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "a", alpha)
	if err != nil {
		t.Fatal(err)
	}
	alpha.Translations = []Translation{
		{Locale: "ru", Text: "Привет, {$name}", Review: ReviewRequired, ContractRevision: alpha.Revision, SourceDigest: digest},
		{Locale: "fr-ca", Text: "Bonjour {$name}", Review: ReviewApproved, ContractRevision: "old", SourceDigest: strings.Repeat("0", 64)},
	}
	beta := simpleMessage("beta", "Beta")
	return CatalogSpec{
		Revision: "source-1", SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"fr-ca", "en", "ru"}, Required: []string{"ru"},
		Parents: []LocaleEdge{{Locale: "fr-ca", Parent: "fr"}},
		Modules: []Module{
			{Name: "z", Messages: []MessageSpec{beta}},
			{Name: "a", Messages: []MessageSpec{alpha}},
		},
		Overrides: []Override{{
			Key: "a.alpha", Locale: "ru", Text: "Привет {$name}", Review: ReviewRequired,
			ContractRevision: alpha.Revision, SourceDigest: digest,
		}},
	}
}

func TestSourceCodecCanonicalRoundTripPreservesSemanticOrderAndWorkingStates(t *testing.T) {
	spec := sourceCodecFixture(t)
	raw, err := EncodeSource(spec)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasSuffix(raw, []byte("\n")) || !bytes.Contains(raw, []byte(`"source":"`+SourceVersion+`"`)) {
		t.Fatalf("unexpected canonical source %q", raw)
	}
	decoded, err := DecodeSource(context.Background(), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{decoded.Modules[0].Name, decoded.Modules[1].Name}; !slices.Equal(got, []string{"a", "z"}) {
		t.Fatalf("module order = %v", got)
	}
	if !slices.Equal(decoded.Supported, []string{"fr-CA", "en", "ru"}) {
		t.Fatalf("supported order or canonicalization changed: %v", decoded.Supported)
	}
	message := decoded.Modules[0].Messages[0]
	if message.Key != "a.alpha" || message.Arguments[0].Name != "name" || message.Arguments[1].Name != "tone" {
		t.Fatalf("derived key or argument order changed: %+v", message)
	}
	if message.Translations[0].Locale != "fr-CA" || message.Translations[1].Locale != "ru" {
		t.Fatalf("translation order is not canonical: %+v", message.Translations)
	}
	if message.Translations[1].Review != ReviewRequired || decoded.Overrides[0].Review != ReviewRequired {
		t.Fatal("working review state was not preserved")
	}
	if _, err := New(decoded); err == nil {
		t.Fatal("working source unexpectedly became a publishable snapshot")
	}
	second, err := EncodeSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, second) {
		t.Fatal("decode and encode changed canonical bytes")
	}
	decoded.Modules[0].Messages[0].Arguments[0].Name = "mutated"
	again, err := DecodeSource(context.Background(), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if again.Modules[0].Messages[0].Arguments[0].Name != "name" {
		t.Fatal("decoded slices share caller mutation")
	}
}

func TestSourceCodecStrictlyRejectsMalformedAndUnknownJSON(t *testing.T) {
	raw, err := EncodeSource(sourceCodecFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	sourceMember := `"source":"` + SourceVersion + `"`
	tests := map[string][]byte{
		"duplicate root member":   []byte(strings.Replace(string(raw), sourceMember, sourceMember+","+sourceMember, 1)),
		"duplicate nested member": []byte(strings.Replace(string(raw), `"description":"Test message alpha"`, `"description":"Test message alpha","description":"Test message alpha"`, 1)),
		"unknown member":          []byte(strings.Replace(string(raw), "{", `{"unknown":true,`, 1)),
		"case folded member":      []byte(strings.Replace(string(raw), `"source":`, `"SOURCE":`, 1)),
		"trailing value":          append(slices.Clone(raw), []byte(`{}`)...),
		"wrong version":           []byte(strings.Replace(string(raw), SourceVersion, "frostgrove.i18n.source/v2", 1)),
		"unknown output":          []byte(strings.Replace(string(raw), `"output":"plain"`, `"output":"html"`, 1)),
		"unknown argument type":   []byte(strings.Replace(string(raw), `"type":"text"`, `"type":"object"`, 1)),
		"unknown review":          []byte(strings.Replace(string(raw), `"review":"approved"`, `"review":"maybe"`, 1)),
		"wrong override layer":    []byte(strings.Replace(string(raw), `"layer":"application"`, `"layer":"tenant"`, 1)),
		"invalid utf8":            []byte("{\"source\":\"frostgrove.i18n.source/v1\",\"revision\":\"\xff\"}"),
		"lone high surrogate":     []byte(`{"source":"frostgrove.i18n.source/v1","revision":"\ud800"}`),
		"lone low surrogate":      []byte(`{"source":"frostgrove.i18n.source/v1","revision":"\udc00"}`),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeSource(context.Background(), bytes.NewReader(input))
			if !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSourceCodecLimitsCancellationAndIOTaxonomy(t *testing.T) {
	raw, err := EncodeSource(sourceCodecFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		limits ArtifactLimits
		want   error
	}{
		{name: "bytes", limits: ArtifactLimits{MaxBytes: len(raw) - 1}, want: ErrLimitExceeded},
		{name: "depth", limits: ArtifactLimits{MaxDepth: 2}, want: ErrLimitExceeded},
		{name: "members", limits: ArtifactLimits{MaxMembers: 2}, want: ErrLimitExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (SourceCodec{Limits: test.limits}).Decode(context.Background(), bytes.NewReader(raw))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := &countingSourceReader{raw: raw}
	if _, err := DecodeSource(ctx, reader); !errors.Is(err, context.Canceled) || reader.reads != 0 {
		t.Fatalf("canceled decode = (%v, %d reads)", err, reader.reads)
	}
	operational := errors.New("disk disappeared")
	if _, err := DecodeSource(context.Background(), sourceErrorReader{err: operational}); !errors.Is(err, ErrSourceIO) || !errors.Is(err, operational) || errors.Is(err, ErrInvalidSource) {
		t.Fatalf("I/O error taxonomy = %v", err)
	}
	var nilReader *bytes.Reader
	if _, err := DecodeSource(context.Background(), nilReader); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("typed nil error = %v", err)
	}
	if _, err := (SourceCodec{Limits: ArtifactLimits{MaxBytes: 1}}).Encode(sourceCodecFixture(t)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("encode byte limit error = %v", err)
	}
}

func TestSourceCodecEnforcesDeclaredCatalogCeilingsBeforeTraversal(t *testing.T) {
	spec := sourceCodecFixture(t)
	limits := DefaultLimits()
	limits.MaxMessages = 2
	spec.Limits = limits
	ceiling := limits
	ceiling.MaxMessages = 1
	if _, err := (SourceCodec{CatalogLimits: ceiling}).Encode(spec); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ceiling error = %v", err)
	}
	raw, err := EncodeSource(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (SourceCodec{CatalogLimits: ceiling}).Decode(context.Background(), bytes.NewReader(raw)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("decode ceiling error = %v", err)
	}
	spec.Limits.MaxMessages = 1
	if _, err := (SourceCodec{CatalogLimits: spec.Limits}).Encode(spec); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("cardinality error = %v", err)
	}
}

func TestSourceCodecRejectsAggregateStringMaterialAtTheLocalCeiling(t *testing.T) {
	spec := sourceCodecFixture(t)
	minimum := minimumSourceCatalogBytes(t, spec)
	spec.Limits.MaxCatalogBytes = minimum
	raw, err := EncodeSource(spec)
	if err != nil {
		t.Fatal(err)
	}
	declared := []byte(fmt.Sprintf(`"max_catalog_bytes":%d`, minimum))
	below := []byte(fmt.Sprintf(`"max_catalog_bytes":%d`, minimum-1))
	raw = bytes.Replace(raw, declared, below, 1)
	if bytes.Contains(raw, declared) {
		t.Fatal("test did not lower the declared catalog byte limit")
	}
	ceiling := Limits{MaxCatalogBytes: minimum - 1}
	if _, err := (SourceCodec{CatalogLimits: ceiling}).Decode(context.Background(), bytes.NewReader(raw)); !errors.Is(err, ErrLimitExceeded) || !strings.Contains(err.Error(), "source material") {
		t.Fatalf("aggregate ceiling error = %v", err)
	}
}

func TestSourceCodecCanonicalRoundTripAtExactSemanticByteLimit(t *testing.T) {
	implicit := sourceCodecFixture(t)
	explicit := cloneCatalogSpec(implicit)
	explicit.Profile = GrammarProfile
	explicit.DefaultLocale = explicit.SourceLocale
	explicit.DefaultTimeZone = "UTC"
	for moduleIndex := range explicit.Modules {
		for messageIndex := range explicit.Modules[moduleIndex].Messages {
			message := &explicit.Modules[moduleIndex].Messages[messageIndex]
			message.Key = Qualify(explicit.Modules[moduleIndex].Name, message.ID)
		}
	}
	implicitLimit := minimumSourceCatalogBytes(t, implicit)
	explicitLimit := minimumSourceCatalogBytes(t, explicit)
	if implicitLimit != explicitLimit {
		t.Fatalf("implicit defaults need %d semantic bytes, explicit defaults need %d", implicitLimit, explicitLimit)
	}
	implicit.Limits.MaxCatalogBytes = implicitLimit
	codec := SourceCodec{CatalogLimits: Limits{MaxCatalogBytes: implicitLimit}}
	raw, err := codec.Encode(implicit)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(context.Background(), bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("canonical source failed its own exact semantic limit: %v", err)
	}
	second, err := codec.Encode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, second) {
		t.Fatal("exact-limit round trip changed canonical bytes")
	}
	implicit.Limits.MaxCatalogBytes--
	if _, err := (SourceCodec{}).Encode(implicit); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("below-boundary encode error = %v", err)
	}
}

func TestSourceCodecRejectsOversizedSemanticStringWhileStreaming(t *testing.T) {
	raw, err := EncodeSource(sourceCodecFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte(`"source":"Hello {$name}"`)
	huge := []byte(`"source":"` + strings.Repeat("x", 2<<20) + `"`)
	raw = bytes.Replace(raw, marker, huge, 1)
	if bytes.Equal(raw, huge) || !bytes.Contains(raw, huge) {
		t.Fatal("test did not replace the message source")
	}
	reader := &boundedSourceReader{raw: raw}
	_, err = (SourceCodec{CatalogLimits: Limits{MaxCatalogBytes: 512}}).Decode(context.Background(), reader)
	if !errors.Is(err, ErrLimitExceeded) || !strings.Contains(err.Error(), "source string") {
		t.Fatalf("streaming catalog limit error = %v", err)
	}
	if reader.bytesRead >= len(raw)/100 {
		t.Fatalf("decoder read %d of %d bytes before rejecting local semantic limit", reader.bytesRead, len(raw))
	}
	if reader.maxRequest > sourceStreamChunkBytes {
		t.Fatalf("decoder requested an unbounded %d-byte read", reader.maxRequest)
	}
}

func TestSourceCodecStreamingPreflightStopsOnCancellation(t *testing.T) {
	raw := []byte(`{"source":"` + SourceVersion + `","revision":"` + strings.Repeat("x", 2<<20) + `"}`)
	ctx, cancel := context.WithCancel(context.Background())
	reader := &boundedSourceReader{raw: raw, cancel: cancel, cancelAfter: 1}
	_, err := DecodeSource(ctx, reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("streaming cancellation error = %v", err)
	}
	if reader.reads != 1 || reader.bytesRead > sourceStreamChunkBytes || reader.maxRequest > sourceStreamChunkBytes {
		t.Fatalf("canceled reader activity = reads %d, bytes %d, request %d", reader.reads, reader.bytesRead, reader.maxRequest)
	}
}

func TestSourceCodecStreamingPreflightKeepsStrictJSONChecks(t *testing.T) {
	tests := map[string]string{
		"duplicate": `{"source":"` + SourceVersion + `","source":"` + SourceVersion + `"}`,
		"unknown":   `{"source":"` + SourceVersion + `","unknown":"value"}`,
		"surrogate": `{"source":"` + SourceVersion + `","revision":"\ud800"}`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			reader := &boundedSourceReader{raw: []byte(input), chunk: 1}
			if _, err := DecodeSource(context.Background(), reader); !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("streamed strict JSON error = %v", err)
			}
		})
	}
}

func TestSourceCodecTinyArtifactLimitAvoidsCatalogClone(t *testing.T) {
	spec := CatalogSpec{
		Revision: "r1", SourceLocale: "en", DefaultLocale: "en", Supported: []string{"en"},
		Modules: []Module{{Name: "large", Messages: make([]MessageSpec, 1024)}},
	}
	for index := range spec.Modules[0].Messages {
		spec.Modules[0].Messages[index] = MessageSpec{
			ID: fmt.Sprintf("message%d", index), Revision: "r1", Source: "value", Output: OutputPlain,
			Arguments: []ArgumentSpec{{Name: "value", Type: TypeText}},
		}
	}
	codec := SourceCodec{Limits: ArtifactLimits{MaxBytes: 1}}
	var encodeErr error
	allocations := testing.AllocsPerRun(5, func() {
		_, encodeErr = codec.Encode(spec)
	})
	if !errors.Is(encodeErr, ErrLimitExceeded) {
		t.Fatalf("tiny artifact limit error = %v", encodeErr)
	}
	if allocations > 64 {
		t.Fatalf("tiny artifact limit allocated %.0f objects before rejection", allocations)
	}
	raw, err := EncodeSource(sourceCodecFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	exact, err := (SourceCodec{Limits: ArtifactLimits{MaxBytes: len(raw)}}).Encode(sourceCodecFixture(t))
	if err != nil || !bytes.Equal(exact, raw) {
		t.Fatalf("exact artifact bound changed canonical encoding: %v", err)
	}
}

func TestSourceCodecAcceptsPairedSurrogatesAndLiteralEscapeText(t *testing.T) {
	spec := sourceCodecFixture(t)
	spec.Revision = "literal \\ud800"
	raw, err := EncodeSource(spec)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSource(context.Background(), bytes.NewReader(raw))
	if err != nil || decoded.Revision != spec.Revision {
		t.Fatalf("literal escape round trip = %q, %v", decoded.Revision, err)
	}
	paired := bytes.Replace(raw, []byte(`"revision":"literal \\ud800"`), []byte(`"revision":"\ud83d\ude00"`), 1)
	decoded, err = DecodeSource(context.Background(), bytes.NewReader(paired))
	if err != nil || decoded.Revision != "😀" {
		t.Fatalf("paired surrogate decode = %q, %v", decoded.Revision, err)
	}
}

func TestSourceConversionAndCanonicalizationRemainCancellationAware(t *testing.T) {
	document := sourceDocument{Modules: make([]sourceModule, 50)}
	for index := range document.Modules {
		document.Modules[index].Name = fmt.Sprintf("module%d", index)
	}
	if _, err := document.catalogSpec(&countdownContext{remaining: 10}); !errors.Is(err, context.Canceled) {
		t.Fatalf("catalog conversion cancellation = %v", err)
	}
	spec := CatalogSpec{
		Revision: "r1", SourceLocale: "en", DefaultLocale: "en", Supported: []string{"en"},
		Modules: make([]Module, 50),
	}
	for index := range spec.Modules {
		spec.Modules[index].Name = fmt.Sprintf("module%d", index)
	}
	if _, err := canonicalSourceSpec(&countdownContext{remaining: 10}, spec, DefaultLimits()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canonicalization cancellation = %v", err)
	}
}

func TestSourceCodecMutationChangesCanonicalIdentity(t *testing.T) {
	spec := sourceCodecFixture(t)
	first, err := EncodeSource(spec)
	if err != nil {
		t.Fatal(err)
	}
	mutated := cloneCatalogSpec(spec)
	mutated.Modules[1].Messages[0].Description += " changed"
	second, err := EncodeSource(mutated)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("translator context mutation did not change canonical source")
	}
}

func TestSourceWireSchemaContainsNoMapsPointersOrInterfaces(t *testing.T) {
	assertSourceWireType(t, reflect.TypeFor[sourceDocument](), make(map[reflect.Type]bool))
}

func FuzzSourceCodecNeverPanics(f *testing.F) {
	raw, err := EncodeSource(sourceCodecFixture(f))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	f.Add([]byte(`{"source":"frostgrove.i18n.source/v1"}`))
	f.Add([]byte(`{"x":[[[[[null]]]]]}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}
		_, _ = (SourceCodec{Limits: ArtifactLimits{MaxBytes: 1 << 20, MaxDepth: 64, MaxMembers: 1 << 16}}).Decode(context.Background(), bytes.NewReader(input))
	})
}

func assertSourceWireType(t testing.TB, value reflect.Type, visited map[reflect.Type]bool) {
	t.Helper()
	if visited[value] {
		return
	}
	visited[value] = true
	switch value.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Interface:
		t.Fatalf("wire type %v contains %v", value, value.Kind())
	case reflect.Array, reflect.Slice:
		assertSourceWireType(t, value.Elem(), visited)
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			assertSourceWireType(t, value.Field(index).Type, visited)
		}
	}
}

type sourceErrorReader struct{ err error }

func (r sourceErrorReader) Read([]byte) (int, error) { return 0, r.err }

type countingSourceReader struct {
	raw   []byte
	reads int
}

func (r *countingSourceReader) Read(target []byte) (int, error) {
	r.reads++
	if len(r.raw) == 0 {
		return 0, io.EOF
	}
	n := copy(target, r.raw)
	r.raw = r.raw[n:]
	return n, nil
}

type countdownContext struct {
	remaining int
}

type boundedSourceReader struct {
	raw         []byte
	chunk       int
	reads       int
	bytesRead   int
	maxRequest  int
	cancel      context.CancelFunc
	cancelAfter int
}

func (r *boundedSourceReader) Read(target []byte) (int, error) {
	r.reads++
	r.maxRequest = max(r.maxRequest, len(target))
	if len(r.raw) == 0 {
		return 0, io.EOF
	}
	if r.chunk > 0 && len(target) > r.chunk {
		target = target[:r.chunk]
	}
	n := copy(target, r.raw)
	r.raw = r.raw[n:]
	r.bytesRead += n
	if r.cancel != nil && r.reads == r.cancelAfter {
		r.cancel()
	}
	return n, nil
}

func minimumSourceCatalogBytes(t testing.TB, spec CatalogSpec) int {
	t.Helper()
	lower, upper := 1, DefaultLimits().MaxCatalogBytes
	for lower < upper {
		middle := lower + (upper-lower)/2
		probe := cloneCatalogSpec(spec)
		probe.Limits.MaxCatalogBytes = middle
		_, err := EncodeSource(probe)
		if errors.Is(err, ErrLimitExceeded) {
			lower = middle + 1
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		upper = middle
	}
	return lower
}

func (c *countdownContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *countdownContext) Done() <-chan struct{}       { return nil }
func (c *countdownContext) Value(any) any               { return nil }
func (c *countdownContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
