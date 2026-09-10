package i18n

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/errs"
)

func TestResolverAccumulatesExclusionsAcrossChoices(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{
		Supported:     []string{"en", "en-US", "fr"},
		Default:       "en",
		DefaultOnMiss: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		choices []Choice
		locale  string
		outcome Outcome
		reason  Reason
	}{
		{
			name:    "broad prior exclusion blocks specific exact",
			choices: []Choice{AcceptLanguage(SourceProtocol, "en;q=0"), Exact(SourceApplication, "en-US")},
			outcome: OutcomeNoMatch,
			reason:  ReasonExcluded,
		},
		{
			name:    "specific prior exclusion does not block parent",
			choices: []Choice{AcceptLanguage(SourceProtocol, "en-US;q=0"), Exact(SourceApplication, "en")},
			locale:  "en",
			outcome: OutcomeSuccess,
			reason:  ReasonExact,
		},
		{
			name:    "wildcard prior exclusion blocks lower choice and default",
			choices: []Choice{AcceptLanguage(SourceProtocol, "*;q=0"), Exact(SourceApplication, "fr")},
			outcome: OutcomeNoMatch,
			reason:  ReasonExcluded,
		},
		{
			name:    "broad prior exclusion blocks later header",
			choices: []Choice{AcceptLanguage(SourceUser, "en;q=0"), AcceptLanguage(SourceProtocol, "en-US")},
			outcome: OutcomeNoMatch,
			reason:  ReasonExcluded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolver.Resolve(test.choices...)
			if got.Locale != test.locale || got.Outcome != test.outcome || got.Reason != test.reason {
				t.Fatalf("Resolve() = %+v", got)
			}
		})
	}
	short, err := NewResolver(LocalePolicy{Supported: []string{"en-US"}, Default: "en-US"})
	if err != nil {
		t.Fatal(err)
	}
	if got := short.Resolve(Exact(SourceExplicit, "en")); got.Matched() || got.Outcome != OutcomeNoMatch || got.Reason != ReasonUnsupported {
		t.Fatalf("short lookup = %+v", got)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := resolver.ResolveContext(canceled, Exact(SourceExplicit, "en")); got.Matched() || got.Outcome != OutcomeCanceled || got.Reason != ReasonContextCanceled {
		t.Fatalf("canceled resolution = %+v", got)
	}
}

func TestRequiredLocaleCanBeCompletedByInitialOverride(t *testing.T) {
	spec := CatalogSpec{
		Revision:      "catalog",
		SourceLocale:  "en",
		DefaultLocale: "en",
		Supported:     []string{"en", "ru"},
		Required:      []string{"ru"},
		Modules: []Module{{Name: "app", Messages: []MessageSpec{{
			ID: "message", Revision: "r", Source: "base", Override: OverrideApplication,
		}}}},
		Overrides: []Override{{Key: "app.message", Locale: "ru", Text: "готово", ContractRevision: "r", Review: ReviewApproved}},
	}
	snapshot := mustSnapshot(t, spec)
	if snapshot.Revision() != "catalog" {
		t.Fatalf("initial override revision = %q", snapshot.Revision())
	}
	message, err := snapshot.Bind("app.message")
	if err != nil {
		t.Fatal(err)
	}
	rendered := mustRender(t, mustView(t, snapshot, "ru", "", "", PresentationDefault), message)
	if rendered.Text != "готово" || rendered.Layer != LayerApplication {
		t.Fatalf("initial override render = %+v", rendered)
	}
	if _, err := snapshot.Overlay(ApplicationOverlay("again", Override{Key: "app.message", Locale: "ru", Text: "again", ContractRevision: "r", Review: ReviewApproved})); err == nil {
		t.Fatal("second application layer was accepted")
	}
}

