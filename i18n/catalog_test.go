package i18n

import (
	"errors"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func testCatalog(messages ...MessageSpec) CatalogSpec {
	for i := range messages {
		if strings.TrimSpace(messages[i].Description) == "" {
			messages[i].Description = "Test message context"
		}
	}
	return CatalogSpec{
		Revision:        "catalog-1",
		SourceLocale:    "en",
		DefaultLocale:   "en",
		DefaultTimeZone: "UTC",
		Supported:       []string{"en", "ru", "ar", "pl", "fr-CA"},
		Modules:         []Module{{Name: "app", Messages: messages}},
	}
}

func simpleMessage(id, source string, arguments ...ArgumentSpec) MessageSpec {
	return MessageSpec{ID: id, Revision: "message-1", Source: source, Description: "Test message " + id, Arguments: arguments, Output: OutputPlain}
}

func mustSnapshot(t *testing.T, spec CatalogSpec) *Snapshot {
	t.Helper()
	spec = reviewedCatalog(t, spec)
	snapshot, err := New(spec)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func reviewedCatalog(t testing.TB, spec CatalogSpec) CatalogSpec {
	t.Helper()
	spec.Modules = slices.Clone(spec.Modules)
	digests := make(map[Key]string)
	for moduleIndex := range spec.Modules {
		module := &spec.Modules[moduleIndex]
		module.Messages = slices.Clone(module.Messages)
		for messageIndex := range module.Messages {
			message := &module.Messages[messageIndex]
			if strings.TrimSpace(message.Description) == "" {
				message.Description = "Test message context"
			}
			message.Translations = slices.Clone(message.Translations)
			digest, err := ExpectedSourceDigestForLocale(spec.Profile, spec.SourceLocale, module.Name, *message)
			if err != nil {
				t.Fatal(err)
			}
			key := message.Key
			if key == "" {
				key = Qualify(module.Name, message.ID)
			}
			digests[key] = digest
			for translationIndex := range message.Translations {
				translation := &message.Translations[translationIndex]
				if translation.Review == ReviewApproved {
					translation.SourceDigest = digest
					translation.ReviewDigest, err = ExpectedReviewDigest(digest, translation.Locale, translation.Text)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	spec.Overrides = slices.Clone(spec.Overrides)
	for i := range spec.Overrides {
		if spec.Overrides[i].Review == ReviewApproved {
			digest := digests[spec.Overrides[i].Key]
			spec.Overrides[i].SourceDigest = digest
			var err error
			spec.Overrides[i].ReviewDigest, err = ExpectedReviewDigest(digest, spec.Overrides[i].Locale, spec.Overrides[i].Text)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return spec
}

func reviewedOverride(t testing.TB, snapshot *Snapshot, override Override) Override {
	t.Helper()
	digest, ok := snapshot.SourceDigest(override.Key)
	if !ok {
		t.Fatalf("source digest for %q is missing", override.Key)
	}
	override.SourceDigest = digest
	var err error
	override.ReviewDigest, err = ExpectedReviewDigest(digest, override.Locale, override.Text)
	if err != nil {
		t.Fatal(err)
	}
	return override
}

func TestCatalogBuildsImmutableDescriptorsAndDeterministicIdentity(t *testing.T) {
	message := simpleMessage("hello", "Hello {$name}", ArgumentSpec{Name: "name", Type: TypeText, Required: true})
	spec := testCatalog(message)
	snapshot := mustSnapshot(t, spec)
	second := mustSnapshot(t, spec)
	if snapshot.Digest() == "" || snapshot.Digest() != second.Digest() || snapshot.Revision() != "catalog-1" {
		t.Fatalf("identity is not deterministic: %q %q", snapshot.Digest(), second.Digest())
	}
	descriptor, ok := snapshot.Descriptor(Qualify("app", "hello"))
	if !ok {
		t.Fatal("descriptor is missing")
	}
	descriptor.Arguments[0].Name = "changed"
	if got, _ := snapshot.Descriptor(Qualify("app", "hello")); got.Arguments[0].Name != "name" {
		t.Fatal("descriptor mutation reached snapshot")
	}
	keys := snapshot.Keys()
	keys[0] = "changed"
	if snapshot.Keys()[0] != Qualify("app", "hello") {
		t.Fatal("keys mutation reached snapshot")
	}

	bestFit := spec
	bestFit.MatchMode = MatchBestFit
	if snapshot.Digest() == mustSnapshot(t, bestFit).Digest() {
		t.Fatal("match mode is absent from digest")
	}
	defaultOnMiss := spec
	defaultOnMiss.DefaultOnMiss = true
	if snapshot.Digest() == mustSnapshot(t, defaultOnMiss).Digest() {
		t.Fatal("default-on-miss is absent from digest")
	}
	bounded := spec
	bounded.Limits.MaxOutputBytes = 100
	if snapshot.Digest() == mustSnapshot(t, bounded).Digest() {
		t.Fatal("render limits are absent from digest")
	}
}

func TestCatalogAggregatesSortedConstructionProblems(t *testing.T) {
	spec := testCatalog(MessageSpec{
		ID:           "bad",
		Revision:     "r",
		Source:       "{$unknown :mystery}",
		Output:       OutputPlain,
		Arguments:    []ArgumentSpec{{Name: "declared", Type: TypeEnum, Values: []string{"x", "x"}}},
		Translations: []Translation{{Locale: "RU", Text: "ok"}},
	})
	_, first := New(spec)
	_, second := New(spec)
	if first == nil || second == nil || first.Error() != second.Error() {
		t.Fatalf("construction errors are not deterministic: %v / %v", first, second)
	}
	var problems *Problems
	if !errors.As(first, &problems) {
		t.Fatalf("error is %T, want *Problems", first)
	}
	items := problems.Items()
	if len(items) < 4 {
		t.Fatalf("only %d problems were aggregated: %v", len(items), items)
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].Path > items[i].Path {
			t.Fatalf("problems are not sorted: %v", items)
		}
	}
	items[0].Path = "mutated"
	if problems.Items()[0].Path == "mutated" {
		t.Fatal("problem list escaped by reference")
	}
}

func TestCatalogRejectsMessageEngineParserPanicAsSyntaxProblem(t *testing.T) {
	_, err := New(testCatalog(simpleMessage("malformed", "{@ ")))
	if !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("New error = %v, want ErrInvalidCatalog", err)
	}
	var problems *Problems
	if !errors.As(err, &problems) {
		t.Fatalf("New error = %T, want *Problems", err)
	}
	items := problems.Items()
	if len(items) != 1 || items[0].Code != ProblemInvalidSyntax || items[0].Path != "modules[0].messages[0].source" {
		t.Fatalf("New problems = %#v", items)
	}
}

func TestCatalogRejectsInvalidOffsetConfigurations(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "missing operation", source: "{$n :offset}"},
		{name: "both operations", source: "{$n :offset add=1 subtract=1}"},
		{name: "negative", source: "{$n :offset add=-1}"},
		{name: "leading zero", source: "{$n :offset add=01}"},
		{name: "above digit size", source: "{$n :offset add=100}"},
		{name: "non numeric", source: "{$n :offset add=one}"},
		{name: "unknown option", source: "{$n :offset add=1 unknown=1}"},
		{name: "dynamic option", source: "{$n :offset add=$delta}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(testCatalog(simpleMessage("offset", test.source,
				ArgumentSpec{Name: "n", Type: TypeInteger, Required: true},
				ArgumentSpec{Name: "delta", Type: TypeInteger},
			)))
			if !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("New error = %v, want ErrInvalidCatalog", err)
			}
		})
	}

	_, err := New(testCatalog(simpleMessage("offset", "{$text :offset add=1}", ArgumentSpec{Name: "text", Type: TypeText, Required: true})))
	if !errors.Is(err, ErrInvalidCatalog) || !strings.Contains(err.Error(), "does not accept text") {
		t.Fatalf("text offset error = %v", err)
	}
}

