package i18n

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func currentTranslation(t testing.TB, module string, message MessageSpec, locale, text string) Translation {
	t.Helper()
	digest, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", module, message)
	if err != nil {
		t.Fatal(err)
	}
	reviewDigest, err := ExpectedReviewDigest(digest, locale, text)
	if err != nil {
		t.Fatal(err)
	}
	return Translation{
		Locale: locale, Text: text, Review: ReviewApproved,
		ContractRevision: message.Revision, SourceDigest: digest, ReviewDigest: reviewDigest,
	}
}

func checkFixture(t testing.TB) CatalogSpec {
	t.Helper()
	message := simpleMessage("notice", "Notice {$name}", ArgumentSpec{Name: "name", Type: TypeText, Required: true})
	message.Override = OverrideApplication
	message.Public = true
	message.Translations = []Translation{currentTranslation(t, "app", message, "ru", "Уведомление {$name}")}
	return CatalogSpec{
		Revision: "check-1", SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"en", "ru"}, Required: []string{"ru"},
		Modules: []Module{{Name: "app", Messages: []MessageSpec{message}}},
	}
}

func TestCheckAcceptsCurrentRequiredLocalesAndReturnsStableCoverage(t *testing.T) {
	spec := checkFixture(t)
	usage := completeUsage([]Key{"app.notice"}, nil)
	report := Check(spec, CheckPolicy{Usage: usage})
	if !report.OK() || report.Err() != nil || report.Error() != "" || report.SourceDigest == "" {
		t.Fatalf("report = %+v, error = %v", report, report.Err())
	}
	if len(report.Findings) != 0 || len(report.Coverage) != 2 {
		t.Fatalf("findings/coverage = (%+v, %+v)", report.Findings, report.Coverage)
	}
	for _, coverage := range report.Coverage {
		if coverage.Total != 1 || coverage.Approved != 1 || !coverage.Complete() {
			t.Fatalf("coverage = %+v", coverage)
		}
	}
	second := Check(spec, CheckPolicy{Usage: usage})
	if !reflect.DeepEqual(report, second) {
		t.Fatalf("reports are not deterministic:\n%+v\n%+v", report, second)
	}
}

func TestCheckClassifiesReviewDebtAndRequiredVersusOptionalLocales(t *testing.T) {
	message := simpleMessage("status", "Status")
	digest, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "app", message)
	if err != nil {
		t.Fatal(err)
	}
	message.Translations = []Translation{
		{Locale: "ru", Text: "Статус", Review: ReviewApproved, ContractRevision: "old", SourceDigest: digest},
		{Locale: "fr", Text: "Statut", Review: ReviewRequired, ContractRevision: message.Revision, SourceDigest: digest},
		{Locale: "de", Text: "Status", Review: ReviewRejected, ContractRevision: message.Revision, SourceDigest: digest},
	}
	spec := CatalogSpec{
		Revision: "check-debt", SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"en", "ru", "fr", "de", "es"}, Required: []string{"ru"},
		Modules: []Module{{Name: "app", Messages: []MessageSpec{message}}},
	}
	report := Check(spec, CheckPolicy{})
	if report.OK() || !errors.Is(report.Err(), ErrCheckFailed) {
		t.Fatalf("report unexpectedly passed: %+v", report)
	}
	assertCheckFinding(t, report, CheckStale, SeverityError, "app.status", "ru")
	assertCheckFinding(t, report, CheckReviewRequired, SeverityWarning, "app.status", "fr")
	assertCheckFinding(t, report, CheckRejected, SeverityWarning, "app.status", "de")
	assertCheckFinding(t, report, CheckMissing, SeverityWarning, "app.status", "es")
	for _, coverage := range report.Coverage {
		switch coverage.Locale {
		case "en":
			if coverage.Approved != 1 {
				t.Fatalf("en coverage = %+v", coverage)
			}
		case "ru":
			if !coverage.Required || coverage.Stale != 1 {
				t.Fatalf("ru coverage = %+v", coverage)
			}
		case "fr":
			if coverage.ReviewRequired != 1 {
				t.Fatalf("fr coverage = %+v", coverage)
			}
		case "de":
			if coverage.Rejected != 1 {
				t.Fatalf("de coverage = %+v", coverage)
			}
		case "es":
			if coverage.Missing != 1 {
				t.Fatalf("es coverage = %+v", coverage)
			}
		}
	}
	strict := Check(spec, CheckPolicy{StrictOptional: true})
	assertCheckFinding(t, strict, CheckMissing, SeverityError, "app.status", "es")
}

func TestCheckValidatesStaleAndUnreviewedTemplatesStructurally(t *testing.T) {
	message := simpleMessage("broken", "Hello {$name}", ArgumentSpec{Name: "name", Type: TypeText, Required: true})
	digest, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "app", message)
	if err != nil {
		t.Fatal(err)
	}
	message.Translations = []Translation{{
		Locale: "ru", Text: "Привет {$undeclared}", Review: ReviewRequired,
		ContractRevision: "old", SourceDigest: digest,
	}}
	spec := CatalogSpec{
		Revision: "check-invalid", SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"en", "ru"}, Required: []string{"ru"},
		Modules: []Module{{Name: "app", Messages: []MessageSpec{message}}},
	}
	report := Check(spec, CheckPolicy{})
	assertCheckFinding(t, report, CheckReviewRequired, SeverityError, "app.broken", "ru")
	assertCheckFinding(t, report, CheckInvalid, SeverityError, "", "")
	coverage, ok := findCoverage(report.Coverage, "ru")
	if !ok || coverage.Invalid != 1 || coverage.ReviewRequired != 0 {
		t.Fatalf("ru coverage = %+v", coverage)
	}
	if message.Translations[0].Review != ReviewRequired || message.Translations[0].ContractRevision != "old" {
		t.Fatal("Check mutated its input")
	}
}