func TestPinnedDefinitionsRejectStaleContractsAtBootstrap(t *testing.T) {
	baseSpec := testCatalog(MessageSpec{
		ID: "message", Revision: "r", Source: "{$name}", Description: "first",
		Arguments: []ArgumentSpec{{Name: "name", Type: TypeText, Required: true}},
	})
	base := mustSnapshot(t, baseSpec)
	contract, ok := base.ContractRef("app.message")
	if !ok || contract != mustDescriptorContract(t, base, "app.message") {
		t.Fatalf("contract ref = %+v/%v", contract, ok)
	}
	encode := func(value string) ([]Argument, error) { return []Argument{Text("name", value)}, nil }
	definition, err := Define(base, DefinitionSpec[string]{Contract: contract, Encode: encode})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Bind("ok"); err != nil {
		t.Fatal(err)
	}
	staleRevision := contract
	staleRevision.Revision = "old"
	if _, err := Define(base, DefinitionSpec[string]{Contract: staleRevision, Encode: encode}); err == nil {
		t.Fatal("stale revision was accepted")
	}
	staleDigest := contract
	staleDigest.Digest = strings.Repeat("0", len(staleDigest.Digest))
	if _, err := Define(base, DefinitionSpec[string]{Contract: staleDigest, Encode: encode}); err == nil {
		t.Fatal("stale digest was accepted")
	}

	metadataSpec := baseSpec
	metadataSpec.Modules = []Module{{Name: "app", Messages: []MessageSpec{{
		ID: "message", Revision: "r", Source: "{$name}", Description: "second", Public: true,
		Override: OverrideAny, AllowEmpty: true,
		Arguments: []ArgumentSpec{{Name: "name", Type: TypeText, Required: true}},
	}}}}
	metadata := mustSnapshot(t, metadataSpec)
	metadataContract, _ := metadata.ContractRef("app.message")
	if metadataContract != contract {
		t.Fatalf("metadata changed message contract: %+v / %+v", contract, metadataContract)
	}
	if metadata.Digest() == base.Digest() {
		t.Fatal("metadata and AllowEmpty are absent from snapshot digest")
	}
	if _, err := Define(metadata, DefinitionSpec[string]{Contract: contract, Encode: encode}); err != nil {
		t.Fatalf("metadata-only change invalidated generated helper: %v", err)
	}
	oldMessage, err := base.Bind("app.message", Text("name", "Ada"))
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutIsolation(mustRender(t, mustView(t, metadata, "en", "", "", PresentationDefault), oldMessage).Text); got != "Ada" {
		t.Fatalf("old deferred message after metadata change = %q", got)
	}
}

func TestSourceReviewDigestPinsWordingAndContextSeparatelyFromContract(t *testing.T) {
	message := MessageSpec{ID: "welcome", Revision: "r", Source: "Welcome", Description: "Shown after sign-in"}
	digest, err := ExpectedSourceDigestForLocale("", "en", "app", message)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "app", message)
	if err != nil || explicit != digest {
		t.Fatalf("explicit profile digest = %q, %v; want %q", explicit, err, digest)
	}
	reviewDigest, err := ExpectedReviewDigest(digest, "ru", "Добро пожаловать")
	if err != nil {
		t.Fatal(err)
	}
	message.Translations = []Translation{{Locale: "ru", Text: "Добро пожаловать", Review: ReviewApproved, ContractRevision: "r", SourceDigest: digest, ReviewDigest: reviewDigest}}
	base, err := New(testCatalog(message))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := base.SourceDigest("app.welcome"); !ok || got != digest {
		t.Fatalf("snapshot source digest = %q/%v, want %q", got, ok, digest)
	}
	contract, _ := base.ContractRef("app.welcome")

	missing := message
	missing.Translations = []Translation{{Locale: "ru", Text: "Добро пожаловать", Review: ReviewApproved, ContractRevision: "r"}}
	if _, err := New(testCatalog(missing)); err == nil || !strings.Contains(err.Error(), "source_digest") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing source digest error = %v", err)
	}

	for name, mutate := range map[string]func(*MessageSpec){
		"wording": func(spec *MessageSpec) { spec.Source = "Welcome back" },
		"context": func(spec *MessageSpec) { spec.Description = "Shown after account recovery" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := message
			mutate(&changed)
			changedDigest, digestErr := ExpectedSourceDigestForLocale(GrammarProfile, "en", "app", changed)
			if digestErr != nil || changedDigest == digest {
				t.Fatalf("changed source digest = %q, %v", changedDigest, digestErr)
			}
			if _, buildErr := New(testCatalog(changed)); buildErr == nil || !strings.Contains(buildErr.Error(), "stale") {
				t.Fatalf("stale source review error = %v", buildErr)
			}
			changed.Translations = nil
			changedSnapshot := mustSnapshot(t, testCatalog(changed))
			changedContract, _ := changedSnapshot.ContractRef("app.welcome")
			if changedContract != contract {
				t.Fatalf("wording-only change altered typed contract: %+v / %+v", contract, changedContract)
			}
			if _, defineErr := Define(changedSnapshot, DefinitionSpec[struct{}]{
				Contract: contract,
				Encode:   func(struct{}) ([]Argument, error) { return nil, nil },
			}); defineErr != nil {
				t.Fatalf("wording-only change invalidated typed helper: %v", defineErr)
			}
		})
	}

	overlayBase := mustSnapshot(t, testCatalog(MessageSpec{ID: "banner", Revision: "r", Source: "Base", Override: OverrideApplication}))
	override := Override{Key: "app.banner", Locale: "en", Text: "Application", ContractRevision: "r", Review: ReviewApproved}
	if _, err := overlayBase.Overlay(ApplicationOverlay("missing", override)); err == nil || !strings.Contains(err.Error(), "source_digest") {
		t.Fatalf("missing override source digest error = %v", err)
	}
	override.SourceDigest = strings.Repeat("0", 64)
	if _, err := overlayBase.Overlay(ApplicationOverlay("stale", override)); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale override source digest error = %v", err)
	}
	override = reviewedOverride(t, overlayBase, override)
	if _, err := overlayBase.Overlay(ApplicationOverlay("reviewed", override)); err != nil {
		t.Fatalf("reviewed override failed: %v", err)
	}
}

