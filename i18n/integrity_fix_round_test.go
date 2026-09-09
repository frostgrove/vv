package i18n

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestContractAndSnapshotDigestsFrameSchemaStructure(t *testing.T) {
	firstMessage := simpleMessage("collision", "fixed", ArgumentSpec{
		Name: "x", Type: TypeEnum, Values: []string{"v", "y", "text", "true", "false"},
	})
	secondMessage := simpleMessage("collision", "fixed",
		ArgumentSpec{Name: "x", Type: TypeEnum, Values: []string{"v"}},
		ArgumentSpec{Name: "y", Type: TypeText, Required: true},
	)
	first := mustSnapshot(t, testCatalog(firstMessage))
	second := mustSnapshot(t, testCatalog(secondMessage))
	firstContract, ok := first.ContractRef("app.collision")
	if !ok {
		t.Fatal("first contract is missing")
	}
	secondContract, ok := second.ContractRef("app.collision")
	if !ok {
		t.Fatal("second contract is missing")
	}
	if firstContract.Digest == secondContract.Digest || first.Digest() == second.Digest() {
		t.Fatalf("structurally different schemas share identity: contracts=%+v/%+v snapshots=%+v/%+v", firstContract, secondContract, first.Reference(), second.Reference())
	}
	controller, err := NewController(ControllerSpec{Initial: first})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Activate(controller.Current(), second); !errors.Is(err, ErrConflict) {
		t.Fatalf("same release revision with different content = %v", err)
	}
}

func TestSnapshotDigestSeparatesContentFromReleaseRevision(t *testing.T) {
	firstSpec := testCatalog(simpleMessage("same", "Same"))
	secondSpec := firstSpec
	secondSpec.Revision = "catalog-2"
	first := mustSnapshot(t, firstSpec)
	second := mustSnapshot(t, secondSpec)
	if first.Digest() != second.Digest() || first.Reference() == second.Reference() {
		t.Fatalf("release and content identities were not separated: %+v / %+v", first.Reference(), second.Reference())
	}
	firstMessage, err := first.Bind("app.same")
	if err != nil {
		t.Fatal(err)
	}
	secondMessage, err := second.Bind("app.same")
	if err != nil {
		t.Fatal(err)
	}
	firstKey, err := mustView(t, first, "en", "", "", PresentationDefault).RenderKey(firstMessage)
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := mustView(t, second, "en", "", "", PresentationDefault).RenderKey(secondMessage)
	if err != nil {
		t.Fatal(err)
	}
	if firstKey == secondKey {
		t.Fatal("release revision is absent from render cache identity")
	}
}

func TestResolverTriesAllowedSiblingAfterQZeroExclusion(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{Supported: []string{"en-US", "en-GB", "fr"}, Default: "fr", Mode: MatchBestFit})
	if err != nil {
		t.Fatal(err)
	}
	resolution := resolver.Resolve(AcceptLanguage(SourceProtocol, "en;q=1,en-US;q=0"))
	if resolution.Locale != "en-GB" || resolution.Outcome != OutcomeFallback || resolution.Reason != ReasonBestFit {
		t.Fatalf("allowed sibling resolution = %+v", resolution)
	}
}

func TestResolverTriesLowerPriorityRangeAfterEarlierChoiceExclusion(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{Supported: []string{"en", "fr"}, Default: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	resolution := resolver.Resolve(
		AcceptLanguage(SourceUser, "en;q=0"),
		AcceptLanguage(SourceProtocol, "en;q=1,fr;q=0.9"),
	)
	if resolution.Locale != "fr" || resolution.Outcome != OutcomeSuccess || resolution.Reason != ReasonExact || resolution.Source != SourceProtocol {
		t.Fatalf("resolution after accumulated exclusion = %+v", resolution)
	}
}

func TestResolverNeverRehabilitatesAnExcludedAncestor(t *testing.T) {
	tests := []struct {
		supported string
		header    string
	}{
		{supported: "en", header: "en;q=0,en-US;q=1"},
		{supported: "en-US", header: "en-US;q=0,en-US-x-private;q=1"},
	}
	for _, test := range tests {
		resolver, err := NewResolver(LocalePolicy{Supported: []string{test.supported}, Default: test.supported})
		if err != nil {
			t.Fatal(err)
		}
		resolution := resolver.Resolve(AcceptLanguage(SourceProtocol, test.header))
		if resolution.Matched() || resolution.Outcome != OutcomeNoMatch || resolution.Reason != ReasonExcluded {
			t.Fatalf("%q against %q = %+v", test.header, test.supported, resolution)
		}
	}
}

func TestResolverDoesNotDefaultAfterInvalidChoiceSource(t *testing.T) {
	resolver, err := NewResolver(LocalePolicy{Supported: []string{"en"}, Default: "en"})
	if err != nil {
		t.Fatal(err)
	}
	resolution := resolver.Resolve(Exact(ChoiceSource(255), "fr"))
	if resolution.Matched() || resolution.Outcome != OutcomeInvalid || resolution.Reason != ReasonMalformed {
		t.Fatalf("invalid source resolution = %+v", resolution)
	}
}

func TestResolverRejectsCyclesAcrossExplicitAndImplicitParents(t *testing.T) {
	_, err := NewResolver(LocalePolicy{
		Supported: []string{"en-US"}, Default: "en-US",
		Parents: []LocaleEdge{{Locale: "en", Parent: "en-US"}},
	})
	if !errors.Is(err, ErrInvalidLocale) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("effective fallback cycle = %v", err)
	}
}

