package i18n

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func TestFallbackUsesExplicitParentThenDefaultThenSource(t *testing.T) {
	spec := CatalogSpec{
		Revision:      "catalog",
		SourceLocale:  "en",
		DefaultLocale: "ru",
		Supported:     []string{"en", "ru", "fr-CA", "pl"},
		Parents:       []LocaleEdge{{Locale: "fr-CA", Parent: "fr"}},
		Modules: []Module{{Name: "app", Messages: []MessageSpec{
			{ID: "parent", Revision: "r", Source: "source", Translations: []Translation{{Locale: "fr", Text: "parent", Review: ReviewApproved, ContractRevision: "r"}}},
			{ID: "default", Revision: "r", Source: "source", Translations: []Translation{{Locale: "ru", Text: "default", Review: ReviewApproved, ContractRevision: "r"}}},
			{ID: "source", Revision: "r", Source: "source"},
		}}},
	}
	snapshot := mustSnapshot(t, spec)
	tests := []struct {
		id       string
		locale   string
		text     string
		template string
	}{
		{id: "parent", locale: "fr-CA", text: "parent", template: "fr"},
		{id: "default", locale: "pl", text: "default", template: "ru"},
		{id: "source", locale: "pl", text: "source", template: "en"},
	}
	for _, tc := range tests {
		message, err := snapshot.Bind(Key("app." + tc.id))
		if err != nil {
			t.Fatal(err)
		}
		rendered := mustRender(t, mustView(t, snapshot, tc.locale, "", "", PresentationDefault), message)
		if rendered.Text != tc.text || rendered.TemplateLocale != tc.template || rendered.Outcome != OutcomeFallback {
			t.Errorf("%s fallback = %+v", tc.id, rendered)
		}
		explanation, err := mustView(t, snapshot, tc.locale, "", "", PresentationDefault).Explain(message)
		if err != nil || explanation.TemplateLocale != tc.template || explanation.Key != Key("app."+tc.id) || len(explanation.Steps) == 0 {
			t.Errorf("%s explanation = %+v, %v", tc.id, explanation, err)
		}
	}
}

func TestOverlaysAreImmutableLayeredAndIsolatedAcrossTenants(t *testing.T) {
	spec := testCatalog(MessageSpec{ID: "m", Revision: "r", Source: "module", Override: OverrideAny})
	base := mustSnapshot(t, spec)
	application, err := base.Overlay(ApplicationOverlay("app-1", reviewedOverride(t, base, Override{Key: "app.m", Locale: "en", Text: "application", ContractRevision: "r", Review: ReviewApproved})))
	if err != nil {
		t.Fatal(err)
	}
	tenantAInput := []Override{reviewedOverride(t, application, Override{Key: "app.m", Locale: "en", Text: "tenant-a", ContractRevision: "r", Review: ReviewApproved})}
	tenantA, err := application.Overlay(TenantOverlay("tenant-a-1", tenantAInput...))
	if err != nil {
		t.Fatal(err)
	}
	tenantB, err := application.Overlay(TenantOverlay("tenant-b-1", reviewedOverride(t, application, Override{Key: "app.m", Locale: "en", Text: "tenant-b", ContractRevision: "r", Review: ReviewApproved})))
	if err != nil {
		t.Fatal(err)
	}
	tenantAInput[0].Text = "mutated"
	checks := []struct {
		snapshot *Snapshot
		text     string
		layer    Layer
	}{
		{base, "module", LayerModule},
		{application, "application", LayerApplication},
		{tenantA, "tenant-a", LayerTenant},
		{tenantB, "tenant-b", LayerTenant},
	}
	var wait sync.WaitGroup
	for _, check := range checks {
		check := check
		for range 20 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				message, err := check.snapshot.Bind("app.m")
				if err != nil {
					t.Error(err)
					return
				}
				rendered, err := mustView(t, check.snapshot, "en", "", "", PresentationDefault).Render(context.Background(), message)
				if err != nil || rendered.Text != check.text || rendered.Layer != check.layer {
					t.Errorf("overlay render = %+v, %v; want %q/%v", rendered, err, check.text, check.layer)
				}
			}()
		}
	}
	wait.Wait()
	if base.Digest() == application.Digest() || application.Digest() == tenantA.Digest() || tenantA.Digest() == tenantB.Digest() {
		t.Fatal("semantically distinct overlays share a digest")
	}
	if _, err := tenantA.Overlay(ApplicationOverlay("lower", Override{Key: "app.m", Locale: "en", Text: "lower", ContractRevision: "r", Review: ReviewApproved})); err == nil {
		t.Fatal("lower layer replaced a tenant layer")
	}
}