func mustDescriptorContract(t *testing.T, snapshot *Snapshot, key Key) ContractRef {
	t.Helper()
	descriptor, ok := snapshot.Descriptor(key)
	if !ok {
		t.Fatal("descriptor is missing")
	}
	return descriptor.ContractRef()
}

func TestMF2ProfileRejectsInvalidOptionsOperandsSelectorsAndMarkupMetadata(t *testing.T) {
	dateArgument := ArgumentSpec{Name: "date", Type: TypeDate, Required: true}
	instantArgument := ArgumentSpec{Name: "instant", Type: TypeInstant, Required: true}
	numberArgument := ArgumentSpec{Name: "n", Type: TypeDecimal, Required: true}
	textArgument := ArgumentSpec{Name: "s", Type: TypeText, Required: true}
	tests := []struct {
		name         string
		source       string
		arguments    []ArgumentSpec
		capabilities []Capability
		output       OutputKind
		markup       []string
	}{
		{name: "ignored number style", source: "{$n :number style=percent}", arguments: []ArgumentSpec{numberArgument}},
		{name: "unknown select mode", source: ".input {$n :number select=typo}\n.match $n\none {{one}}\n* {{other}}", arguments: []ArgumentSpec{numberArgument}},
		{name: "non-integer digit option", source: "{$n :number maximumFractionDigits=bogus}", arguments: []ArgumentSpec{numberArgument}},
		{name: "out-of-range digit option", source: "{$n :number maximumFractionDigits=101}", arguments: []ArgumentSpec{numberArgument}},
		{name: "numeric literal operand", source: "{|1| :number}"},
		{name: "wrong external operand", source: "{$s :number}", arguments: []ArgumentSpec{textArgument}},
		{name: "wrong local operand", source: ".local $local = {$s}\n{{{$local :number}}}", arguments: []ArgumentSpec{textArgument}},
		{name: "date with time option", source: "{$date :date timeStyle=short}", arguments: []ArgumentSpec{dateArgument}, capabilities: []Capability{CapabilityDateTime}},
		{name: "invalid date option value", source: "{$date :date dateStyle=bogus}", arguments: []ArgumentSpec{dateArgument}, capabilities: []Capability{CapabilityDateTime}},
		{name: "exact selector with category", source: ".input {$n :number select=exact}\n.match $n\none {{bad}}\n* {{other}}", arguments: []ArgumentSpec{numberArgument}},
		{name: "noncanonical numeric selector", source: ".input {$n :number select=cardinal}\n.match $n\none {{bad}}\n* {{other}}", arguments: []ArgumentSpec{numberArgument}},
		{name: "unknown plural category", source: ".input {$n :number select=plural}\n.match $n\noen {{bad}}\n* {{other}}", arguments: []ArgumentSpec{numberArgument}},
		{name: "invalid string select mode", source: ".input {$s :string select=exact}\n.match $s\nx {{x}}\n* {{other}}", arguments: []ArgumentSpec{textArgument}},
		{name: "markup options", source: "{#strong tone=|loud|}x{/strong}", output: OutputRich, markup: []string{"strong"}},
		{name: "time function rejects date", source: "{$date :time timeStyle=short}", arguments: []ArgumentSpec{dateArgument}, capabilities: []Capability{CapabilityDateTime}},
		{name: "datetime function rejects date", source: "{$date :datetime}", arguments: []ArgumentSpec{dateArgument}, capabilities: []Capability{CapabilityDateTime}},
		{name: "date style conflict", source: "{$instant :datetime dateStyle=short year=numeric}", arguments: []ArgumentSpec{instantArgument}, capabilities: []Capability{CapabilityDateTime}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := testCatalog(MessageSpec{ID: "m", Revision: "r", Source: test.source, Arguments: test.arguments, Output: test.output, Markup: test.markup})
			spec.Capabilities = test.capabilities
			if _, err := New(spec); err == nil {
				t.Fatal("invalid MF2 profile input was accepted")
			}
		})
	}

	controls := []CatalogSpec{
		testCatalog(simpleMessage("m", "{$n :number minimumFractionDigits=1 maximumFractionDigits=2}", numberArgument)),
		testCatalog(simpleMessage("m", ".input {$n :number select=ordinal}\n.match $n\none {{one}}\ntwo {{two}}\nfew {{few}}\n* {{other}}", numberArgument)),
	}
	dateControl := testCatalog(simpleMessage("m", "{$date :date dateStyle=short}", dateArgument))
	dateControl.Capabilities = []Capability{CapabilityDateTime}
	controls = append(controls, dateControl)
	for i, control := range controls {
		if _, err := New(control); err != nil {
			t.Fatalf("valid profile control %d failed: %v", i, err)
		}
	}
}

