package i18n

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDirectFormattersProduceStructuredPinnedOutput(t *testing.T) {
	snapshot := localeSnapshot(t, "en", "ready")
	view := mustView(t, snapshot, "en", "", "", PresentationNoIsolation)

	list, err := view.FormatList(context.Background(), []string{"alpha", "beta", "gamma"}, ListFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if list.Text != "alpha, beta, and gamma" || list.Locale != "en" {
		t.Fatalf("list = %#v", list)
	}
	if got := joinPartText(list.Parts); got != list.Text {
		t.Fatalf("list parts = %q, want %q", got, list.Text)
	}
	if countPartKind(list.Parts, PartValue) != 3 {
		t.Fatalf("list value parts = %#v", list.Parts)
	}
	for _, part := range list.Parts {
		if part.Type == "" || part.Name != part.Type {
			t.Fatalf("list part type/name = %#v", list.Parts)
		}
	}

	relative, err := view.FormatRelative(context.Background(), -1, RelativeDay, RelativeFormatSpec{Numeric: RelativeNumericAuto})
	if err != nil {
		t.Fatal(err)
	}
	if relative.Text != "yesterday" || relative.Locale != "en" || joinPartText(relative.Parts) != relative.Text {
		t.Fatalf("relative = %#v", relative)
	}
	if len(relative.Parts) != 1 || relative.Parts[0].Type != "literal" || relative.Parts[0].Name != "" {
		t.Fatalf("relative part type/name = %#v", relative.Parts)
	}

	duration, err := view.FormatDuration(context.Background(), DurationValue{Hours: 1, Minutes: 2, Seconds: 3}, DurationFormatSpec{Style: DurationDigital})
	if err != nil {
		t.Fatal(err)
	}
	if duration.Text != "1:02:03" || duration.Locale != "en" || joinPartText(duration.Parts) != duration.Text {
		t.Fatalf("duration = %#v", duration)
	}
	for _, part := range duration.Parts {
		if part.Type == "" {
			t.Fatalf("duration part type is empty: %#v", duration.Parts)
		}
		if part.Kind == PartValue && part.Name == "" {
			t.Fatalf("duration value lost its unit name: %#v", duration.Parts)
		}
	}

	display, found, err := view.FormatDisplayName(context.Background(), "US", DisplayNameSpec{Type: DisplayRegion})
	if err != nil {
		t.Fatal(err)
	}
	if !found || display.Text != "United States" || display.Locale != "en" || joinPartText(display.Parts) != display.Text {
		t.Fatalf("display name = %#v/%v", display, found)
	}
	if len(display.Parts) != 1 || display.Parts[0].Type != "displayName" || display.Parts[0].Name != "region" {
		t.Fatalf("display-name part type/name = %#v", display.Parts)
	}
}

func TestDirectFormattingUsesFormattingLocaleWithoutAmbientState(t *testing.T) {
	snapshot := localeSnapshot(t, "en", "ready")
	view := mustView(t, snapshot, "en", "ru", "", PresentationNoIsolation)

	list, err := view.FormatList(context.Background(), []string{"один", "два", "три"}, ListFormatSpec{Type: ListDisjunction, Width: FormatWidthLong})
	if err != nil {
		t.Fatal(err)
	}
	if list.Text != "один, два или три" || list.Locale != "ru" {
		t.Fatalf("Russian list = %#v", list)
	}
	relative, err := view.FormatRelative(context.Background(), 2, RelativeDay, RelativeFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if relative.Text != "через 2 дня" || relative.Locale != "ru" {
		t.Fatalf("Russian relative time = %#v", relative)
	}
	display, found, err := view.FormatDisplayName(context.Background(), "US", DisplayNameSpec{Type: DisplayRegion})
	if err != nil {
		t.Fatal(err)
	}
	if !found || display.Text != "Соединенные Штаты" || display.Locale != "ru" {
		t.Fatalf("Russian display name = %#v/%v", display, found)
	}

	arabic := mustView(t, snapshot, "en", "ar-u-nu-arab", "", PresentationNoIsolation)
	formatted, err := arabic.FormatRelative(context.Background(), 3, RelativeDay, RelativeFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(formatted.Text, "٣") || formatted.Locale != "ar-u-nu-arab" {
		t.Fatalf("Arabic numbering = %#v", formatted)
	}
}

func TestDirectListAppliesIsolationAndRejectsUnsafeInput(t *testing.T) {
	snapshot := localeSnapshot(t, "en", "ready")
	isolated := mustView(t, snapshot, "en", "", "", PresentationDefault)
	formatted, err := isolated.FormatList(context.Background(), []string{"one", "اثنان"}, ListFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if formatted.Text != "\u2066\u2068one\u2069 and \u2068اثنان\u2069\u2069" || countPartKind(formatted.Parts, PartBidiIsolation) != 6 {
		t.Fatalf("isolated list = %#v", formatted)
	}
	for _, part := range formatted.Parts {
		if part.Type == "" || part.Kind == PartBidiIsolation && part.Type != "bidiIsolation" {
			t.Fatalf("isolated list part type = %#v", formatted.Parts)
		}
	}

	plain := mustView(t, snapshot, "en", "", "", PresentationNoIsolation)
	formatted, err = plain.FormatList(context.Background(), []string{"one", "اثنان"}, ListFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if formatted.Text != "one and اثنان" || slices.ContainsFunc(formatted.Parts, func(part Part) bool { return part.Kind == PartBidiIsolation }) {
		t.Fatalf("non-isolated list = %#v", formatted)
	}

	for _, value := range []string{"unsafe\u202e", "\xff"} {
		if _, err := plain.FormatList(context.Background(), []string{value}, ListFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("unsafe %q error = %v", value, err)
		}
	}
}

func TestDirectFormattersEnforceValidationExactnessAndLimits(t *testing.T) {
	limitedSpec := testCatalog(simpleMessage("m", "ok"))
	limitedSpec.Limits.MaxArguments = 2
	limitedSpec.Limits.MaxArgumentBytes = 8
	limitedSpec.Limits.MaxOutputBytes = 8
	limitedSpec.Limits.MaxOutputParts = 3
	limited := mustView(t, mustSnapshot(t, limitedSpec), "en", "", "", PresentationNoIsolation)
	if _, err := limited.FormatList(context.Background(), []string{"a", "b", "c"}, ListFormatSpec{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("list count error = %v", err)
	}
	if _, err := limited.FormatList(context.Background(), []string{"123456789"}, ListFormatSpec{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("list bytes error = %v", err)
	}
	if _, err := limited.FormatRelative(context.Background(), 1234567, RelativeDay, RelativeFormatSpec{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("relative output error = %v", err)
	}

	view := mustView(t, localeSnapshot(t, "en", "ready"), "en", "", "", PresentationNoIsolation)
	for _, value := range []int64{maximumExactIntlInteger, -maximumExactIntlInteger} {
		formatted, err := view.FormatRelative(context.Background(), value, RelativeDay, RelativeFormatSpec{})
		if err != nil || formatted.Text == "" || formatted.Text != joinPartText(formatted.Parts) {
			t.Fatalf("exact relative boundary %d = %#v, %v", value, formatted, err)
		}
	}
	for _, value := range []int64{maximumExactIntlInteger + 1, -maximumExactIntlInteger - 1} {
		if _, err := view.FormatRelative(context.Background(), value, RelativeDay, RelativeFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("inexact relative boundary %d = %v", value, err)
		}
	}
	if _, err := view.FormatDuration(context.Background(), DurationValue{Seconds: maximumExactIntlInteger + 1}, DurationFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("inexact duration error = %v", err)
	}
	if _, err := view.FormatDuration(context.Background(), DurationValue{Hours: 1, Minutes: -1}, DurationFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("mixed-sign duration error = %v", err)
	}
	if _, err := view.FormatDuration(context.Background(), DurationValue{Seconds: 1}, DurationFormatSpec{FractionalDigits: DurationFractionalDigits(255)}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("fractional digit error = %v", err)
	}
	if _, err := view.FormatRelative(context.Background(), 1, RelativeUnit(255), RelativeFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("relative unit error = %v", err)
	}
	if _, err := view.FormatList(context.Background(), nil, ListFormatSpec{Type: ListType(255)}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("list type error = %v", err)
	}
	if _, _, err := view.FormatDisplayName(context.Background(), "US", DisplayNameSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("display type error = %v", err)
	}
	if _, _, err := view.FormatDisplayName(context.Background(), "not a region", DisplayNameSpec{Type: DisplayRegion}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("display code error = %v", err)
	}
}

func TestDirectFormatterLocaleFallbackIsNeverSilent(t *testing.T) {
	view := mustView(t, localeSnapshot(t, "ast", "ready"), "ast", "", "", PresentationNoIsolation)
	if _, err := view.FormatList(context.Background(), []string{"a", "b"}, ListFormatSpec{}); !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("list fallback error = %v", err)
	}
	if _, err := view.FormatRelative(context.Background(), 1, RelativeDay, RelativeFormatSpec{}); !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("relative fallback error = %v", err)
	}
	if _, err := view.FormatDuration(context.Background(), DurationValue{Seconds: 1}, DurationFormatSpec{}); !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("duration fallback error = %v", err)
	}
	if _, _, err := view.FormatDisplayName(context.Background(), "US", DisplayNameSpec{Type: DisplayRegion}); !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("display fallback error = %v", err)
	}
}

func TestDirectFormattersCancellationObservationAndConcurrency(t *testing.T) {
	var observations atomic.Int64
	spec := testCatalog(simpleMessage("m", "ok"))
	spec.Observer = func(_ context.Context, observation Observation) {
		if observation.Operation == OperationRender {
			observations.Add(1)
		}
	}
	view := mustView(t, mustSnapshot(t, spec), "en", "", "", PresentationNoIsolation)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := view.FormatList(canceled, []string{"a", "b"}, ListFormatSpec{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}

	const workers = 32
	var wait sync.WaitGroup
	wait.Add(workers)
	for index := range workers {
		go func() {
			defer wait.Done()
			formatted, err := view.FormatRelative(context.Background(), int64(index), RelativeDay, RelativeFormatSpec{})
			if err != nil || formatted.Text == "" {
				t.Errorf("concurrent relative = %#v, %v", formatted, err)
			}
		}()
	}
	wait.Wait()
	if observations.Load() != workers+1 {
		t.Fatalf("observations = %d, want %d", observations.Load(), workers+1)
	}
}

func TestDirectFormatterClosedEnums(t *testing.T) {
	valid := []interface {
		String() string
		Valid() bool
	}{
		FormatWidthNarrow,
		ListDisjunction,
		RelativeYear,
		RelativeNumericAuto,
		DurationDigital,
		DurationUnitTwoDigit,
		DurationDisplayAlways,
		DisplayDateTimeField,
		DisplayNameNone,
		LanguageStandard,
	}
	for _, value := range valid {
		if !value.Valid() || value.String() == "unknown" {
			t.Fatalf("valid enum = %T/%s", value, value.String())
		}
	}
	invalid := []interface {
		String() string
		Valid() bool
	}{
		FormatWidth(255),
		ListType(255),
		RelativeUnit(255),
		RelativeNumeric(255),
		DurationStyle(255),
		DurationUnitStyle(255),
		DurationDisplay(255),
		DisplayNameType(255),
		DisplayNameFallback(255),
		LanguageDisplay(255),
	}
	for _, value := range invalid {
		if value.Valid() || value.String() != "unknown" {
			t.Fatalf("invalid enum = %T/%s", value, value.String())
		}
	}
}

func joinPartText(parts []Part) string {
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

func countPartKind(parts []Part, kind PartKind) int {
	count := 0
	for _, part := range parts {
		if part.Kind == kind {
			count++
		}
	}
	return count
}