func TestExplainSeparatesPreferenceResolutionFromTemplateFallback(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("explain", "English")))
	view, err := snapshot.ForContext(context.Background(),
		Exact(SourceUser, "de"),
		AcceptLanguage(SourceProtocol, "fr-CA-x-client;q=1"),
	)
	if err != nil {
		t.Fatal(err)
	}
	message, err := snapshot.Bind("app.explain")
	if err != nil {
		t.Fatal(err)
	}
	explanation, err := view.Explain(message)
	if err != nil {
		t.Fatal(err)
	}
	if explanation.ResolutionOutcome != OutcomeFallback || explanation.ResolutionReason != ReasonLookup || explanation.Source != SourceProtocol {
		t.Fatalf("resolution explanation = %+v", explanation)
	}
	if explanation.TemplateLocale != "en" || explanation.TemplateOutcome != OutcomeFallback || explanation.TemplateReason != ReasonLookup {
		t.Fatalf("template explanation = %+v", explanation)
	}
	want := []PreferenceStep{
		{Source: SourceUser, Outcome: OutcomeNoMatch, Reason: ReasonUnsupported},
		{Source: SourceProtocol, Outcome: OutcomeFallback, Reason: ReasonLookup},
	}
	if len(explanation.PreferenceSteps) != len(want) {
		t.Fatalf("preference steps = %+v", explanation.PreferenceSteps)
	}
	for index := range want {
		if explanation.PreferenceSteps[index] != want[index] {
			t.Fatalf("preference step %d = %+v, want %+v", index, explanation.PreferenceSteps[index], want[index])
		}
	}
}

func TestCompilerRejectsLocalCeilingsBeforeMessageCompilation(t *testing.T) {
	spec := testCatalog(simpleMessage("invalid", "{{"))
	ceiling := DefaultLimits()
	ceiling.MaxMessages = 1
	if _, err := (Compiler{CatalogLimits: ceiling}).Compile(spec); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("catalog ceiling did not win before compilation: %v", err)
	}
	if _, err := (Compiler{Limits: ArtifactLimits{MaxBytes: 512}}).Compile(spec); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("artifact byte ceiling did not win before compilation: %v", err)
	}
	if _, err := (Compiler{Limits: ArtifactLimits{MaxDepth: 1}}).Compile(spec); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("artifact depth ceiling did not win before compilation: %v", err)
	}
	if _, err := (Compiler{Limits: ArtifactLimits{MaxMembers: 1}}).Compile(spec); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("artifact member ceiling did not win before compilation: %v", err)
	}
}

func TestCatalogRejectsSilentlyDowngradedFormatterOptions(t *testing.T) {
	dateSpec := testCatalog(simpleMessage("date", "{$when :date calendar=buddhist}", ArgumentSpec{Name: "when", Type: TypeDate, Required: true}))
	dateSpec.Capabilities = []Capability{CapabilityDateTime}
	if _, err := New(dateSpec); err == nil || !strings.Contains(err.Error(), "calendar") || !strings.Contains(err.Error(), "resolves") {
		t.Fatalf("unsupported calendar = %v", err)
	}
	numberSpec := testCatalog(simpleMessage("number", "{$count :number numberingSystem=notreal}", ArgumentSpec{Name: "count", Type: TypeInteger, Required: true}))
	if _, err := New(numberSpec); err == nil || !strings.Contains(err.Error(), "numbering system") {
		t.Fatalf("unsupported numbering system = %v", err)
	}
}

