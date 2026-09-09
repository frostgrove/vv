package i18n

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestResolverAcceptLanguageAndCanonicalization(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{
		Supported:     []string{"en", "FR-ca", "iw", "zh-Hant-HK", "sr-Latn"},
		Default:       "EN",
		DefaultOnMiss: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resolver.Supported(), []string{"en", "fr-CA", "he", "zh-Hant-HK", "sr-Latn"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Supported() = %v, want %v", got, want)
	}
	cases := []struct {
		name    string
		choice  Choice
		locale  string
		reason  Reason
		outcome Outcome
	}{
		{name: "weighted", choice: AcceptLanguage(SourceProtocol, "de, en;q=0.1"), locale: "en", reason: ReasonExact, outcome: OutcomeSuccess},
		{name: "repeated values", choice: AcceptLanguage(SourceProtocol, "de;q=0.9", "fr-CA;q=0.8"), locale: "fr-CA", reason: ReasonExact, outcome: OutcomeSuccess},
		{name: "legacy alias", choice: Exact(SourceUser, "iw"), locale: "he", reason: ReasonExact, outcome: OutcomeSuccess},
		{name: "case", choice: Exact(SourceUser, "ZH-hant-hk"), locale: "zh-Hant-HK", reason: ReasonExact, outcome: OutcomeSuccess},
		{name: "script", choice: Exact(SourceUser, "sr-latn"), locale: "sr-Latn", reason: ReasonExact, outcome: OutcomeSuccess},
		{name: "lookup", choice: Exact(SourceUser, "fr-CA-x-client"), locale: "fr-CA", reason: ReasonLookup, outcome: OutcomeFallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolver.Resolve(tc.choice)
			if got.Locale != tc.locale || got.Reason != tc.reason || got.Outcome != tc.outcome {
				t.Fatalf("Resolve() = %+v", got)
			}
		})
	}
	first := resolver.Resolve(Exact(SourceExplicit, "he"), Exact(SourceApplication, "en"))
	if first.Locale != "he" || first.Source != SourceExplicit {
		t.Fatalf("choice precedence = %+v", first)
	}
	copy := resolver.Supported()
	copy[0] = "mutated"
	if resolver.Supported()[0] != "en" {
		t.Fatal("supported locales escaped by reference")
	}
}