func TestCheckUsageSupportsStaticKeysBoundedDynamicDomainsAndIncompleteVisibility(t *testing.T) {
	notice := simpleMessage("notice", "Notice")
	dynamicOne := simpleMessage("dynamic.one", "One")
	dynamicTwo := simpleMessage("dynamic.two", "Two")
	unused := simpleMessage("unused", "Unused")
	spec := CatalogSpec{
		Revision: "usage-1", SourceLocale: "en", DefaultLocale: "en", Supported: []string{"en"},
		Modules: []Module{{Name: "app", Messages: []MessageSpec{unused, dynamicTwo, notice, dynamicOne}}},
	}
	usage := completeUsage([]Key{"app.notice", "app.missing"}, []DynamicUsage{{Domain: "app", Prefix: "dynamic."}})
	report := Check(spec, CheckPolicy{Usage: usage})
	assertCheckFinding(t, report, CheckMissing, SeverityError, "app.missing", "")
	assertCheckFinding(t, report, CheckUnused, SeverityWarning, "app.unused", "")
	for _, finding := range report.Findings {
		if finding.Status == CheckUnused && strings.HasPrefix(string(finding.Key), "app.dynamic.") {
			t.Fatalf("dynamic usage was reported unused: %+v", finding)
		}
	}
	incomplete := Check(spec, CheckPolicy{Usage: UsageManifest{Keys: []Key{"app.notice"}, Complete: false}})
	for _, finding := range incomplete.Findings {
		if finding.Status == CheckUnused {
			t.Fatalf("incomplete manifest asserted unused: %+v", finding)
		}
	}
	invalid := Check(spec, CheckPolicy{Usage: UsageManifest{Dynamic: []DynamicUsage{{Domain: "missing", Prefix: "x"}}}})
	assertCheckFinding(t, invalid, CheckMissing, SeverityError, "", "")
}

func TestCheckUsageAcceptsUnambiguousSourceOccurrencesWithoutChangingCoverage(t *testing.T) {
	spec := checkFixture(t)
	usage := UsageManifest{
		Keys:    []Key{"app.notice"},
		Dynamic: []DynamicUsage{{Domain: "app"}},
		Occurrences: []UsageOccurrence{
			{Key: "app.notice", Path: "example.test/app/use.go", Line: 12, Column: 8},
			{Domain: "app", Path: "example.test/app/use.go", Line: 13, Column: 8},
		},
		GoScope:  testGoUsageScope(),
		Complete: true,
	}
	usage.ManifestDigest = ExpectedUsageManifestDigest(usage)
	report := Check(spec, CheckPolicy{Usage: usage})
	if !report.OK() || len(report.Findings) != 0 {
		t.Fatalf("valid usage occurrences failed: %+v", report.Findings)
	}
	if usage.Occurrences[0].Path != "example.test/app/use.go" || usage.Occurrences[1].Line != 13 {
		t.Fatalf("Check mutated usage occurrences: %+v", usage.Occurrences)
	}
}

func TestCheckUsageRequiresCompleteScopeAndReverseOccurrenceEvidence(t *testing.T) {
	spec := checkFixture(t)
	withoutScope := Check(spec, CheckPolicy{Usage: UsageManifest{Complete: true}})
	assertCheckFinding(t, withoutScope, CheckInvalid, SeverityError, "", "")
	for _, finding := range withoutScope.Findings {
		if finding.Status == CheckUnused {
			t.Fatalf("scope-free manifest asserted non-use: %+v", withoutScope.Findings)
		}
	}
	withoutOccurrenceUsage := sealTestUsage(UsageManifest{
		Keys: []Key{"app.notice"}, GoScope: testGoUsageScope(), Complete: true,
	})
	withoutOccurrence := Check(spec, CheckPolicy{Usage: withoutOccurrenceUsage})
	assertCheckFinding(t, withoutOccurrence, CheckInvalid, SeverityError, "app.notice", "")
	for _, finding := range withoutOccurrence.Findings {
		if finding.Status == CheckUnused {
			t.Fatalf("manifest without reverse evidence asserted non-use: %+v", withoutOccurrence.Findings)
		}
	}
}

func TestCheckUsageAcceptsIncompleteEmptySourceLedger(t *testing.T) {
	scope := testGoUsageScope()
	scope.Files = nil
	scope.SelectedFiles = 0
	scope.SourceDigest = ExpectedUsageSourceDigest(*scope)
	usage := UsageManifest{GoScope: scope}
	usage.ManifestDigest = ExpectedUsageManifestDigest(usage)
	report := Check(checkFixture(t), CheckPolicy{Usage: usage})
	if !report.OK() || len(report.Findings) != 0 {
		t.Fatalf("incomplete empty usage failed: %+v", report.Findings)
	}
}