func TestOptionalArgumentsAreAbsentNullAndFunctionSafe(t *testing.T) {
	spec := testCatalog(
		MessageSpec{ID: "unused", Revision: "r", Source: "ok", Arguments: []ArgumentSpec{{Name: "unused", Type: TypeText}}},
		MessageSpec{ID: "raw", Revision: "r", Source: "A{$value}B", Arguments: []ArgumentSpec{{Name: "value", Type: TypeText, Nullable: true}}},
		MessageSpec{ID: "select", Revision: "r", Source: ".input {$value :string}\n.match $value\nnull {{none}}\n* {{some}}", Arguments: []ArgumentSpec{{Name: "value", Type: TypeText, Nullable: true}}},
		MessageSpec{ID: "function", Revision: "r", Source: "A{$value :number}B", Arguments: []ArgumentSpec{{Name: "value", Type: TypeDecimal, Nullable: true}}},
	)
	snapshot := mustSnapshot(t, spec)
	view := mustView(t, snapshot, "en", "", "", PresentationNoIsolation)
	for key, want := range map[Key]string{"app.unused": "ok", "app.raw": "AB", "app.select": "none", "app.function": "AB"} {
		message, err := snapshot.Bind(key)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustRender(t, view, message).Text; got != want {
			t.Errorf("%s absent optional = %q, want %q", key, got, want)
		}
	}
	absent, _ := snapshot.Bind("app.raw")
	explicit, err := snapshot.Bind("app.raw", Null("value"))
	if err != nil {
		t.Fatal(err)
	}
	absentKey, _ := view.RenderKey(absent)
	explicitKey, _ := view.RenderKey(explicit)
	if absentKey == explicitKey {
		t.Fatal("absent and explicit null share a render key")
	}
}