func TestCatalogRejectsSilentlyDowngradedFormatterLocales(t *testing.T) {
	textOnly := CatalogSpec{
		Revision: "catalog-1", SourceLocale: "ast", DefaultLocale: "ast", DefaultTimeZone: "UTC",
		Supported: []string{"ast"}, Modules: []Module{{Name: "app", Messages: []MessageSpec{simpleMessage("text", "Testu")}}},
	}
	if _, err := New(reviewedCatalog(t, textOnly)); err != nil {
		t.Fatalf("text-only locale was unnecessarily rejected: %v", err)
	}
	number := textOnly
	number.Modules = []Module{{Name: "app", Messages: []MessageSpec{simpleMessage("number", "{$count}", ArgumentSpec{Name: "count", Type: TypeInteger, Required: true})}}}
	if _, err := New(reviewedCatalog(t, number)); err == nil || !strings.Contains(err.Error(), "resolves to") {
		t.Fatalf("downgraded number locale = %v", err)
	}
	calendar := CatalogSpec{
		Revision: "catalog-1", SourceLocale: "en-u-ca-buddhist", DefaultLocale: "en-u-ca-buddhist", DefaultTimeZone: "UTC",
		Supported: []string{"en-u-ca-buddhist"}, Capabilities: []Capability{CapabilityDateTime},
		Modules: []Module{{Name: "app", Messages: []MessageSpec{simpleMessage("date", "{$when}", ArgumentSpec{Name: "when", Type: TypeDate, Required: true})}}},
	}
	if _, err := New(reviewedCatalog(t, calendar)); err == nil || !strings.Contains(err.Error(), "calendar") {
		t.Fatalf("downgraded calendar extension = %v", err)
	}
	numbering := CatalogSpec{
		Revision: "catalog-1", SourceLocale: "en-u-nu-notreal", DefaultLocale: "en-u-nu-notreal", DefaultTimeZone: "UTC",
		Supported: []string{"en-u-nu-notreal"},
		Modules:   []Module{{Name: "app", Messages: []MessageSpec{simpleMessage("number", "{$count}", ArgumentSpec{Name: "count", Type: TypeInteger, Required: true})}}},
	}
	if _, err := New(reviewedCatalog(t, numbering)); err == nil || !strings.Contains(err.Error(), "numbering system") {
		t.Fatalf("downgraded numbering-system extension = %v", err)
	}
}

func TestViewValidatesCustomFormattingLocaleForReachableTemplates(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("number", "{$count}", ArgumentSpec{Name: "count", Type: TypeInteger, Required: true})))
	resolution := snapshot.Resolve(Exact(SourceExplicit, "en"))
	if _, err := snapshot.View(ViewSpec{Resolution: resolution, FormattingLocale: "ast"}); err == nil || !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("unsupported custom formatting locale = %v", err)
	}
	if _, err := snapshot.View(ViewSpec{Resolution: resolution, FormattingLocale: "fr-CA"}); err != nil {
		t.Fatalf("supported custom formatting locale = %v", err)
	}
	textSnapshot := mustSnapshot(t, testCatalog(simpleMessage("text", "Text")))
	textResolution := textSnapshot.Resolve(Exact(SourceExplicit, "en"))
	if _, err := textSnapshot.View(ViewSpec{Resolution: textResolution, FormattingLocale: "ast"}); err != nil {
		t.Fatalf("text-only custom formatting locale = %v", err)
	}
}

func TestCatalogPreflightStopsAfterTopLevelCardinalityFailure(t *testing.T) {
	limits := DefaultLimits()
	spec := CatalogSpec{Supported: make([]string, limits.MaxLocales+1)}
	spec.Supported[0] = strings.Repeat("x", limits.Locale.MaxTagBytes+1)
	err := preflightCatalog(spec, limits)
	if err == nil || strings.Contains(err.Error(), "supported[0]") {
		t.Fatalf("cardinality preflight did not stop before elements: %v", err)
	}
}

func TestLoadFSChecksContextAndLimitsBeforeOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	filesystem := &controlledArtifactFS{}
	if _, err := LoadFS(ctx, filesystem, "catalog.json"); !errors.Is(err, context.Canceled) || filesystem.opens != 0 {
		t.Fatalf("pre-canceled LoadFS = %v, opens=%d", err, filesystem.opens)
	}
	if _, err := (Loader{Limits: ArtifactLimits{MaxBytes: -1}}).LoadFS(context.Background(), filesystem, "catalog.json"); !errors.Is(err, ErrLimitExceeded) || filesystem.opens != 0 {
		t.Fatalf("invalid-limit LoadFS = %v, opens=%d", err, filesystem.opens)
	}
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := LoadFS(deadline, filesystem, "catalog.json"); !errors.Is(err, context.DeadlineExceeded) || filesystem.opens != 0 {
		t.Fatalf("expired LoadFS = %v, opens=%d", err, filesystem.opens)
	}
}