func TestCheckUsageRejectsSelfConsistentForgedV4Provenance(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(*UsageManifest)
		corruptSource   bool
		corruptManifest bool
	}{
		{name: "unsupported target", mutate: func(value *UsageManifest) {
			value.GoScope.GOOS = "unknown"
			value.GoScope.Environment[5].Value = "unknown"
		}},
		{name: "unsupported compiler", mutate: func(value *UsageManifest) { value.GoScope.Compiler = "custom" }},
		{name: "absolute root", mutate: func(value *UsageManifest) {
			value.GoScope.Roots[0].Path = "/workspace"
			value.GoScope.Files[0].Root = "/workspace"
			value.GoScope.Files[0].LogicalPath = "/workspace/use.go"
			value.Occurrences[0].Path = "/workspace/use.go"
		}},
		{name: "repeated root", mutate: func(value *UsageManifest) { value.GoScope.Roots = append(value.GoScope.Roots, value.GoScope.Roots[0]) }},
		{name: "repeated file", mutate: func(value *UsageManifest) {
			value.GoScope.Files = append(value.GoScope.Files, value.GoScope.Files[0])
			value.GoScope.SelectedFiles = 2
		}},
		{name: "unsorted files", mutate: func(value *UsageManifest) {
			value.GoScope.Files = append(value.GoScope.Files, UsageFile{Root: "example.test/app", Path: "before.go", LogicalPath: "example.test/app/before.go", SHA256: strings.Repeat("3", 64), Selected: false})
			value.GoScope.ExcludedFiles = 1
		}},
		{name: "logical path mismatch", mutate: func(value *UsageManifest) { value.GoScope.Files[0].LogicalPath = "example.test/app/other.go" }},
		{name: "invalid file digest", mutate: func(value *UsageManifest) { value.GoScope.Files[0].SHA256 = "0" }},
		{name: "repeated metadata", mutate: func(value *UsageManifest) {
			value.GoScope.Metadata = append(value.GoScope.Metadata, value.GoScope.Metadata[0])
		}},
		{name: "environment contradiction", mutate: func(value *UsageManifest) { value.GoScope.Environment[1].Value = "arm64" }},
		{name: "unsorted build tags", mutate: func(value *UsageManifest) { value.GoScope.BuildTags = []string{"z", "a"} }},
		{name: "repeated build tag", mutate: func(value *UsageManifest) { value.GoScope.BuildTags = []string{"edge", "edge"} }},
		{name: "occurrence on excluded file", mutate: func(value *UsageManifest) {
			value.GoScope.Files[0].Selected = false
			value.GoScope.SelectedFiles = 0
			value.GoScope.ExcludedFiles = 1
		}},
		{name: "source digest mismatch", corruptSource: true},
		{name: "manifest digest mismatch", corruptManifest: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage := cloneTestUsage(completeUsage([]Key{"app.notice"}, nil))
			if test.mutate != nil {
				test.mutate(&usage)
			}
			usage.GoScope.SourceDigest = ExpectedUsageSourceDigest(*usage.GoScope)
			if test.corruptSource {
				usage.GoScope.SourceDigest = strings.Repeat("f", 64)
			}
			usage.ManifestDigest = ExpectedUsageManifestDigest(usage)
			if test.corruptManifest {
				usage.ManifestDigest = strings.Repeat("e", 64)
			}
			report := Check(checkFixture(t), CheckPolicy{Usage: usage})
			assertCheckFinding(t, report, CheckInvalid, SeverityError, "", "")
			for _, finding := range report.Findings {
				if finding.Status == CheckUnused {
					t.Fatalf("forged usage asserted non-use: %+v", report.Findings)
				}
			}
		})
	}
}

func cloneTestUsage(value UsageManifest) UsageManifest {
	cloned := value
	cloned.Keys = slices.Clone(value.Keys)
	cloned.Dynamic = slices.Clone(value.Dynamic)
	cloned.Occurrences = slices.Clone(value.Occurrences)
	if value.GoScope != nil {
		scope := *value.GoScope
		scope.BuildTags = slices.Clone(value.GoScope.BuildTags)
		scope.ToolTags = slices.Clone(value.GoScope.ToolTags)
		scope.ReleaseTags = slices.Clone(value.GoScope.ReleaseTags)
		scope.Environment = slices.Clone(value.GoScope.Environment)
		scope.Roots = slices.Clone(value.GoScope.Roots)
		scope.Files = slices.Clone(value.GoScope.Files)
		scope.Metadata = slices.Clone(value.GoScope.Metadata)
		cloned.GoScope = &scope
	}
	return cloned
}

func TestCheckUsageRejectsAmbiguousOrUnverifiableSourceOccurrences(t *testing.T) {
	tests := map[string]UsageOccurrence{
		"empty identity":            {Path: "example.test/app/use.go", Line: 1, Column: 1},
		"two identities":            {Key: "app.notice", Domain: "app", Path: "example.test/app/use.go", Line: 1, Column: 1},
		"orphan prefix":             {Prefix: "dynamic.", Path: "example.test/app/use.go", Line: 1, Column: 1},
		"missing exact aggregate":   {Key: "app.other", Path: "example.test/app/use.go", Line: 1, Column: 1},
		"missing dynamic aggregate": {Domain: "other", Prefix: "dynamic.", Path: "example.test/app/use.go", Line: 1, Column: 1},
		"absolute path":             {Key: "app.notice", Path: "/workspace/use.go", Line: 1, Column: 1},
		"drive path":                {Key: "app.notice", Path: "C:/workspace/use.go", Line: 1, Column: 1},
		"colon path":                {Key: "app.notice", Path: "example.test/app:spoof/use.go", Line: 1, Column: 1},
		"unclean path":              {Key: "app.notice", Path: "example.test/app/../use.go", Line: 1, Column: 1},
		"host separator":            {Key: "app.notice", Path: `example.test\app\use.go`, Line: 1, Column: 1},
		"non Go path":               {Key: "app.notice", Path: "example.test/app/use.txt", Line: 1, Column: 1},
		"bidi path":                 {Key: "app.notice", Path: "example.test/app/\u202espoof.go", Line: 1, Column: 1},
		"zero line":                 {Key: "app.notice", Path: "example.test/app/use.go", Column: 1},
		"zero column":               {Key: "app.notice", Path: "example.test/app/use.go", Line: 1},
		"line overflow":             {Key: "app.notice", Path: "example.test/app/use.go", Line: maximumUsageCoordinate + 1, Column: 1},
		"column overflow":           {Key: "app.notice", Path: "example.test/app/use.go", Line: 1, Column: maximumUsageCoordinate + 1},
	}
	for name, occurrence := range tests {
		t.Run(name, func(t *testing.T) {
			usage := UsageManifest{
				Keys: []Key{"app.notice"}, Dynamic: []DynamicUsage{{Domain: "app", Prefix: "dynamic."}},
				Occurrences: []UsageOccurrence{occurrence}, GoScope: testGoUsageScope(), Complete: true,
			}
			usage.ManifestDigest = ExpectedUsageManifestDigest(usage)
			report := Check(checkFixture(t), CheckPolicy{Usage: usage})
			assertCheckFinding(t, report, CheckInvalid, SeverityError, "", "")
			for _, finding := range report.Findings {
				if finding.Status == CheckUnused {
					t.Fatalf("invalid provenance asserted non-use: %+v", report.Findings)
				}
			}
		})
	}

	usage := UsageManifest{
		Keys: []Key{"app.notice"},
		Occurrences: []UsageOccurrence{
			{Key: "app.notice", Path: "example.test/app/use.go", Line: 1, Column: 1},
			{Key: "app.notice", Path: "example.test/app/use.go", Line: 1, Column: 1},
		},
	}
	repeated := Check(checkFixture(t), CheckPolicy{Usage: usage})
	assertCheckFinding(t, repeated, CheckInvalid, SeverityError, "", "")
}