func TestOutputBudgetStopsRepeatedDynamicExpansion(t *testing.T) {
	messageSpec := simpleMessage("m", strings.Repeat("{$value}", 64), ArgumentSpec{Name: "value", Type: TypeText, Required: true})
	spec := testCatalog(messageSpec)
	spec.Limits.MaxOutputBytes = 256
	snapshot := mustSnapshot(t, spec)
	message, err := snapshot.Bind("app.m", Text("value", strings.Repeat("x", 128)))
	if err != nil {
		t.Fatal(err)
	}
	view := mustView(t, snapshot, "en", "", "", PresentationNoIsolation)
	if _, err := view.Render(context.Background(), message); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("repeated expansion error = %v", err)
	}
	budget := &renderBudget{bytes: 2, parts: 2}
	if err := budget.charge(3, 1); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("first overrun = %v", err)
	}
	if err := budget.charge(0, 0); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("exhausted budget resumed = %v", err)
	}

	control := testCatalog(messageSpec)
	control.Limits.MaxOutputBytes = 64 * 128
	controlSnapshot := mustSnapshot(t, control)
	controlMessage, _ := controlSnapshot.Bind("app.m", Text("value", strings.Repeat("x", 128)))
	if got := mustRender(t, mustView(t, controlSnapshot, "en", "", "", PresentationNoIsolation), controlMessage).Text; len(got) != 64*128 {
		t.Fatalf("bounded control output bytes = %d", len(got))
	}
}

func TestPercentExactSelectionUsesScaledExactValue(t *testing.T) {
	snapshot := localeSnapshot(t, "en", ".input {$rate :percent select=exact}\n.match $rate\n25 {{quarter}}\n* {{other}}", ArgumentSpec{Name: "rate", Type: TypeDecimal, Required: true})
	message, err := snapshot.Bind("app.m", Decimal("rate", "0.25"))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRender(t, mustView(t, snapshot, "en", "", "", PresentationNoIsolation), message).Text; got != "quarter" {
		t.Fatalf("percent exact selection = %q", got)
	}
}

func TestPluralSelectionAndFormattingShareDigitOptions(t *testing.T) {
	snapshot := localeSnapshot(t, "ru", ".input {$n :number select=plural maximumFractionDigits=0}\n.match $n\none {{one {$n}}}\nfew {{few {$n}}}\n* {{other {$n}}}", ArgumentSpec{Name: "n", Type: TypeDecimal, Required: true})
	view := mustView(t, snapshot, "ru", "ru", "", PresentationNoIsolation)
	for value, want := range map[string]string{"1.49": "one 1", "1.50": "few 2"} {
		message, err := snapshot.Bind("app.m", Decimal("n", value))
		if err != nil {
			t.Fatal(err)
		}
		if got := mustRender(t, view, message).Text; got != want {
			t.Errorf("rounded plural %s = %q, want %q", value, got, want)
		}
	}
}

func TestBidiControlsAreRejectedInTemplatesSchemasAndArguments(t *testing.T) {
	if _, err := New(testCatalog(simpleMessage("m", "unsafe\u202e"))); err == nil {
		t.Fatal("template bidi override was accepted")
	}
	if _, err := New(testCatalog(simpleMessage("m", "{$value}", ArgumentSpec{Name: "value", Type: TypeEnum, Values: []string{"safe", "unsafe\u2069"}}))); err == nil {
		t.Fatal("enum bidi control was accepted")
	}
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("m", "{$value}", ArgumentSpec{Name: "value", Type: TypeText, Required: true})))
	if _, err := snapshot.Bind("app.m", Text("value", "safe\u2069\u202eunsafe")); err == nil {
		t.Fatal("argument bidi controls were accepted")
	}
	if _, err := snapshot.Bind("app.m", Text("value", "مرحبا")); err != nil {
		t.Fatalf("ordinary RTL text was rejected: %v", err)
	}
}

func TestAuthoredDirectionalMarksAndBalancedIsolatesArePreserved(t *testing.T) {
	source := "\u200fRTL \u061c\u2067مرحبا\u2069"
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("directional", source)))
	message, err := snapshot.Bind("app.directional")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := mustView(t, snapshot, "en", "", "", PresentationDefault).Render(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Text != source {
		t.Fatalf("authored directional text = %q, want %q", rendered.Text, source)
	}
	for _, invalid := range []string{"open \u2067", "close \u2069", "override \u202e"} {
		if _, err := New(testCatalog(simpleMessage("invalid", invalid))); err == nil {
			t.Fatalf("unsafe authored bidi sequence %q was accepted", invalid)
		}
	}
	argumentSnapshot := mustSnapshot(t, testCatalog(simpleMessage("argument", "{$value}", ArgumentSpec{Name: "value", Type: TypeText, Required: true})))
	if _, err := argumentSnapshot.Bind("app.argument", Text("value", "\u200f")); err == nil {
		t.Fatal("untrusted directional mark was accepted")
	}
}