func TestConstructionLimitProblemsPreserveBothErrorCategories(t *testing.T) {
	limited := &Problems{problems: []Problem{{Code: ProblemLimit, Path: "catalog", Detail: "too large"}}}
	if !errors.Is(limited, ErrInvalidCatalog) || !errors.Is(limited, ErrLimitExceeded) {
		t.Fatalf("limit problem categories = %v", limited)
	}
	invalid := &Problems{problems: []Problem{{Code: ProblemInvalid, Path: "catalog", Detail: "invalid"}}}
	if !errors.Is(invalid, ErrInvalidCatalog) || errors.Is(invalid, ErrLimitExceeded) {
		t.Fatalf("invalid problem categories = %v", invalid)
	}
}

func TestCatalogRejectsReviewSchemaSecurityFallbackAndCapabilityViolations(t *testing.T) {
	tests := []struct {
		name string
		spec CatalogSpec
		want string
	}{
		{
			name: "review unset",
			spec: testCatalog(MessageSpec{ID: "m", Revision: "r", Source: "source", Translations: []Translation{{Locale: "ru", Text: "translation"}}}),
			want: "review",
		},
		{
			name: "plain markup",
			spec: testCatalog(MessageSpec{ID: "m", Revision: "r", Source: "{#strong}x{/strong}", Output: OutputPlain}),
			want: "plain template contains markup",
		},
		{
			name: "missing catchall",
			spec: testCatalog(simpleMessage("m", ".input {$n :number}\n.match $n\none {{one}}", ArgumentSpec{Name: "n", Type: TypeInteger, Required: true})),
			want: "all-other",
		},
		{
			name: "date disabled",
			spec: testCatalog(simpleMessage("m", "{$when}", ArgumentSpec{Name: "when", Type: TypeInstant, Required: true})),
			want: "date/time capability is disabled",
		},
		{
			name: "stale override",
			spec: func() CatalogSpec {
				s := testCatalog(MessageSpec{ID: "m", Revision: "r", Source: "source", Override: OverrideApplication})
				s.Overrides = []Override{{Key: "app.m", Locale: "en", Text: "override", ContractRevision: "old", Review: ReviewApproved}}
				return s
			}(),
			want: "stale",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.spec)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestCatalogBoundsAllAcceptedArtifactMaterial(t *testing.T) {
	longDescription := strings.Repeat("d", 20)
	spec := testCatalog(MessageSpec{
		ID:          "m",
		Revision:    "message",
		Description: longDescription,
		Source:      "{$kind}",
		Arguments:   []ArgumentSpec{{Name: "kind", Type: TypeEnum, Values: []string{"one", "two", "three"}}},
	})
	spec.Limits.MaxDescriptionBytes = 10
	_, err := New(spec)
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("artifact limits error = %v", err)
	}
	spec.Limits.MaxDescriptionBytes = 0
	spec.Limits.MaxEnumValues = 2
	_, err = New(spec)
	if err == nil || !strings.Contains(err.Error(), "values") {
		t.Fatalf("enum limit error = %v", err)
	}

	spec = testCatalog(simpleMessage("m", "a"))
	spec.Limits.MaxCatalogBytes = 8
	_, err = New(spec)
	if err == nil || !strings.Contains(err.Error(), "catalog") {
		t.Fatalf("whole-catalog limit error = %v", err)
	}
}

func TestCatalogCanonicalCollisionAndFallbackCycleAreRejected(t *testing.T) {
	spec := testCatalog(simpleMessage("m", "ok"))
	spec.Supported = []string{"iw", "he"}
	spec.DefaultLocale = "he"
	spec.SourceLocale = "he"
	if _, err := New(spec); err == nil {
		t.Fatal("canonical collision was accepted")
	}
	spec = testCatalog(simpleMessage("m", "ok"))
	spec.Parents = []LocaleEdge{{Locale: "en", Parent: "fr"}, {Locale: "fr", Parent: "en"}}
	if _, err := New(spec); err == nil {
		t.Fatal("fallback cycle was accepted")
	}
}

func TestDefinitionsBindTypedMessagesAndArgumentsAreCloned(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("m", "{$n :number}", ArgumentSpec{Name: "n", Type: TypeBigInteger, Required: true})))
	type input struct{ N string }
	definition, err := NewDefinition(snapshot, Key("app.m"), func(value input) ([]Argument, error) {
		integer, ok := new(big.Int).SetString(value.N, 10)
		if !ok {
			return nil, errors.New("invalid integer")
		}
		return []Argument{BigInteger("n", integer)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := definition.Bind(input{N: "10000000000000000000000000000000000000000"})
	if err != nil {
		t.Fatal(err)
	}
	arguments := message.Arguments()
	arguments[0].big.SetInt64(1)
	if message.Arguments()[0].big.String() == "1" {
		t.Fatal("message arguments escaped by reference")
	}
	if _, err := snapshot.Bind("app.m", BigInteger("n", nil)); err == nil {
		t.Fatal("nil big integer was accepted")
	}
	if _, err := snapshot.Bind("app.m", Null("n")); err == nil {
		t.Fatal("illegal null was accepted")
	}
}

func TestZeroValuesAreSafe(t *testing.T) {
	var snapshot *Snapshot
	if snapshot.Revision() != "" || snapshot.Digest() != "" || snapshot.Profile() != "" || snapshot.Supported() != nil || snapshot.Keys() != nil {
		t.Fatal("nil snapshot accessors are not empty")
	}
	if _, ok := snapshot.Descriptor(""); ok {
		t.Fatal("nil snapshot returned a descriptor")
	}
	if _, err := snapshot.Bind(""); err == nil {
		t.Fatal("nil snapshot bind succeeded")
	}
	var message Message
	if message.Key() != "" || message.ContractRevision() != "" || message.Arguments() != nil {
		t.Fatal("zero message accessors are not empty")
	}
	var argument Argument
	if argument.Name() != "" || argument.Type() != 0 || argument.IsNull() {
		t.Fatal("zero argument accessors are not empty")
	}
	if !reflect.DeepEqual(Definition[int]{}.Key(), Key("")) {
		t.Fatal("zero definition key is not empty")
	}
}

func TestSnapshotKeyLookupsPreflightBoundsAndShape(t *testing.T) {
	const maximum = 16
	const messageID = "abcdefghijkl"
	spec := testCatalog(simpleMessage(messageID, "ok"))
	spec.Limits.MaxIdentifierBytes = maximum
	snapshot := mustSnapshot(t, spec)
	atBound := Key("app." + messageID)
	if len(atBound) != maximum {
		t.Fatalf("test key length = %d", len(atBound))
	}
	if _, err := snapshot.Bind(atBound); err != nil {
		t.Fatalf("at-bound Bind error = %v", err)
	}
	if _, ok := snapshot.Descriptor(atBound); !ok {
		t.Fatal("at-bound Descriptor lookup failed")
	}
	if _, ok := snapshot.ContractRef(atBound); !ok {
		t.Fatal("at-bound ContractRef lookup failed")
	}
	if digest, ok := snapshot.SourceDigest(atBound); !ok || digest == "" {
		t.Fatal("at-bound SourceDigest lookup failed")
	}

	oneOver := Key(strings.Repeat("x", maximum+1))
	if _, err := snapshot.Bind(oneOver); !errors.Is(err, ErrLimitExceeded) || strings.Contains(err.Error(), string(oneOver)) {
		t.Fatalf("one-over Bind error = %v", err)
	}
	if _, ok := snapshot.Descriptor(oneOver); ok {
		t.Fatal("one-over Descriptor lookup succeeded")
	}
	if _, ok := snapshot.ContractRef(oneOver); ok {
		t.Fatal("one-over ContractRef lookup succeeded")
	}
	if _, ok := snapshot.SourceDigest(oneOver); ok {
		t.Fatal("one-over SourceDigest lookup succeeded")
	}

	malformed := Key("invalid key")
	if _, err := snapshot.Bind(malformed); !errors.Is(err, ErrInvalidMessage) || strings.Contains(err.Error(), string(malformed)) {
		t.Fatalf("malformed Bind error = %v", err)
	}
	if _, ok := snapshot.Descriptor(malformed); ok {
		t.Fatal("malformed Descriptor lookup succeeded")
	}
	if _, ok := snapshot.ContractRef(malformed); ok {
		t.Fatal("malformed ContractRef lookup succeeded")
	}
	if _, ok := snapshot.SourceDigest(malformed); ok {
		t.Fatal("malformed SourceDigest lookup succeeded")
	}
}

func TestDescriptorContractRefRejectsInvalidKeysBeforeDigest(t *testing.T) {
	for name, key := range map[string]Key{
		"malformed":              Key("app." + strings.Repeat("x", maximumIdentifierBytes-len("app.")-1) + " "),
		"oversized shaped":       Key("app." + strings.Repeat("x", maximumIdentifierBytes-len("app.")+1)),
		"oversized no separator": Key(strings.Repeat("x", maximumIdentifierBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			descriptor := Descriptor{Key: key, Revision: "revision"}
			if contract := descriptor.ContractRef(); contract != (ContractRef{}) {
				t.Fatalf("invalid descriptor contract = %+v", contract)
			}
		})
	}

	messageID := strings.Repeat("x", maximumIdentifierBytes-len("app."))
	key := Key("app." + messageID)
	spec := testCatalog(simpleMessage(messageID, "ok"))
	spec.Limits.MaxIdentifierBytes = maximumIdentifierBytes
	snapshot := mustSnapshot(t, spec)
	descriptor, ok := snapshot.Descriptor(key)
	if !ok {
		t.Fatal("descriptor is missing")
	}
	want, ok := snapshot.ContractRef(key)
	if !ok {
		t.Fatal("snapshot contract is missing")
	}
	if got := descriptor.ContractRef(); got != want {
		t.Fatalf("descriptor contract = %+v, want %+v", got, want)
	}
}