func TestCheckUsageBoundsOccurrenceItemsAndMaterial(t *testing.T) {
	limits := DefaultUsageLimits()
	limits.MaxOccurrences = 1
	collector := checkCollector{maximum: 16, ctx: context.Background()}
	_, valid := checkUsage(nil, UsageManifest{
		Keys: []Key{"app.one"}, Occurrences: []UsageOccurrence{
			{Key: "app.one", Path: "one.go", Line: 1, Column: 1},
			{Key: "app.one", Path: "two.go", Line: 1, Column: 1},
		},
	}, limits, &collector)
	if valid || len(collector.findings) != 1 || collector.findings[0].Detail != "usage manifest exceeds configured item bounds" {
		t.Fatalf("occurrence item bound = valid %v, findings %+v", valid, collector.findings)
	}

	limits = DefaultUsageLimits()
	limits.MaxMaterialBytes = len("app.one") + len("one.go") - 1
	collector = checkCollector{maximum: 16, ctx: context.Background()}
	_, valid = checkUsage(nil, UsageManifest{
		Keys: []Key{"app.one"}, Occurrences: []UsageOccurrence{{Key: "app.one", Path: "one.go", Line: 1, Column: 1}},
	}, limits, &collector)
	if valid || len(collector.findings) == 0 || collector.findings[len(collector.findings)-1].Detail != "usage manifest exceeds configured byte bounds" {
		t.Fatalf("occurrence byte bound = valid %v, findings %+v", valid, collector.findings)
	}
}

func TestUsageLimitsHaveStableDefaultsAndCannotExceedHardBounds(t *testing.T) {
	want := UsageLimits{
		MaxKeys: 1 << 18, MaxDynamic: 1 << 18, MaxOccurrences: 1 << 18,
		MaxRoots: 1024, MaxFiles: 100000, MaxMetadata: 100000,
		MaxTags: 256, MaxEnvironment: 256, MaxStringBytes: 4 << 20, MaxMaterialBytes: 64 << 20,
	}
	if got := DefaultUsageLimits(); got != want {
		t.Fatalf("default usage limits = %+v, want %+v", got, want)
	}
	for _, limit := range []int{-1, want.MaxKeys + 1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			configured := UsageLimits{MaxKeys: limit}
			report := Check(checkFixture(t), CheckPolicy{UsageLimits: configured})
			if len(report.Findings) != 1 || report.Findings[0].Path != "policy.usage_limits" || report.Findings[0].Status != CheckInvalid {
				t.Fatalf("invalid usage limit report = %+v", report)
			}
		})
	}
}

func TestUsageDigestContextMatchesStableWrappersAndHonorsCancellation(t *testing.T) {
	scope := testGoUsageScope()
	manifest := completeUsage([]Key{"app.notice"}, nil)
	sourceDigest, err := ExpectedUsageSourceDigestContext(context.Background(), *scope)
	if err != nil || sourceDigest != ExpectedUsageSourceDigest(*scope) {
		t.Fatalf("context source digest = %q, %v", sourceDigest, err)
	}
	manifestDigest, err := ExpectedUsageManifestDigestContext(context.Background(), manifest)
	if err != nil || manifestDigest != ExpectedUsageManifestDigest(manifest) {
		t.Fatalf("context manifest digest = %q, %v", manifestDigest, err)
	}
	if _, err := ExpectedUsageSourceDigestContext(nil, *scope); err == nil {
		t.Fatal("nil source digest context was accepted")
	}
	if _, err := ExpectedUsageManifestDigestContext(nil, manifest); err == nil {
		t.Fatal("nil manifest digest context was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ExpectedUsageSourceDigestContext(ctx, *scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled source digest = %v", err)
	}
	if _, err := ExpectedUsageManifestDigestContext(ctx, manifest); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled manifest digest = %v", err)
	}
}

