package i18n

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/errs"
)

func errorPlanCatalog(t *testing.T, observer Observer) *Snapshot {
	t.Helper()
	return mustSnapshot(t, CatalogSpec{
		Revision:            "error-plan-1",
		SourceLocale:        "en",
		DefaultLocale:       "en",
		DefaultTimeZone:     "UTC",
		TimeZoneDataVersion: "iana-2026a",
		Supported:           []string{"en", "ru"},
		Capabilities:        []Capability{CapabilityDateTime},
		Observer:            observer,
		Modules: []Module{{Name: "errors", Messages: []MessageSpec{
			{
				ID:       "details",
				Revision: "r1",
				Source:   "Count {$count :number}; at {$at :datetime dateStyle=short timeStyle=short}",
				Arguments: []ArgumentSpec{
					{Name: "count", Type: TypeInteger, Required: true},
					{Name: "at", Type: TypeInstant, Required: true},
				},
				Translations: []Translation{{
					Locale: "ru", Text: "Количество {$count :number}; в {$at :datetime dateStyle=short timeStyle=short}", Review: ReviewApproved, ContractRevision: "r1",
				}},
			},
		}}},
	})
}

func TestErrorPlanBindsAllMessagesToOneViewWithoutResolvingAgain(t *testing.T) {
	var mu sync.Mutex
	observations := make([]Observation, 0, 4)
	snapshot := errorPlanCatalog(t, func(_ context.Context, observation Observation) {
		mu.Lock()
		observations = append(observations, observation)
		mu.Unlock()
	})
	mappings := []ErrorMapping{{
		Ladder: "invalid",
		Key:    "errors.details",
		Params: []ErrorParam{{Param: "count", Argument: "count"}, {Param: "at", Argument: "at"}},
	}}
	plan, err := snapshot.ErrorPlan(ErrorPlanSpec{Mappings: mappings})
	if err != nil {
		t.Fatal(err)
	}
	mappings[0].Key = "errors.missing"
	mappings[0].Params[0] = ErrorParam{Param: "changed", Argument: "changed"}
	resolution := snapshot.Resolve(Exact(SourceUser, "ru"))
	view, err := snapshot.View(ViewSpec{
		Resolution:       resolution,
		FormattingLocale: "en-US",
		TimeZone:         "Asia/Almaty",
		Presentation:     PresentationNoIsolation,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.September, 9, 20, 15, 0, 0, time.UTC)
	message, err := snapshot.Bind("errors.details", Integer("count", 1234), Instant("at", at))
	if err != nil {
		t.Fatal(err)
	}
	want, err := view.Render(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	source, err := view.ErrorMessages(plan)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	observations = observations[:0]
	mu.Unlock()
	violation := errs.Violation{Code: "invalid", Params: map[string]any{"count": int64(1234), "at": at}}
	got, actualLocale, ok := source.MessageWithLocale(context.Background(), violation, "not a locale")
	if !ok || got != want.Text || actualLocale != want.TemplateLocale || actualLocale != "ru" {
		t.Fatalf("view-bound error = %q/%q/%v, want %q/%q/true", got, actualLocale, ok, want.Text, want.TemplateLocale)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observations) != 1 || observations[0].Operation != OperationRender {
		t.Fatalf("view-bound observations = %+v", observations)
	}
}

func TestErrorPlanCanBindConcurrentRecipientViews(t *testing.T) {
	snapshot := errorPlanCatalog(t, nil)
	plan, err := snapshot.ErrorPlan(ErrorPlanSpec{Mappings: []ErrorMapping{{
		Ladder: "invalid",
		Key:    "errors.details",
		Params: []ErrorParam{{Param: "count", Argument: "count"}, {Param: "at", Argument: "at"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	enSource, err := mustView(t, snapshot, "en", "en", "UTC", PresentationDefault).ErrorMessages(plan)
	if err != nil {
		t.Fatal(err)
	}
	ruSource, err := mustView(t, snapshot, "ru", "ru", "Asia/Almaty", PresentationNoIsolation).ErrorMessages(plan)
	if err != nil {
		t.Fatal(err)
	}
	violation := errs.Violation{
		Code: "invalid",
		Params: map[string]any{
			"count": int64(7),
			"at":    time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC),
		},
	}
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(2)
		go func() {
			defer wait.Done()
			text, localeName, ok := enSource.MessageWithLocale(context.Background(), violation, "ru")
			if !ok || localeName != "en" || text == "" {
				t.Errorf("English source = %q/%q/%v", text, localeName, ok)
			}
		}()
		go func() {
			defer wait.Done()
			text, localeName, ok := ruSource.MessageWithLocale(context.Background(), violation, "en")
			if !ok || localeName != "ru" || text == "" {
				t.Errorf("Russian source = %q/%q/%v", text, localeName, ok)
			}
		}()
	}
	wait.Wait()
}

func TestErrorPlanOwnsItsFieldLabelMappings(t *testing.T) {
	snapshot := errorCatalog(t, nil)
	mappings := []ErrorMapping{{Ladder: "required", Key: "errors.field", FieldArgument: "field"}}
	labels := []FieldLabel{{Field: "email", Key: "errors.email"}}
	plan, err := snapshot.ErrorPlan(ErrorPlanSpec{Mappings: mappings, FieldLabels: labels})
	if err != nil {
		t.Fatal(err)
	}
	mappings[0] = ErrorMapping{Ladder: "required", Key: "errors.most"}
	labels[0] = FieldLabel{Field: "email", Key: "errors.generic"}
	source, err := mustView(t, snapshot, "ru", "", "", PresentationNoIsolation).ErrorMessages(plan)
	if err != nil {
		t.Fatal(err)
	}
	violation := errs.Violation{Path: errs.Path{errs.Named("user"), errs.Named("email")}, Code: "required"}
	text, localeName, ok := source.MessageWithLocale(context.Background(), violation, "en")
	if !ok || text != "Электронная почта: обязательно" || localeName != "ru" {
		t.Fatalf("owned field mapping = %q/%q/%v", text, localeName, ok)
	}
}

func TestErrorPlanRejectsInvalidConstructionAndCrossSnapshotBinding(t *testing.T) {
	snapshot := errorPlanCatalog(t, nil)
	valid, err := snapshot.ErrorPlan(ErrorPlanSpec{})
	if err != nil {
		t.Fatal(err)
	}
	view := mustView(t, snapshot, "en", "", "", PresentationDefault)
	var nilSnapshot *Snapshot
	if _, err := nilSnapshot.ErrorPlan(ErrorPlanSpec{}); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("nil snapshot error = %v", err)
	}
	if _, err := (&Snapshot{}).ErrorPlan(ErrorPlanSpec{}); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("zero snapshot error = %v", err)
	}
	var nilView *View
	if _, err := nilView.ErrorMessages(valid); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("nil view error = %v", err)
	}
	if _, err := view.ErrorMessages(nil); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("nil plan error = %v", err)
	}
	if _, err := view.ErrorMessages(&ErrorPlan{}); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("zero plan error = %v", err)
	}
	foreign := errorPlanCatalog(t, nil)
	foreignPlan, err := foreign.ErrorPlan(ErrorPlanSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.ErrorMessages(foreignPlan); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("foreign plan error = %v", err)
	}
	invalid := []ErrorPlanSpec{
		{Mappings: []ErrorMapping{{Ladder: "invalid", Key: "errors.missing"}}},
		{Mappings: []ErrorMapping{{Ladder: "invalid", Key: "errors.details"}}},
		{Mappings: []ErrorMapping{{Ladder: "invalid", Key: "errors.details"}, {Ladder: "invalid", Key: "errors.details"}}},
	}
	for index, spec := range invalid {
		if _, err := snapshot.ErrorPlan(spec); err == nil {
			t.Errorf("invalid plan %d was accepted", index)
		}
	}
}