func TestResolutionOwnershipAndTimeZoneIdentity(t *testing.T) {
	first := mustSnapshot(t, testCatalog(simpleMessage("m", "ok")))
	second := mustSnapshot(t, testCatalog(simpleMessage("m", "ok")))
	if _, err := first.View(ViewSpec{Resolution: Resolution{Locale: "en", Source: SourceExplicit, Outcome: OutcomeSuccess, Reason: ReasonExact}}); err == nil {
		t.Fatal("forged resolution was accepted")
	}
	if _, err := first.View(ViewSpec{Resolution: second.Resolve(Exact(SourceExplicit, "en"))}); err == nil {
		t.Fatal("foreign resolver resolution was accepted")
	}
	owned := first.Resolve(Exact(SourceExplicit, "en"))
	if _, err := first.View(ViewSpec{Resolution: owned}); err != nil {
		t.Fatalf("owned resolution was rejected: %v", err)
	}
	copyOfOwned := owned
	if _, err := first.View(ViewSpec{Resolution: copyOfOwned}); err != nil {
		t.Fatalf("copied resolution was rejected: %v", err)
	}
	mutations := map[string]func(*Resolution){
		"locale":  func(value *Resolution) { value.Locale = "fr-CA" },
		"source":  func(value *Resolution) { value.Source = SourceUser },
		"outcome": func(value *Resolution) { value.Outcome = OutcomeFallback },
		"reason":  func(value *Resolution) { value.Reason = ReasonWildcard },
		"tuple": func(value *Resolution) {
			value.Outcome = OutcomeFallback
			value.Reason = ReasonLookup
		},
	}
	for name, mutate := range mutations {
		t.Run("tampered "+name, func(t *testing.T) {
			candidate := owned
			mutate(&candidate)
			if _, err := first.View(ViewSpec{Resolution: candidate}); err == nil {
				t.Fatal("tampered resolution was accepted")
			}
		})
	}
	overlayBase := mustSnapshot(t, testCatalog(MessageSpec{ID: "overlay", Revision: "r", Source: "base", Description: "Overlay resolution", Override: OverrideApplication}))
	overlayResolution := overlayBase.Resolve(Exact(SourceExplicit, "en"))
	overlay, err := overlayBase.Overlay(ApplicationOverlay("application"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := overlay.View(ViewSpec{Resolution: overlayResolution}); err != nil {
		t.Fatalf("overlay rejected shared resolver resolution: %v", err)
	}

	nonUTC := testCatalog(simpleMessage("m", "ok"))
	nonUTC.DefaultTimeZone = "Asia/Almaty"
	if _, err := New(nonUTC); err == nil {
		t.Fatal("non-UTC default without data version was accepted")
	}
	if _, err := first.View(ViewSpec{Resolution: first.Resolve(Exact(SourceExplicit, "en")), TimeZone: "Asia/Almaty"}); err == nil {
		t.Fatal("non-UTC view without data version was accepted")
	}

	versioned := testCatalog(simpleMessage("m", "ok"))
	versioned.TimeZoneDataVersion = "iana-2026a"
	a := mustSnapshot(t, versioned)
	versioned.TimeZoneDataVersion = "iana-2026b"
	b := mustSnapshot(t, versioned)
	if a.Digest() == b.Digest() || a.TimeZoneDataVersion() != "iana-2026a" || b.TimeZoneDataVersion() != "iana-2026b" {
		t.Fatalf("time-zone identity = %q/%q, %q/%q", a.TimeZoneDataVersion(), b.TimeZoneDataVersion(), a.Digest(), b.Digest())
	}
	messageA, _ := a.Bind("app.m")
	messageB, _ := b.Bind("app.m")
	viewA, err := a.View(ViewSpec{Resolution: a.Resolve(Exact(SourceExplicit, "en")), TimeZone: "Asia/Almaty"})
	if err != nil {
		t.Fatal(err)
	}
	viewB, err := b.View(ViewSpec{Resolution: b.Resolve(Exact(SourceExplicit, "en")), TimeZone: "Asia/Almaty"})
	if err != nil {
		t.Fatal(err)
	}
	keyA, _ := viewA.RenderKey(messageA)
	keyB, _ := viewB.RenderKey(messageB)
	if keyA == keyB {
		t.Fatal("time-zone data version is absent from render key")
	}
}

func TestStrictOverlayLayersAndInputBounds(t *testing.T) {
	spec := testCatalog(MessageSpec{ID: "m", Revision: "r", Source: "base", Override: OverrideAny})
	base := mustSnapshot(t, spec)
	application, err := base.Overlay(ApplicationOverlay("application", reviewedOverride(t, base, Override{Key: "app.m", Locale: "en", Text: "application", ContractRevision: "r", Review: ReviewApproved})))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Overlay(ApplicationOverlay("again")); err == nil {
		t.Fatal("application-to-application overlay was accepted")
	}
	tenant, err := application.Overlay(TenantOverlay("tenant", reviewedOverride(t, application, Override{Key: "app.m", Locale: "en", Text: "tenant", ContractRevision: "r", Review: ReviewApproved})))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.Overlay(TenantOverlay("another")); err == nil {
		t.Fatal("tenant-to-tenant overlay was accepted")
	}

	boundedSpec := testCatalog(MessageSpec{ID: "m", Revision: "r", Source: "base", Override: OverrideApplication})
	boundedSpec.Limits.MaxTranslations = 2
	bounded := mustSnapshot(t, boundedSpec)
	tooMany := make([]Override, 3)
	if _, err := bounded.Overlay(ApplicationOverlay("large", tooMany...)); err == nil {
		t.Fatal("oversized overlay input was accepted")
	}
}

func TestConstructionDiagnosticsBigIntegersAndErrorSpecsAreBounded(t *testing.T) {
	set := &problemSet{}
	for i := 0; i < 1000; i++ {
		set.add(ProblemInvalid, strings.Repeat("p", 2048), strings.Repeat("d", 2048))
	}
	var problems *Problems
	if !errors.As(set.err(), &problems) || len(problems.Items()) > 257 {
		t.Fatalf("bounded problems = %T/%d", set.err(), len(problems.Items()))
	}
	for _, problem := range problems.Items() {
		if len(problem.Path) > 1024 || len(problem.Detail) > 1024 {
			t.Fatalf("unbounded problem = %+v", problem)
		}
	}

	tooLarge := new(big.Int).Lsh(big.NewInt(1), maxBigIntegerBits)
	if argument := BigInteger("n", tooLarge); argument.invalid == "" {
		t.Fatal("oversized BigInteger was cloned")
	}
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("m", "{$n :number}", ArgumentSpec{Name: "n", Type: TypeBigInteger, Required: true})))
	if _, err := snapshot.Bind("app.m", BigInteger("n", new(big.Int).Exp(big.NewInt(10), big.NewInt(1000), nil))); err != nil {
		t.Fatalf("bounded large integer control failed: %v", err)
	}
	limitedSpec := testCatalog(simpleMessage("m", "{$n :number}", ArgumentSpec{Name: "n", Type: TypeBigInteger, Required: true}))
	limitedSpec.Limits.MaxBigIntegerBits = 64
	limited := mustSnapshot(t, limitedSpec)
	if _, err := limited.Bind("app.m", BigInteger("n", new(big.Int).Lsh(big.NewInt(1), 100))); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("configured BigInteger limit = %v", err)
	}

	errorSnapshot := errorCatalog(t, nil)
	params := make([]ErrorParam, errorSnapshot.limits.MaxArguments+1)
	if _, err := errorSnapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{{Ladder: "required", Key: "errors.generic", Params: params}}}); err == nil {
		t.Fatal("oversized error mapping parameters were accepted")
	}
	path := make(errs.Path, errorSnapshot.limits.MaxExplainSteps+1)
	source, err := errorSnapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{{Ladder: "required", Key: "errors.generic"}}})
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := source.Message(context.Background(), errs.Violation{Path: path, Code: "required"}, "en"); ok || text != "" {
		t.Fatalf("oversized violation path = %q/%v", text, ok)
	}
}