func TestCheckUsageLimitsAreIndependentFromCatalogLimits(t *testing.T) {
	spec := checkFixture(t)
	probe, err := New(spec)
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	limits.MaxMessages = 1
	limits.MaxCatalogItems = snapshotCatalogItems(probe)
	limits.MaxCatalogBytes = snapshotCatalogBytes(probe)
	spec.Limits = limits

	usage := completeUsage([]Key{"app.notice"}, nil)
	path := "example.test/app/generated/" + strings.Repeat("x", 512) + ".go"
	usage.GoScope.Files[0].Path = strings.TrimPrefix(path, usage.GoScope.Files[0].Root+"/")
	usage.GoScope.Files[0].LogicalPath = path
	usage.Occurrences = make([]UsageOccurrence, 64)
	for index := range usage.Occurrences {
		usage.Occurrences[index] = UsageOccurrence{Key: "app.notice", Path: path, Line: index + 1, Column: 1}
	}
	usage.GoScope.SourceDigest = ExpectedUsageSourceDigest(*usage.GoScope)
	usage.ManifestDigest = ExpectedUsageManifestDigest(usage)
	if len(path)*len(usage.Occurrences) <= limits.MaxCatalogBytes {
		t.Fatalf("usage fixture does not exceed catalog byte policy: %d <= %d", len(path)*len(usage.Occurrences), limits.MaxCatalogBytes)
	}
	report := Check(spec, CheckPolicy{Usage: usage})
	if !report.OK() {
		t.Fatalf("catalog limits rejected independent usage evidence: %+v", report.Findings)
	}

	usageLimits := UsageLimits{MaxOccurrences: 1}
	narrow := Check(spec, CheckPolicy{Usage: usage, UsageLimits: usageLimits})
	assertCheckFinding(t, narrow, CheckInvalid, SeverityError, "", "")
	for _, finding := range narrow.Findings {
		if finding.Path == "usage" && finding.Detail == "usage manifest exceeds configured item bounds" {
			return
		}
	}
	t.Fatalf("narrow usage limit was not enforced: %+v", narrow.Findings)
}

func TestCheckTreatsLegacyUsageScopeAsNonAuthoritative(t *testing.T) {
	scope := testGoUsageScope()
	scope.Analyzer = GoUsageAnalyzerV1
	usage := UsageManifest{
		Keys:        []Key{"app.notice"},
		Occurrences: []UsageOccurrence{{Key: "app.notice", Path: "legacy/use.go", Line: 1, Column: 1}},
		GoScope:     scope,
		Complete:    true,
	}
	report := Check(checkFixture(t), CheckPolicy{Usage: usage})
	if !report.OK() {
		t.Fatalf("legacy positive usage failed: %+v", report.Findings)
	}
	for _, finding := range report.Findings {
		if strings.HasPrefix(finding.Path, "usage.") || finding.Status == CheckUnused {
			t.Fatalf("legacy scope became v4 authority: %+v", report.Findings)
		}
	}
}

func TestCheckApplicationOverrideCanCompleteARequiredLocale(t *testing.T) {
	message := simpleMessage("override", "Base")
	message.Override = OverrideApplication
	digest, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "app", message)
	if err != nil {
		t.Fatal(err)
	}
	reviewDigest, err := ExpectedReviewDigest(digest, "ru", "Замена")
	if err != nil {
		t.Fatal(err)
	}
	spec := CatalogSpec{
		Revision: "override-1", SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"en", "ru"}, Required: []string{"ru"},
		Modules: []Module{{Name: "app", Messages: []MessageSpec{message}}},
		Overrides: []Override{{
			Key: "app.override", Locale: "ru", Text: "Замена", Review: ReviewApproved,
			ContractRevision: message.Revision, SourceDigest: digest, ReviewDigest: reviewDigest,
		}},
	}
	report := Check(spec, CheckPolicy{})
	if !report.OK() {
		t.Fatalf("valid required override did not pass: %+v", report.Findings)
	}
	coverage, ok := findCoverage(report.Coverage, "ru")
	if !ok || coverage.Approved != 1 || coverage.Missing != 0 {
		t.Fatalf("ru coverage = %+v", coverage)
	}
}

func TestCheckValidatesOverridesEvenWhenAnotherTranslationBlocksBaseConstruction(t *testing.T) {
	message := simpleMessage("isolated", "Base {$name}", ArgumentSpec{Name: "name", Type: TypeText, Required: true})
	message.Override = OverrideApplication
	digest, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "app", message)
	if err != nil {
		t.Fatal(err)
	}
	message.Translations = []Translation{{
		Locale: "fr", Text: "Erreur {$unknown}", Review: ReviewRequired,
		ContractRevision: message.Revision, SourceDigest: digest,
	}}
	spec := CatalogSpec{
		Revision: "isolated-1", SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"en", "fr", "ru"}, Required: []string{"ru"},
		Modules: []Module{{Name: "app", Messages: []MessageSpec{message}}},
		Overrides: []Override{{
			Key: "app.isolated", Locale: "ru", Text: "Ошибка {$also_unknown}", Review: ReviewApproved,
			ContractRevision: message.Revision, SourceDigest: digest,
		}},
	}
	report := Check(spec, CheckPolicy{})
	foundTranslation := false
	foundOverride := false
	for _, finding := range report.Findings {
		if finding.Status != CheckInvalid {
			continue
		}
		foundTranslation = foundTranslation || strings.HasPrefix(finding.Path, "modules[0].messages[0].translations[0]")
		foundOverride = foundOverride || strings.HasPrefix(finding.Path, "overrides[0].text")
	}
	if !foundTranslation || !foundOverride {
		t.Fatalf("isolated structural findings = %+v", report.Findings)
	}
	coverage, ok := findCoverage(report.Coverage, "ru")
	if !ok || coverage.Invalid != 1 {
		t.Fatalf("ru coverage = %+v", coverage)
	}
}

func TestCheckSourceDigestAndStalenessReactToSourceMutation(t *testing.T) {
	spec := checkFixture(t)
	original := Check(spec, CheckPolicy{})
	mutated := cloneCatalogSpec(spec)
	mutated.Modules[0].Messages[0].Source = "Changed {$name}"
	changed := Check(mutated, CheckPolicy{})
	if original.SourceDigest == "" || changed.SourceDigest == "" || original.SourceDigest == changed.SourceDigest {
		t.Fatalf("source identities = %q and %q", original.SourceDigest, changed.SourceDigest)
	}
	assertCheckFinding(t, changed, CheckStale, SeverityError, "app.notice", "ru")
}

