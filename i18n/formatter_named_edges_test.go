package i18n

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestNamedFormatterFailuresObserveEveryFamilyExactlyOnce(t *testing.T) {
	observations := make([]Observation, 0)
	formatter, err := NewFormatter(FormatterSpec{
		Locale: "en",
		Observer: func(_ context.Context, observation Observation) {
			observations = append(observations, observation)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	calls := []func(context.Context, string) error{
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedNumber(ctx, name, SignedNumber(1))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedNumberRange(ctx, name, SignedNumber(1), SignedNumber(2))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedMoney(ctx, name, MoneyValue{Amount: "1", Currency: "USD"})
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedMoneyRange(ctx, name, MoneyValue{Amount: "1", Currency: "USD"}, MoneyValue{Amount: "2", Currency: "USD"})
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedPercent(ctx, name, SignedNumber(1))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedPercentRange(ctx, name, SignedNumber(1), SignedNumber(2))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedUnit(ctx, name, SignedNumber(1))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedUnitRange(ctx, name, SignedNumber(1), SignedNumber(2))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.SelectNamedPlural(ctx, name, SignedNumber(1))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.SelectNamedPluralRange(ctx, name, SignedNumber(1), SignedNumber(2))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedDate(ctx, name, DateValue{Year: 2026, Month: time.September, Day: 9})
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedDateRange(ctx, name, DateValue{Year: 2026, Month: time.September, Day: 9}, DateValue{Year: 2026, Month: time.September, Day: 10})
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedInstantDate(ctx, name, now)
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedInstantDateRange(ctx, name, now, now.Add(time.Hour))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedTime(ctx, name, now)
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedTimeRange(ctx, name, now, now.Add(time.Hour))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedDateTime(ctx, name, now)
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedDateTimeRange(ctx, name, now, now.Add(time.Hour))
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedList(ctx, name, []string{"one", "two"})
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedRelative(ctx, name, 1, RelativeDay)
			return err
		},
		func(ctx context.Context, name string) error {
			_, err := formatter.FormatNamedDuration(ctx, name, DurationValue{Seconds: 1})
			return err
		},
		func(ctx context.Context, name string) error {
			_, _, err := formatter.FormatNamedDisplayName(ctx, name, "US")
			return err
		},
	}
	for index, call := range calls {
		if err := call(context.Background(), "missing"); !errors.Is(err, ErrFormatNotFound) {
			t.Fatalf("missing call %d = %v", index, err)
		}
		assertNamedObservation(t, observations, index+1, OutcomeInvalid, ReasonTemplateFailure)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for index, call := range calls {
		if err := call(canceled, "missing"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled call %d = %v", index, err)
		}
		assertNamedObservation(t, observations, len(calls)+index+1, OutcomeCanceled, ReasonContextCanceled)
	}
	if err := calls[0](context.Background(), "bad\n"); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("invalid name = %v", err)
	}
	assertNamedObservation(t, observations, len(calls)*2+1, OutcomeInvalid, ReasonSchemaMismatch)
	if err := calls[0](context.Background(), strings.Repeat("a", formatter.context.limits.MaxIdentifierBytes+1)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized name = %v", err)
	}
	assertNamedObservation(t, observations, len(calls)*2+2, OutcomeLimited, ReasonLimit)
}

func assertNamedObservation(t *testing.T, observations []Observation, count int, outcome Outcome, reason Reason) {
	t.Helper()
	if len(observations) != count {
		t.Fatalf("observations = %d, want %d", len(observations), count)
	}
	observation := observations[len(observations)-1]
	if observation.Operation != OperationRender || observation.Outcome != outcome || observation.Reason != reason || observation.Count != 1 || observation.Duration < 0 {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestNamedFormatMaterialHasExactAggregateBound(t *testing.T) {
	formats := Formats{
		Numbers:      []NamedNumberFormat{{Name: "number", Spec: NumberFormatSpec{NumberingSystem: "latn"}}},
		Money:        []NamedMoneyFormat{{Name: "money", Spec: MoneyFormatSpec{Number: NumberFormatSpec{NumberingSystem: "latn"}}}},
		Percents:     []NamedPercentFormat{{Name: "percent", Spec: PercentFormatSpec{Number: NumberFormatSpec{NumberingSystem: "latn"}}}},
		Units:        []NamedUnitFormat{{Name: "unit", Spec: UnitFormatSpec{Number: NumberFormatSpec{NumberingSystem: "latn"}, Unit: "meter"}}},
		Plurals:      []NamedPluralFormat{{Name: "plural"}},
		Dates:        []NamedDateFormat{{Name: "date", Spec: DateFormatSpec{Calendar: "gregory", NumberingSystem: "latn"}}},
		Times:        []NamedTimeFormat{{Name: "time", Spec: TimeFormatSpec{Calendar: "gregory", NumberingSystem: "latn"}}},
		DateTimes:    []NamedDateTimeFormat{{Name: "datetime", Spec: DateTimeFormatSpec{Calendar: "gregory", NumberingSystem: "latn"}}},
		Lists:        []NamedListFormat{{Name: "list"}},
		Relatives:    []NamedRelativeFormat{{Name: "relative", Spec: RelativeFormatSpec{NumberingSystem: "latn"}}},
		Durations:    []NamedDurationFormat{{Name: "duration", Spec: DurationFormatSpec{NumberingSystem: "latn"}}},
		DisplayNames: []NamedDisplayNameFormat{{Name: "display", Spec: DisplayNameSpec{Type: DisplayRegion}}},
	}
	maximum := len("numberlatnmoneylatnpercentlatnunitlatnmeterpluraldategregorylatntimegregorylatndatetimegregorylatnlistrelativelatndurationlatndisplay")
	if _, err := NewFormatter(FormatterSpec{
		Locale:       "en",
		Capabilities: []Capability{CapabilityDateTime, CapabilityUnit},
		Limits:       Limits{MaxCatalogBytes: maximum},
		Formats:      formats,
	}); err != nil {
		t.Fatalf("at aggregate bound: %v", err)
	}
	if _, err := NewFormatter(FormatterSpec{
		Locale:       "en",
		Capabilities: []Capability{CapabilityDateTime, CapabilityUnit},
		Limits:       Limits{MaxCatalogBytes: maximum - 1},
		Formats:      formats,
	}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("one over aggregate bound = %v", err)
	}
	if _, err := NewFormatter(FormatterSpec{
		Locale:  "en",
		Limits:  Limits{MaxCatalogBytes: len("invalid") - 1},
		Formats: Formats{Relatives: []NamedRelativeFormat{{Name: "invalid", Spec: RelativeFormatSpec{Width: FormatWidth(255)}}}},
	}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("material preflight precedence = %v", err)
	}
}

func TestNamedFormatRetainedStringsAreCloned(t *testing.T) {
	nameBacking := strings.Repeat("number", 32)
	name := nameBacking[:len("number")]
	numberingBacking := strings.Repeat("latn", 32)
	numberingSystem := numberingBacking[:len("latn")]
	formatter, err := NewFormatter(FormatterSpec{
		Locale:  "en",
		Formats: Formats{Numbers: []NamedNumberFormat{{Name: name, Spec: NumberFormatSpec{NumberingSystem: numberingSystem}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for retainedName, spec := range formatter.formats.numbers {
		if unsafe.StringData(retainedName) == unsafe.StringData(name) {
			t.Fatal("named format retained caller name storage")
		}
		if unsafe.StringData(spec.NumberingSystem) == unsafe.StringData(numberingSystem) {
			t.Fatal("named format retained caller option storage")
		}
	}
}

func TestFormatterCapabilityCardinalityPrecedesAllocationAndContent(t *testing.T) {
	for _, capabilities := range [][]Capability{
		{CapabilityDateTime, CapabilityUnit, CapabilityDateTime},
		make([]Capability, 1<<20),
	} {
		if _, err := NewFormatter(FormatterSpec{Capabilities: capabilities}); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("capability cardinality = %v", err)
		}
	}
	formatter, err := NewFormatter(FormatterSpec{Locale: "en", Capabilities: []Capability{CapabilityDateTime, CapabilityUnit}})
	if err != nil {
		t.Fatalf("closed capability set = %v", err)
	}
	if !formatter.context.capabilities[CapabilityDateTime] || !formatter.context.capabilities[CapabilityUnit] {
		t.Fatalf("compiled capabilities = %#v", formatter.context.capabilities)
	}
	if _, err := NewFormatter(FormatterSpec{Locale: "en", Capabilities: []Capability{CapabilityUnit, CapabilityUnit}}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("duplicate capability = %v", err)
	}
}