func TestOverlayBoundsWholeResultAndRevision(t *testing.T) {
	spec := testCatalog(MessageSpec{ID: "m", Revision: "r", Source: "base", Override: OverrideApplication})
	probe := mustSnapshot(t, spec)
	spec.Limits.MaxCatalogBytes = snapshotCatalogBytes(probe) + 5
	spec.Limits.MaxRevisionBytes = 16
	base := mustSnapshot(t, spec)
	_, err := base.Overlay(ApplicationOverlay("overlay", reviewedOverride(t, base, Override{Key: "app.m", Locale: "en", Text: "a much longer replacement", ContractRevision: "r", Review: ReviewApproved})))
	if err == nil {
		t.Fatal("whole-result byte limit was not enforced")
	}
	_, err = base.Overlay(ApplicationOverlay("revision-too-long", reviewedOverride(t, base, Override{Key: "app.m", Locale: "en", Text: "x", ContractRevision: "r", Review: ReviewApproved})))
	if err == nil {
		t.Fatal("combined revision limit was not enforced")
	}
}

func TestOverlayBoundsTheFinalTranslationSet(t *testing.T) {
	spec := testCatalog(MessageSpec{ID: "m", Revision: "r", Source: "base", Override: OverrideApplication})
	spec.Supported = []string{"en", "fr", "de"}
	spec.Limits.MaxTranslations = 2
	base := mustSnapshot(t, spec)
	overrides := []Override{
		reviewedOverride(t, base, Override{Key: "app.m", Locale: "fr", Text: "français", ContractRevision: "r", Review: ReviewApproved}),
		reviewedOverride(t, base, Override{Key: "app.m", Locale: "de", Text: "deutsch", ContractRevision: "r", Review: ReviewApproved}),
	}
	if _, err := base.Overlay(ApplicationOverlay("too-many", overrides...)); err == nil {
		t.Fatal("overlay exceeded the final translation limit")
	}
	replacement, err := base.Overlay(ApplicationOverlay("replacement", reviewedOverride(t, base, Override{
		Key: "app.m", Locale: "en", Text: "replacement", ContractRevision: "r", Review: ReviewApproved,
	})))
	if err != nil {
		t.Fatalf("replacement at the translation limit failed: %v", err)
	}
	encoded, err := Encode(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), bytes.NewReader(encoded)); err != nil {
		t.Fatalf("loader rejected an encoded bounded overlay: %v", err)
	}

	itemSpec := testCatalog(MessageSpec{ID: "m", Revision: "r", Source: "base", Override: OverrideApplication})
	itemSpec.Supported = []string{"en", "fr"}
	itemProbe := mustSnapshot(t, itemSpec)
	itemSpec.Limits.MaxCatalogItems = snapshotCatalogItems(itemProbe)
	itemBounded := mustSnapshot(t, itemSpec)
	if _, err := itemBounded.Overlay(ApplicationOverlay("new-locale", reviewedOverride(t, itemBounded, Override{
		Key: "app.m", Locale: "fr", Text: "français", ContractRevision: "r", Review: ReviewApproved,
	}))); err == nil {
		t.Fatal("overlay exceeded the final catalog item limit")
	}
}

func errorCatalog(t *testing.T, observer Observer) *Snapshot {
	t.Helper()
	translation := func(text string) []Translation {
		return []Translation{{Locale: "ru", Text: text, Review: ReviewApproved, ContractRevision: "r"}}
	}
	spec := CatalogSpec{
		Revision: "errors", SourceLocale: "en", DefaultLocale: "en", Supported: []string{"en", "ru"}, Observer: observer,
		Modules: []Module{{Name: "errors", Messages: []MessageSpec{
			{ID: "most", Revision: "r", Source: "most"},
			{ID: "first", Revision: "r", Source: "first"},
			{ID: "last", Revision: "r", Source: "last"},
			{ID: "generic", Revision: "r", Source: "generic"},
			{ID: "limited", Revision: "r", Source: "Limit {$limit :number}", Arguments: []ArgumentSpec{{Name: "limit", Type: TypeInteger, Required: true}}},
			{ID: "field", Revision: "r", Source: "{$field}: required", Arguments: []ArgumentSpec{{Name: "field", Type: TypeText, Required: true}}, Translations: translation("{$field}: обязательно")},
			{ID: "email", Revision: "r", Source: "Email address", Translations: translation("Электронная почта")},
		}}},
	}
	return mustSnapshot(t, spec)
}