func TestCheckFindingsAreSortedAndBoundedWithoutVacuousCounts(t *testing.T) {
	message := simpleMessage("bounded", "Bounded")
	spec := CatalogSpec{
		Revision: "bounded-1", SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"en", "de", "es", "fr", "ru"},
		Modules:   []Module{{Name: "app", Messages: []MessageSpec{message}}},
	}
	report := Check(spec, CheckPolicy{MaxFindings: 2, Usage: completeUsage([]Key{"app.nope"}, nil)})
	if !report.Truncated || len(report.Findings) != 2 || report.Errors+report.Warnings <= len(report.Findings) {
		t.Fatalf("bounded report = %+v", report)
	}
	if !slices.IsSortedFunc(report.Findings, compareFinding) {
		t.Fatalf("findings are not sorted: %+v", report.Findings)
	}
	invalidPolicy := Check(spec, CheckPolicy{MaxFindings: 65537})
	if invalidPolicy.OK() || len(invalidPolicy.Findings) != 1 || invalidPolicy.Findings[0].Path != "policy.max_findings" {
		t.Fatalf("invalid policy report = %+v", invalidPolicy)
	}
}

func TestCheckBoundedReportAlwaysRetainsAnErrorAndBoundsIdentityFields(t *testing.T) {
	message := simpleMessage("bounded", "Bounded")
	spec := CatalogSpec{
		Revision: "bounded-1", SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"en", "fr"}, Modules: []Module{{Name: "app", Messages: []MessageSpec{message}}},
	}
	huge := Key(strings.Repeat("x", 1<<20))
	report := Check(spec, CheckPolicy{MaxFindings: 1, Usage: completeUsage([]Key{huge}, nil)})
	if report.Errors == 0 || len(report.Findings) != 1 || report.Findings[0].Severity != SeverityError {
		t.Fatalf("bounded error report = %+v", report)
	}
	if len(report.Findings[0].Key) > 1024 || len(report.Findings[0].Locale) > 1024 {
		t.Fatalf("unbounded finding identity = (%d, %d)", len(report.Findings[0].Key), len(report.Findings[0].Locale))
	}
}

func TestCheckCollectorReplacesRetainedWarningsInTheirOriginalOrder(t *testing.T) {
	collector := checkCollector{maximum: 3, ctx: context.Background()}
	collector.add(Finding{Status: CheckMissing, Severity: SeverityWarning, Path: "warning.0"})
	collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "error.0"})
	collector.add(Finding{Status: CheckMissing, Severity: SeverityWarning, Path: "warning.1"})
	collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "error.1"})
	collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "error.2"})
	collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "error.discarded"})
	report := collector.report(nil, "")
	if report.Errors != 4 || report.Warnings != 2 || len(report.Findings) != 3 || !report.Truncated {
		t.Fatalf("replacement report = %+v", report)
	}
	for _, path := range []string{"error.0", "error.1", "error.2"} {
		if _, found := slices.BinarySearchFunc(report.Findings, path, func(finding Finding, path string) int {
			return strings.Compare(finding.Path, path)
		}); !found {
			t.Fatalf("retained errors = %+v; missing %q", report.Findings, path)
		}
	}
}

func TestCheckSparseCoverageKeepsExactCountsAndTheFirstFinding(t *testing.T) {
	const messageCount = 4096
	const localeCount = 128
	spec := scaledCheckSpec(t, messageCount, localeCount, false)
	report := Check(spec, CheckPolicy{MaxFindings: 1})
	wantWarnings := messageCount * (localeCount - 1)
	if report.Errors != 0 || report.Warnings != wantWarnings || !report.Truncated {
		t.Fatalf("bounded sparse counts = (%d errors, %d warnings, truncated %t), want (0, %d, true); findings = %+v", report.Errors, report.Warnings, report.Truncated, wantWarnings, report.Findings)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("retained findings = %d, want 1", len(report.Findings))
	}
	first := report.Findings[0]
	if first.Status != CheckMissing || first.Severity != SeverityWarning || first.Key != "app.message.m0000" || first.Locale != "en-x-l000" {
		t.Fatalf("first retained finding = %+v", first)
	}
	if len(report.Coverage) != localeCount {
		t.Fatalf("coverage locales = %d, want %d", len(report.Coverage), localeCount)
	}
	for _, coverage := range report.Coverage {
		if coverage.Total != messageCount {
			t.Fatalf("coverage total for %q = %d, want %d", coverage.Locale, coverage.Total, messageCount)
		}
		if coverage.Locale == "en" {
			if coverage.Approved != messageCount || coverage.Missing != 0 {
				t.Fatalf("source coverage = %+v", coverage)
			}
		} else if coverage.Missing != messageCount || coverage.Approved != 0 {
			t.Fatalf("missing coverage = %+v", coverage)
		}
	}
}

func TestCheckSparseCoverageDoesNotAllocatePerMissingCell(t *testing.T) {
	const messageCount = 4096
	spec := scaledCheckSpec(t, messageCount, 128, false)
	spec.Limits = DefaultLimits()
	messages := make([]checkedMessage, len(spec.Modules[0].Messages))
	for index, message := range spec.Modules[0].Messages {
		messages[index] = checkedMessage{
			moduleIndex: 0, messageIndex: index, module: "app",
			key: Qualify("app", message.ID), message: message,
		}
	}
	var coverage []LocaleCoverage
	allocations := testing.AllocsPerRun(3, func() {
		collector := checkCollector{maximum: 1, ctx: context.Background()}
		coverage = buildCoverage(spec, messages, CheckPolicy{MaxFindings: 1}, checkInvalidPathIndex{}, &collector)
	})
	if len(coverage) != 128 || allocations > messageCount {
		t.Fatalf("sparse coverage = %d locales and %.0f allocations; allocations must follow inputs, not %d missing cells", len(coverage), allocations, messageCount*127)
	}
}