func TestLookupDoesNotExpandAGenericRangeToAnArbitrarySpecificLocale(t *testing.T) {
	for _, supported := range [][]string{{"zh-Hans", "zh-Hant"}, {"zh-Hant", "zh-Hans"}} {
		resolver, err := NewResolver(LocalePolicy{Supported: supported, Default: supported[0], Mode: MatchLookup})
		if err != nil {
			t.Fatal(err)
		}
		for _, choice := range []Choice{Exact(SourceExplicit, "zh"), AcceptLanguage(SourceProtocol, "zh")} {
			resolved := resolver.Resolve(choice)
			if resolved.Matched() || resolved.Outcome != OutcomeNoMatch || resolved.Reason != ReasonUnsupported {
				t.Fatalf("lookup with supported %v expanded zh: %+v", supported, resolved)
			}
		}
	}

	resolver, err := NewResolver(LocalePolicy{
		Supported:     []string{"zh-Hans", "zh-Hant"},
		Default:       "zh-Hant",
		Mode:          MatchLookup,
		DefaultOnMiss: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := resolver.Resolve(Exact(SourceExplicit, "zh"))
	if resolved.Locale != "zh-Hant" || resolved.Outcome != OutcomeDefault || resolved.Reason != ReasonPolicyDefault || resolved.Source != SourceApplication {
		t.Fatalf("explicit default after generic miss = %+v", resolved)
	}
}

func TestLookupUsesSyntacticTruncationInsteadOfCLDRParentInference(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{Supported: []string{"zh-Hant", "de-DE", "en"}, Default: "en", Mode: MatchLookup})
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range []Choice{Exact(SourceExplicit, "zh-TW"), AcceptLanguage(SourceProtocol, "zh-TW")} {
		resolved := resolver.Resolve(choice)
		if resolved.Matched() || resolved.Outcome != OutcomeNoMatch || resolved.Reason != ReasonUnsupported {
			t.Fatalf("strict lookup inferred a CLDR script parent: %+v", resolved)
		}
	}
	for _, test := range []struct {
		requested string
		want      string
	}{
		{requested: "de-DE-u-co-phonebk", want: "de-DE"},
		{requested: "en-x-client", want: "en"},
	} {
		resolved := resolver.Resolve(Exact(SourceExplicit, test.requested))
		if resolved.Locale != test.want || resolved.Outcome != OutcomeFallback || resolved.Reason != ReasonLookup {
			t.Fatalf("lookup %q = %+v", test.requested, resolved)
		}
	}
	explicit, err := NewResolver(LocalePolicy{
		Supported: []string{"zh-Hant", "en"}, Default: "en", Mode: MatchLookup,
		Parents: []LocaleEdge{{Locale: "zh-TW", Parent: "zh-Hant"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := explicit.Resolve(Exact(SourceExplicit, "zh-TW"))
	if resolved.Locale != "zh-Hant" || resolved.Reason != ReasonLookup {
		t.Fatalf("explicit parent lookup = %+v", resolved)
	}
}

func TestResolverValidatesTheSameSyntacticParentGraphItExecutes(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{
		Supported: []string{"zh-TW", "en"}, Default: "en", Mode: MatchLookup,
		Parents: []LocaleEdge{{Locale: "zh-Hant", Parent: "zh-TW"}},
	})
	if err != nil {
		t.Fatalf("valid RFC lookup graph was rejected through a CLDR-only cycle: %v", err)
	}
	resolved := resolver.Resolve(Exact(SourceExplicit, "zh-Hant"))
	if resolved.Locale != "zh-TW" || resolved.Reason != ReasonLookup {
		t.Fatalf("explicit syntactic graph resolution = %+v", resolved)
	}
	_, err = NewResolver(LocalePolicy{
		Supported: []string{"en"}, Default: "en", Mode: MatchLookup,
		Parents: []LocaleEdge{{Locale: "zh", Parent: "zh-TW"}, {Locale: "zh-Hant", Parent: "en"}},
	})
	if !errors.Is(err, ErrInvalidLocale) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("runtime syntactic cycle was accepted: %v", err)
	}
}

func TestCatalogSeparatelyRejectsCyclesInItsCLDRTemplateFallbackGraph(t *testing.T) {
	spec := testCatalog(simpleMessage("fallback", "ok"))
	spec.Supported = []string{"en", "zh-TW"}
	spec.DefaultLocale = "en"
	spec.Parents = []LocaleEdge{{Locale: "zh-Hant", Parent: "zh-TW"}}
	if _, err := New(reviewedCatalog(t, spec)); err == nil || !strings.Contains(err.Error(), "template fallback cycle") {
		t.Fatalf("CLDR template fallback cycle = %v", err)
	}
}

func TestResolveContextObservesCancellationDuringMatching(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{Supported: []string{"en", "fr"}, Default: "en", Mode: MatchBestFit})
	if err != nil {
		t.Fatal(err)
	}
	resolution := resolver.ResolveContext(&countdownContext{remaining: 7}, AcceptLanguage(SourceProtocol, "de"))
	if resolution.Outcome != OutcomeCanceled || resolution.Reason != ReasonContextCanceled || resolution.Matched() {
		t.Fatalf("matching cancellation = %+v", resolution)
	}
}

func TestResolverExclusionsAreAsymmetricAndWildcardZeroRejectsDefault(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{Supported: []string{"en-US", "fr"}, Default: "en-US", DefaultOnMiss: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolver.Resolve(AcceptLanguage(SourceProtocol, "en;q=0,*;q=1")); got.Locale != "fr" || got.Reason != ReasonWildcard {
		t.Fatalf("broad exclusion = %+v", got)
	}
	if got := resolver.Resolve(AcceptLanguage(SourceProtocol, "*;q=0")); got.Matched() || got.Reason != ReasonExcluded {
		t.Fatalf("wildcard exclusion = %+v", got)
	}

	parent, err := NewResolver(LocalePolicy{Supported: []string{"en", "fr"}, Default: "en", DefaultOnMiss: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := parent.Resolve(AcceptLanguage(SourceProtocol, "en-US;q=0,*;q=1")); got.Locale != "en" {
		t.Fatalf("specific exclusion excluded parent: %+v", got)
	}
	if got := resolver.Resolve(AcceptLanguage(SourceProtocol, "en;q=0,en-US;q=1")); got.Locale != "en-US" {
		t.Fatalf("specific positive did not override broad range: %+v", got)
	}
}

func TestResolverBestFitHonorsBroadExclusionsWithoutBlockingExactSpecific(t *testing.T) {
	bestFit, err := NewResolver(LocalePolicy{Supported: []string{"en-GB"}, Default: "en-GB", Mode: MatchBestFit})
	if err != nil {
		t.Fatal(err)
	}
	if got := bestFit.Resolve(AcceptLanguage(SourceProtocol, "en-US;q=1,en;q=0")); got.Matched() || got.Outcome != OutcomeNoMatch || got.Reason != ReasonExcluded {
		t.Fatalf("sibling best fit escaped broad exclusion: %+v", got)
	}

	exact, err := NewResolver(LocalePolicy{Supported: []string{"en-US"}, Default: "en-US", Mode: MatchBestFit})
	if err != nil {
		t.Fatal(err)
	}
	if got := exact.Resolve(AcceptLanguage(SourceProtocol, "en-US;q=1,en;q=0")); got.Locale != "en-US" || got.Outcome != OutcomeSuccess || got.Reason != ReasonExact {
		t.Fatalf("specific exact match was blocked: %+v", got)
	}
	if got := exact.Resolve(AcceptLanguage(SourceProtocol, "en-US-x-client;q=1,en;q=0")); got.Locale != "en-US" || got.Outcome != OutcomeFallback || got.Reason != ReasonLookup {
		t.Fatalf("specific lookup match was blocked: %+v", got)
	}
}

func TestResolverRejectsQValuesWithoutLeadingDigit(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{Supported: []string{"en"}, Default: "en"})
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"en;q=.1", "en;q=.000", "en;q=.999", "en;q=00.1", "en;q=1.001", "en;q=2"} {
		got := resolver.Resolve(AcceptLanguage(SourceProtocol, header))
		if got.Matched() || got.Outcome != OutcomeInvalid || got.Reason != ReasonMalformed {
			t.Errorf("Resolve(%q) = %+v", header, got)
		}
	}
	for _, header := range []string{"en;q=0", "en;q=0.", "en;q=0.000", "en;q=0.001", "en;q=1", "en;q=1.", "en;q=1.000"} {
		got := resolver.Resolve(AcceptLanguage(SourceProtocol, header))
		if header == "en;q=0" || header == "en;q=0." || header == "en;q=0.000" {
			if got.Matched() || got.Reason != ReasonExcluded {
				t.Errorf("Resolve(%q) = %+v", header, got)
			}
			continue
		}
		if got.Locale != "en" || !got.Matched() {
			t.Errorf("Resolve(%q) = %+v", header, got)
		}
	}
}

func TestResolverRejectsMalformedOversizedCyclesAndDeepParents(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{Supported: []string{"en"}, Default: "en", Limits: LocaleLimits{MaxHeaderBytes: 8}})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolver.Resolve(AcceptLanguage(SourceProtocol, "en;q=bogus")); got.Outcome != OutcomeLimited && got.Outcome != OutcomeInvalid {
		t.Fatalf("malformed outcome = %+v", got)
	}
	if got := resolver.Resolve(AcceptLanguage(SourceProtocol, strings.Repeat("x", 9))); got.Outcome != OutcomeLimited {
		t.Fatalf("oversized outcome = %+v", got)
	}
	separatorBounded, err := NewResolver(LocalePolicy{Supported: []string{"en", "fr"}, Default: "en", Limits: LocaleLimits{MaxHeaderBytes: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if got := separatorBounded.Resolve(AcceptLanguage(SourceProtocol, "en", "fr")); got.Outcome != OutcomeLimited {
		t.Fatalf("split oversized outcome = %+v", got)
	}
	_, err = NewResolver(LocalePolicy{Supported: []string{"en"}, Default: "en", Parents: []LocaleEdge{{Locale: "en", Parent: "fr"}, {Locale: "fr", Parent: "en"}}})
	if err == nil {
		t.Fatal("cycle was accepted")
	}
	_, err = NewResolver(LocalePolicy{
		Supported: []string{"en"},
		Default:   "en",
		Limits:    LocaleLimits{MaxFallbackDepth: 2},
		Parents:   []LocaleEdge{{Locale: "en-US-x-one", Parent: "en-US-x-two"}, {Locale: "en-US-x-two", Parent: "en-US-x-three"}, {Locale: "en-US-x-three", Parent: "en"}},
	})
	if err == nil {
		t.Fatal("overdeep parent graph was accepted")
	}
}

func TestResolverObservationIsOncePrivacySafeAndPanicIsolated(t *testing.T) {
	var mu sync.Mutex
	observations := make([]Observation, 0, 2)
	resolver, err := NewResolver(LocalePolicy{
		Supported: []string{"en"},
		Default:   "en",
		Observer: func(_ context.Context, observation Observation) {
			mu.Lock()
			observations = append(observations, observation)
			mu.Unlock()
			panic("observer")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolver.ResolveContext(nil, Exact(SourceExplicit, "en")); got.Locale != "en" {
		t.Fatalf("ResolveContext(nil) = %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observations) != 1 {
		t.Fatalf("observer calls = %d", len(observations))
	}
	want := Observation{Operation: OperationResolve, Outcome: OutcomeSuccess, Reason: ReasonExact, Count: 1}
	observations[0].Duration = 0
	if observations[0] != want {
		t.Fatalf("observation = %+v, want %+v", observations[0], want)
	}
}

func TestSnapshotForHasExplicitBoundedLocaleSemantics(t *testing.T) {
	spec := testCatalog(simpleMessage("m", "ok"))
	snapshot := mustSnapshot(t, spec)
	view, err := snapshot.For("FR-ca")
	if err != nil || view.resolution.Locale != "fr-CA" || view.resolution.Source != SourceExplicit || view.formattingLocale != "fr-CA" || view.timeZone != "UTC" {
		t.Fatalf("For() = %+v, %v", view, err)
	}
	if _, err := snapshot.For("de"); err == nil {
		t.Fatal("unsupported explicit locale silently defaulted")
	}
	if _, err := snapshot.For(""); err == nil {
		t.Fatal("malformed explicit locale silently defaulted")
	}
	defaulted := spec
	defaulted.DefaultOnMiss = true
	defaultSnapshot := mustSnapshot(t, defaulted)
	view, err = defaultSnapshot.For("de")
	if err != nil || view.resolution.Locale != "en" || view.resolution.Outcome != OutcomeDefault {
		t.Fatalf("default-on-miss For() = %+v, %v", view, err)
	}
	view, err = snapshot.ForContext(nil)
	if err != nil || view.resolution.Locale != "en" || view.resolution.Outcome != OutcomeDefault {
		t.Fatalf("choice-free ForContext() = %+v, %v", view, err)
	}
}

func FuzzResolverNeverPanicsOrReturnsUnsupported(f *testing.F) {
	f.Add("en;q=0.1, fr;q=0")
	f.Add("*;q=0")
	f.Add("FR-ca, iw;q=0.5")
	resolver, err := NewResolver(LocalePolicy{Supported: []string{"en", "fr-CA", "he"}, Default: "en", DefaultOnMiss: true})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, header string) {
		resolution := resolver.Resolve(AcceptLanguage(SourceProtocol, header))
		if resolution.Locale != "" && !slicesContains(resolver.Supported(), resolution.Locale) {
			t.Fatalf("unsupported locale escaped: %+v", resolution)
		}
	})
}

func slicesContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