func TestErrorMessageSourcePreservesLadderSpecificityAndFallsThroughBadParams(t *testing.T) {
	snapshot := errorCatalog(t, nil)
	all := []ErrorMapping{
		{Ladder: "user.email.required", Key: "errors.most"},
		{Ladder: "user.required", Key: "errors.first"},
		{Ladder: "email.required", Key: "errors.last"},
		{Ladder: "required", Key: "errors.generic"},
	}
	violation := errs.Violation{Path: errs.Path{errs.Named("user"), errs.Indexed(2), errs.Named("email")}, Code: errs.Code("required")}
	for removed, want := range []string{"most", "first", "last", "generic"} {
		source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: all[removed:]})
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := source.Message(context.Background(), violation, "en"); !ok || got != want {
			t.Errorf("ladder %d = %q/%v, want %q", removed, got, ok, want)
		}
	}

	source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{
		{Ladder: "user.email.required", Key: "errors.limited", Params: []ErrorParam{{Param: "limit", Argument: "limit"}}},
		{Ladder: "required", Key: "errors.generic"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := source.Message(context.Background(), violation, "en"); !ok || got != "generic" {
		t.Fatalf("missing param fallback = %q/%v", got, ok)
	}
	violation.Params = map[string]any{"limit": int64(7), "secret": "must not be forwarded"}
	if got, ok := source.Message(context.Background(), violation, "en"); !ok || withoutIsolation(got) != "Limit 7" {
		t.Fatalf("allowlisted param render = %q/%v", got, ok)
	}

	dotted, err := snapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{{Ladder: "order.items.email.unique", Key: "errors.most"}}})
	if err != nil {
		t.Fatal(err)
	}
	dottedViolation := errs.Violation{Path: errs.Path{errs.Named("order.items"), errs.Named("email")}, Code: errs.Code("unique")}
	if got, ok := dotted.Message(context.Background(), dottedViolation, "en"); !ok || got != "most" {
		t.Fatalf("opaque dotted ladder = %q/%v", got, ok)
	}
}

func TestErrorMessageSourceRendersDeclaredFieldLabelAndObservesOnce(t *testing.T) {
	var mu sync.Mutex
	observations := make([]Observation, 0)
	snapshot := errorCatalog(t, func(_ context.Context, observation Observation) {
		mu.Lock()
		observations = append(observations, observation)
		mu.Unlock()
		panic("isolated")
	})
	source, err := snapshot.ErrorMessages(ErrorSpec{
		Mappings:    []ErrorMapping{{Ladder: "required", Key: "errors.field", FieldArgument: "field"}},
		FieldLabels: []FieldLabel{{Field: "email", Key: "errors.email"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	violation := errs.Violation{Path: errs.Path{errs.Named("user"), errs.Named("email")}, Code: errs.Code("required")}
	got, ok := source.Message(nil, violation, "ru")
	if !ok || withoutIsolation(got) != "Электронная почта: обязательно" {
		t.Fatalf("field label = %q/%v", got, ok)
	}
	if got, ok := source.Message(context.Background(), violation, "not a locale"); ok || got != "" {
		t.Fatalf("invalid locale = %q/%v", got, ok)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observations) != 2 || observations[0].Operation != OperationRender || observations[0].Outcome != OutcomeSuccess || observations[1].Outcome != OutcomeInvalid {
		t.Fatalf("adapter observations = %+v", observations)
	}
}

func TestErrorMessageSourceIsImmutableAndConcurrent(t *testing.T) {
	snapshot := errorCatalog(t, nil)
	mappings := []ErrorMapping{{Ladder: "required", Key: "errors.generic"}}
	source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: mappings})
	if err != nil {
		t.Fatal(err)
	}
	mappings[0].Key = "errors.most"
	violation := errs.Violation{Code: errs.Code("required")}
	var wait sync.WaitGroup
	for index := range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			localeName := "en"
			if index%2 == 1 {
				localeName = "ru"
			}
			got, ok := source.Message(context.Background(), violation, localeName)
			if !ok || got != "generic" {
				t.Errorf("concurrent error message = %q/%v", got, ok)
			}
		}()
	}
	wait.Wait()
}

func TestErrorMappingConstructionRejectsUndeclaredOrUnsafeInputs(t *testing.T) {
	snapshot := errorCatalog(t, nil)
	tests := []ErrorSpec{
		{Mappings: []ErrorMapping{{Ladder: "required", Key: "missing.key"}}},
		{Mappings: []ErrorMapping{{Ladder: "required", Key: "errors.limited"}}},
		{Mappings: []ErrorMapping{{Ladder: "required", Key: "errors.generic", Params: []ErrorParam{{Param: "secret", Argument: "secret"}}}}},
		{Mappings: []ErrorMapping{{Ladder: "contains space", Key: "errors.generic"}}},
		{Mappings: []ErrorMapping{{Ladder: "required", Key: "errors.field", FieldArgument: "limit"}}},
	}
	for i, spec := range tests {
		if _, err := snapshot.ErrorMessages(spec); err == nil {
			t.Errorf("invalid ErrorSpec %d was accepted: %+v", i, spec)
		}
	}
}