func TestCheckSparseCoverageReplacesAnEarlierWarningWithAnError(t *testing.T) {
	spec := scaledCheckSpec(t, 3, 1, false)
	spec.Supported = []string{"de", "en", "zu"}
	spec.Required = []string{"zu"}
	report := Check(spec, CheckPolicy{MaxFindings: 1})
	if report.Errors != 3 || report.Warnings != 3 || len(report.Findings) != 1 || !report.Truncated {
		t.Fatalf("bounded mixed report = %+v", report)
	}
	finding := report.Findings[0]
	if finding.Status != CheckMissing || finding.Severity != SeverityError || finding.Locale != "zu" || finding.Key != "app.message.m0000" {
		t.Fatalf("retained error = %+v", finding)
	}
}

func TestCheckIndexedOverridesCoverEveryEffectiveCell(t *testing.T) {
	const messageCount = 12
	spec := scaledCheckSpec(t, messageCount, 8, true)
	report := Check(spec, CheckPolicy{MaxFindings: 1})
	if !report.OK() || report.Warnings != 0 || len(report.Findings) != 0 {
		t.Fatalf("indexed override report = %+v", report)
	}
	if len(report.Coverage) != 8 {
		t.Fatalf("coverage locales = %d, want 8", len(report.Coverage))
	}
	for _, coverage := range report.Coverage {
		if coverage.Total != messageCount || coverage.Approved != messageCount || !coverage.Complete() {
			t.Fatalf("override coverage = %+v", coverage)
		}
	}
}

func TestCheckInvalidCompleteUsageDoesNotClaimMessagesAreUnused(t *testing.T) {
	spec := scaledCheckSpec(t, 2, 1, false)
	assertNoUnused := func(t *testing.T, report Report) {
		t.Helper()
		if report.Errors == 0 {
			t.Fatalf("invalid usage unexpectedly passed: %+v", report)
		}
		for _, finding := range report.Findings {
			if finding.Status == CheckUnused {
				t.Fatalf("invalid complete usage claimed an unused message: %+v", report.Findings)
			}
		}
	}
	t.Run("invalid key", func(t *testing.T) {
		report := Check(spec, CheckPolicy{Usage: sealTestUsage(UsageManifest{
			Keys: []Key{"app.message.m0000", "not-qualified"}, GoScope: testGoUsageScope(), Complete: true,
		})})
		assertNoUnused(t, report)
	})
	t.Run("out of bounds", func(t *testing.T) {
		bounded := cloneCatalogSpec(spec)
		bounded.Limits = DefaultLimits()
		bounded.Limits.MaxMessages = 2
		report := Check(bounded, CheckPolicy{Usage: sealTestUsage(UsageManifest{
			Keys: []Key{"app.message.m0000", "app.message.m0001", "app.missing"}, GoScope: testGoUsageScope(), Complete: true,
		})})
		assertNoUnused(t, report)
	})
	t.Run("valid empty control", func(t *testing.T) {
		report := Check(spec, CheckPolicy{Usage: sealTestUsage(UsageManifest{GoScope: testGoUsageScope(), Complete: true})})
		if report.Warnings != 2 {
			t.Fatalf("valid complete usage warnings = %d, want 2", report.Warnings)
		}
		assertCheckFinding(t, report, CheckUnused, SeverityWarning, "app.message.m0000", "")
	})
}

func completeUsage(keys []Key, dynamic []DynamicUsage) UsageManifest {
	usage := UsageManifest{Keys: slices.Clone(keys), Dynamic: slices.Clone(dynamic), GoScope: testGoUsageScope(), Complete: true}
	slices.Sort(usage.Keys)
	slices.SortFunc(usage.Dynamic, compareDynamicUsage)
	line := 1
	for _, key := range keys {
		usage.Occurrences = append(usage.Occurrences, UsageOccurrence{Key: key, Path: "example.test/app/use.go", Line: line, Column: 1})
		line++
	}
	for _, value := range dynamic {
		usage.Occurrences = append(usage.Occurrences, UsageOccurrence{Domain: value.Domain, Prefix: value.Prefix, Path: "example.test/app/use.go", Line: line, Column: 1})
		line++
	}
	usage.ManifestDigest = ExpectedUsageManifestDigest(usage)
	return usage
}

func testGoUsageScope() *GoUsageScope {
	scope := &GoUsageScope{
		Analyzer: GoUsageAnalyzerV2, GOOS: "linux", GOARCH: "amd64", Compiler: "gc", GoVersion: "go1.26.5", Toolchain: "go1.26.5", GoWork: "off", GoEnv: "off",
		Environment: []UsageSetting{{Name: "CGO_ENABLED", Value: "false"}, {Name: "GOARCH", Value: "amd64"}, {Name: "GOENV", Value: "off"}, {Name: "GOEXPERIMENT"}, {Name: "GOFLAGS"}, {Name: "GOOS", Value: "linux"}, {Name: "GOVERSION", Value: "go1.26.5"}, {Name: "GOWORK", Value: "off"}},
		Roots:       []UsageRoot{{Path: "example.test/app", Kind: UsageRootDirectory}},
		Files:       []UsageFile{{Root: "example.test/app", Path: "use.go", LogicalPath: "example.test/app/use.go", SHA256: strings.Repeat("1", sha256.Size*2), Selected: true}},
		Metadata:    []UsageMetadata{{Kind: "module", Path: "example.test/app/go.mod", SHA256: strings.Repeat("2", sha256.Size*2)}}, SelectedFiles: 1,
	}
	scope.SourceDigest = ExpectedUsageSourceDigest(*scope)
	return scope
}

func sealTestUsage(usage UsageManifest) UsageManifest {
	usage.ManifestDigest = ExpectedUsageManifestDigest(usage)
	return usage
}