func TestErrorMessagesPreferLocaleBeforeLadderSpecificity(t *testing.T) {
	translation := []Translation{{Locale: "ru", Text: "общее", Review: ReviewApproved, ContractRevision: "r"}}
	snapshot := mustSnapshot(t, CatalogSpec{
		Revision: "errors", SourceLocale: "en", DefaultLocale: "en", Supported: []string{"en", "ru"},
		Modules: []Module{{Name: "errors", Messages: []MessageSpec{
			{ID: "specific", Revision: "r", Source: "specific English"},
			{ID: "generic", Revision: "r", Source: "generic English", Translations: translation},
		}}},
	})
	source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{
		{Ladder: "user.email.required", Key: "errors.specific"},
		{Ladder: "required", Key: "errors.generic"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	violation := errs.Violation{Path: errs.Path{errs.Named("user"), errs.Named("email")}, Code: "required"}
	if got, ok := source.Message(context.Background(), violation, "ru"); !ok || got != "общее" {
		t.Fatalf("locale-first error selection = %q/%v", got, ok)
	}
	if got, actual, ok := source.MessageWithLocale(context.Background(), violation, "ru-RU"); !ok || got != "общее" || actual != "ru" {
		t.Fatalf("actual template locale = %q/%q/%v", got, actual, ok)
	}
	if got, ok := source.Message(context.Background(), violation, "en"); !ok || got != "specific English" {
		t.Fatalf("ladder-specific control = %q/%v", got, ok)
	}
}

func TestExportedEnumValidityAndStrings(t *testing.T) {
	valid := []interface {
		String() string
		Valid() bool
	}{
		OutputPlain, ReviewUnset, OverrideDenied, LayerModule, CapabilityDateTime, SourceExplicit,
		MatchLookup, TypeText, PresentationDefault, PartText, OperationResolve, OutcomeSuccess, ReasonNone, ProblemInvalid,
	}
	for _, value := range valid {
		if !value.Valid() || value.String() == "unknown" || value.String() == "" {
			t.Errorf("valid enum = %T %q/%v", value, value.String(), value.Valid())
		}
	}
	invalid := []interface {
		String() string
		Valid() bool
	}{
		OutputKind(255), ReviewState(255), OverridePolicy(255), Layer(255), Capability(255), ChoiceSource(255),
		MatchMode(255), ArgumentType(255), Presentation(255), PartKind(255), Operation(255), Outcome(255), Reason(255), ProblemCode("bogus"),
	}
	for _, value := range invalid {
		if value.Valid() || value.String() != "unknown" && value.String() != "bogus" {
			t.Errorf("invalid enum = %T %q/%v", value, value.String(), value.Valid())
		}
	}
}

func TestDateDoesNotShiftAndInstantNeedsExplicitViewZone(t *testing.T) {
	spec := testCatalog(MessageSpec{
		ID: "m", Revision: "r", Source: "{$date :date dateStyle=full}|{$instant :datetime dateStyle=short timeStyle=short}",
		Arguments: []ArgumentSpec{{Name: "date", Type: TypeDate, Required: true}, {Name: "instant", Type: TypeInstant, Required: true}},
	})
	spec.Capabilities = []Capability{CapabilityDateTime}
	spec.TimeZoneDataVersion = "iana-2026a"
	snapshot := mustSnapshot(t, spec)
	message, err := snapshot.Bind("app.m", Date("date", 2024, time.January, 2), Instant("instant", time.Date(2024, time.January, 2, 1, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	utc := mustRender(t, mustView(t, snapshot, "en", "en", "UTC", PresentationNoIsolation), message).Text
	honolulu := mustRender(t, mustView(t, snapshot, "en", "en", "Pacific/Honolulu", PresentationNoIsolation), message).Text
	if !strings.Contains(utc, "January 2, 2024") || !strings.Contains(honolulu, "January 2, 2024") || utc == honolulu {
		t.Fatalf("date/instant zone split = %q / %q", utc, honolulu)
	}
}