func TestCheckContextReturnsOneStableFailureWithoutLeakingTheCause(t *testing.T) {
	spec := checkFixture(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("private cancellation cause"))
	report := CheckContext(ctx, spec, CheckPolicy{})
	if report.Errors != 1 || report.Warnings != 0 || len(report.Findings) != 1 || !errors.Is(report.Err(), ErrCheckFailed) {
		t.Fatalf("canceled report = %+v, error = %v", report, report.Err())
	}
	finding := report.Findings[0]
	if finding.Status != CheckInvalid || finding.Severity != SeverityError || finding.Path != "check" || finding.Detail != "check was canceled" {
		t.Fatalf("cancellation finding = %+v", finding)
	}
	if strings.Contains(report.Error(), "private") || strings.Contains(finding.Detail, "private") {
		t.Fatalf("cancellation cause leaked: %+v", report)
	}
	if got := CheckContext(nil, spec, CheckPolicy{}); !reflect.DeepEqual(got, report) {
		t.Fatalf("nil and canceled contexts differ:\n%+v\n%+v", got, report)
	}
	if got, want := CheckContext(context.Background(), spec, CheckPolicy{}), Check(spec, CheckPolicy{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckContext changed the successful report:\n%+v\n%+v", got, want)
	}
}

func TestCheckContextPollsForCancellationDuringBoundedWork(t *testing.T) {
	ctx := &cancelAfterChecks{remaining: 3, done: make(chan struct{})}
	report := CheckContext(ctx, scaledCheckSpec(t, 4096, 2, false), CheckPolicy{MaxFindings: 1})
	if ctx.remaining != 0 || report.Errors != 1 || len(report.Findings) != 1 || report.Findings[0].Detail != "check was canceled" {
		t.Fatalf("bounded cancellation = (%d checks remaining, %+v)", ctx.remaining, report)
	}
}

func TestCheckReportsSourceIdentityFailures(t *testing.T) {
	spec := checkFixture(t)
	ceiling := DefaultLimits()
	ceiling.MaxMessages = 1
	if digest, err := checkSourceDigest(spec, ceiling); err == nil || digest != "" {
		t.Fatalf("source identity failure = (%q, %v)", digest, err)
	}
}

func BenchmarkCheckCappedSparseCoverage(b *testing.B) {
	spec := scaledCheckSpec(b, 4096, 128, false)
	policy := CheckPolicy{MaxFindings: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		report := Check(spec, policy)
		if report.Errors != 0 || report.Warnings != 4096*127 || len(report.Findings) != 1 {
			b.Fatalf("report = %+v", report)
		}
	}
}

func BenchmarkCheckIndexedEffectiveOverrides(b *testing.B) {
	spec := scaledCheckSpec(b, 300, 20, true)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		report := Check(spec, CheckPolicy{MaxFindings: 1})
		if !report.OK() || len(report.Findings) != 0 {
			b.Fatalf("report = %+v", report)
		}
	}
}

func BenchmarkCheckCollectorCappedErrorReplacement(b *testing.B) {
	const maximum = 4096
	warning := Finding{Status: CheckMissing, Severity: SeverityWarning, Path: "warning"}
	failure := Finding{Status: CheckInvalid, Severity: SeverityError, Path: "error"}
	b.ReportAllocs()
	for range b.N {
		collector := checkCollector{maximum: maximum, ctx: context.Background()}
		for range maximum {
			collector.add(warning)
		}
		for range maximum {
			collector.add(failure)
		}
		if collector.errors != maximum || collector.warnings != maximum || collector.warningsAt != maximum {
			b.Fatalf("collector = %+v", collector)
		}
	}
}

func scaledCheckSpec(t testing.TB, messageCount, localeCount int, overrides bool) CatalogSpec {
	t.Helper()
	supported := make([]string, 0, localeCount)
	supported = append(supported, "en")
	for index := 0; index < localeCount-1; index++ {
		supported = append(supported, fmt.Sprintf("en-x-l%03d", index))
	}
	messages := make([]MessageSpec, messageCount)
	for index := range messages {
		message := simpleMessage(fmt.Sprintf("message.m%04d", index), "Message")
		if overrides {
			message.Override = OverrideApplication
		}
		messages[index] = message
	}
	spec := CatalogSpec{
		Revision: "scaled-check", SourceLocale: "en", DefaultLocale: "en",
		Supported: supported, Modules: []Module{{Name: "app", Messages: messages}},
	}
	if !overrides {
		return spec
	}
	for _, message := range messages {
		digest, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "app", message)
		if err != nil {
			t.Fatal(err)
		}
		key := Qualify("app", message.ID)
		for _, locale := range supported[1:] {
			reviewDigest, err := ExpectedReviewDigest(digest, locale, "Override")
			if err != nil {
				t.Fatal(err)
			}
			spec.Overrides = append(spec.Overrides, Override{
				Key: key, Locale: locale, Text: "Override", Review: ReviewApproved,
				ContractRevision: message.Revision, SourceDigest: digest, ReviewDigest: reviewDigest,
			})
		}
	}
	return spec
}

type cancelAfterChecks struct {
	remaining int
	done      chan struct{}
}

func (c *cancelAfterChecks) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *cancelAfterChecks) Done() <-chan struct{} {
	if c.remaining > 0 {
		c.remaining--
		if c.remaining == 0 {
			close(c.done)
		}
	}
	return c.done
}

func (c *cancelAfterChecks) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func (c *cancelAfterChecks) Value(any) any { return nil }

func assertCheckFinding(t testing.TB, report Report, status CheckStatus, severity CheckSeverity, key Key, locale string) {
	t.Helper()
	for _, finding := range report.Findings {
		if finding.Status == status && finding.Severity == severity && (key == "" || finding.Key == key) && (locale == "" || finding.Locale == locale) {
			return
		}
	}
	t.Fatalf("finding (%s, %s, %q, %q) is absent: %+v", status, severity, key, locale, report.Findings)
}

func findCoverage(values []LocaleCoverage, locale string) (LocaleCoverage, bool) {
	for _, value := range values {
		if value.Locale == locale {
			return value, true
		}
	}
	return LocaleCoverage{}, false
}
