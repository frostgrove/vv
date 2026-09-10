package i18n

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func localeSnapshot(t *testing.T, localeName, source string, arguments ...ArgumentSpec) *Snapshot {
	t.Helper()
	return mustSnapshot(t, CatalogSpec{
		Revision:        "catalog-1",
		SourceLocale:    localeName,
		DefaultLocale:   localeName,
		DefaultTimeZone: "UTC",
		Supported:       []string{localeName},
		Modules:         []Module{{Name: "app", Messages: []MessageSpec{simpleMessage("m", source, arguments...)}}},
	})
}

func mustView(t *testing.T, snapshot *Snapshot, localeName, formattingLocale, zone string, presentation Presentation) *View {
	t.Helper()
	resolution := snapshot.Resolve(Exact(SourceExplicit, localeName))
	view, err := snapshot.View(ViewSpec{Resolution: resolution, FormattingLocale: formattingLocale, TimeZone: zone, Presentation: presentation})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func mustRender(t *testing.T, view *View, message Message) Rendered {
	t.Helper()
	rendered, err := view.Render(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

func TestExactPluralSelectionPreservesLexicalDecimalsAcrossCLDRLocales(t *testing.T) {
	tests := []struct {
		locale   string
		branches string
		want     map[string]string
	}{
		{locale: "en", branches: "one {{one}}\n* {{other}}", want: map[string]string{"0": "other", "1": "one", "2": "other", "5": "other", "11": "other", "21": "other", "1.0": "other", "1.5": "other"}},
		{locale: "ru", branches: "one {{one}}\nfew {{few}}\nmany {{many}}\n* {{other}}", want: map[string]string{"0": "many", "1": "one", "2": "few", "5": "many", "11": "many", "21": "one", "1.0": "other", "1.5": "other"}},
		{locale: "ar", branches: "zero {{zero}}\none {{one}}\ntwo {{two}}\nfew {{few}}\nmany {{many}}\n* {{other}}", want: map[string]string{"0": "zero", "1": "one", "2": "two", "5": "few", "11": "many", "21": "many", "1.0": "one", "1.5": "other"}},
		{locale: "pl", branches: "one {{one}}\nfew {{few}}\nmany {{many}}\n* {{other}}", want: map[string]string{"0": "many", "1": "one", "2": "few", "5": "many", "11": "many", "21": "many", "1.0": "other", "1.5": "other"}},
	}
	for _, tc := range tests {
		t.Run(tc.locale, func(t *testing.T) {
			template := ".input {$n :number select=plural}\n.match $n\n" + tc.branches
			snapshot := localeSnapshot(t, tc.locale, template, ArgumentSpec{Name: "n", Type: TypeDecimal, Required: true})
			view := mustView(t, snapshot, tc.locale, "", "", PresentationDefault)
			for value, want := range tc.want {
				message, err := snapshot.Bind("app.m", Decimal("n", value))
				if err != nil {
					t.Fatal(err)
				}
				if got := mustRender(t, view, message).Text; got != want {
					t.Errorf("Decimal(%q) = %q, want %q", value, got, want)
				}
			}
		})
	}
}

func TestExactBigIntegerOrdinalNestedAndMultiSelector(t *testing.T) {
	huge := "10000000000000000000000000000000000000000"
	template := ".input {$n :number select=plural}\n.match $n\n" + huge + " {{exact}}\nmany {{many}}\n* {{other}}"
	snapshot := localeSnapshot(t, "ru", template, ArgumentSpec{Name: "n", Type: TypeBigInteger, Required: true})
	integer, _ := new(big.Int).SetString(huge, 10)
	message, err := snapshot.Bind("app.m", BigInteger("n", integer))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRender(t, mustView(t, snapshot, "ru", "", "", PresentationDefault), message).Text; got != "exact" {
		t.Fatalf("huge exact branch = %q", got)
	}

	ordinal := localeSnapshot(t, "en", ".input {$n :number select=ordinal}\n.match $n\none {{st}}\ntwo {{nd}}\nfew {{rd}}\n* {{th}}", ArgumentSpec{Name: "n", Type: TypeInteger, Required: true})
	ordinalView := mustView(t, ordinal, "en", "", "", PresentationDefault)
	for value, want := range map[int64]string{1: "st", 2: "nd", 3: "rd", 4: "th", 11: "th", 21: "st", 22: "nd", 23: "rd"} {
		message, err := ordinal.Bind("app.m", Integer("n", value))
		if err != nil {
			t.Fatal(err)
		}
		if got := mustRender(t, ordinalView, message).Text; got != want {
			t.Errorf("ordinal %d = %q, want %q", value, got, want)
		}
	}

	multiTemplate := ".input {$kind :string}\n.input {$n :number select=plural}\n.local $normalized = {$kind}\n.match $normalized $n\nfemale one {{female-one {|literal|}}}\nfemale * {{female-other {|literal|}}}\n* one {{other-one {|literal|}}}\n* * {{other-other {|literal|}}}"
	multi := localeSnapshot(t, "en", multiTemplate,
		ArgumentSpec{Name: "kind", Type: TypeEnum, Required: true, Values: []string{"female", "male"}},
		ArgumentSpec{Name: "n", Type: TypeInteger, Required: true},
	)
	multiView := mustView(t, multi, "en", "", "", PresentationNoIsolation)
	for _, test := range []struct {
		kind string
		n    int64
		want string
	}{
		{kind: "female", n: 1, want: "female-one literal"},
		{kind: "female", n: 2, want: "female-other literal"},
		{kind: "male", n: 1, want: "other-one literal"},
		{kind: "male", n: 2, want: "other-other literal"},
	} {
		multiMessage, err := multi.Bind("app.m", Enum("kind", test.kind), Integer("n", test.n))
		if err != nil {
			t.Fatal(err)
		}
		if got := mustRender(t, multiView, multiMessage).Text; got != test.want {
			t.Errorf("multi selector %s/%d = %q, want %q", test.kind, test.n, got, test.want)
		}
	}
}

func TestOffsetFormatsExactNumericKindsAndInheritsOptions(t *testing.T) {
	huge := new(big.Int).SetUint64(^uint64(0))
	tests := []struct {
		name      string
		input     ArgumentSpec
		argument  Argument
		operation string
		want      string
	}{
		{name: "signed", input: ArgumentSpec{Name: "n", Type: TypeInteger, Required: true}, argument: Integer("n", 41), operation: "add=1", want: "42"},
		{name: "unsigned crosses zero", input: ArgumentSpec{Name: "n", Type: TypeUnsignedInteger, Required: true}, argument: UnsignedInteger("n", 0), operation: "subtract=1", want: "-1"},
		{name: "unsigned promotes", input: ArgumentSpec{Name: "n", Type: TypeUnsignedInteger, Required: true}, argument: UnsignedInteger("n", ^uint64(0)), operation: "add=1", want: "18446744073709551616"},
		{name: "big integer", input: ArgumentSpec{Name: "n", Type: TypeBigInteger, Required: true}, argument: BigInteger("n", huge), operation: "add=1", want: "18446744073709551616"},
		{name: "decimal precision", input: ArgumentSpec{Name: "n", Type: TypeDecimal, Required: true}, argument: Decimal("n", "1.20"), operation: "add=1", want: "2.20"},
		{name: "negative decimal", input: ArgumentSpec{Name: "n", Type: TypeDecimal, Required: true}, argument: Decimal("n", "-1.20"), operation: "add=1", want: "-0.20"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := ".input {$n :number useGrouping=never}\n.local $adjusted = {$n :offset " + test.operation + "}\n{{{$adjusted}}}"
			snapshot := localeSnapshot(t, "en", template, test.input)
			message, err := snapshot.Bind("app.m", test.argument)
			if err != nil {
				t.Fatal(err)
			}
			got := withoutIsolation(mustRender(t, mustView(t, snapshot, "en", "", "", PresentationDefault), message).Text)
			if got != test.want {
				t.Fatalf("offset output = %q, want %q", got, test.want)
			}
		})
	}

	snapshot := localeSnapshot(t, "en", ".input {$n :integer signDisplay=always}\n.local $adjusted = {$n :offset add=1}\n{{{$adjusted}}}", ArgumentSpec{Name: "n", Type: TypeInteger, Required: true})
	message, err := snapshot.Bind("app.m", Integer("n", 41))
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutIsolation(mustRender(t, mustView(t, snapshot, "en", "", "", PresentationDefault), message).Text); got != "+42" {
		t.Fatalf("offset inherited format = %q, want +42", got)
	}
}

func TestOffsetSelectsAdjustedExactCardinalAndInheritedOrdinalValues(t *testing.T) {
	tests := []struct {
		name     string
		template string
		value    int64
		want     string
	}{
		{
			name:     "exact",
			template: ".input {$n :number}\n.local $adjusted = {$n :offset subtract=6}\n.match $adjusted\n4 {{exact}}\n* {{other}}",
			value:    10,
			want:     "exact",
		},
		{
			name:     "cardinal",
			template: ".input {$n :number}\n.local $adjusted = {$n :offset add=1}\n.match $adjusted\none {{one}}\n* {{other}}",
			value:    0,
			want:     "one",
		},
		{
			name:     "ordinal inherited",
			template: ".input {$n :number select=ordinal}\n.local $adjusted = {$n :offset add=1}\n.match $adjusted\none {{one}}\n* {{other}}",
			value:    20,
			want:     "one",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := localeSnapshot(t, "en", test.template, ArgumentSpec{Name: "n", Type: TypeInteger, Required: true})
			message, err := snapshot.Bind("app.m", Integer("n", test.value))
			if err != nil {
				t.Fatal(err)
			}
			if got := mustRender(t, mustView(t, snapshot, "en", "", "", PresentationDefault), message).Text; got != test.want {
				t.Fatalf("offset selection = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStableMF2NumberOptionsAndNFCStringSelection(t *testing.T) {
	plural := localeSnapshot(t, "en", ".input {$n :number select=plural}\n.match $n\none {{one}}\n* {{other}}", ArgumentSpec{Name: "n", Type: TypeInteger, Required: true})
	message, err := plural.Bind("app.m", Integer("n", 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRender(t, mustView(t, plural, "en", "", "", PresentationDefault), message).Text; got != "one" {
		t.Fatalf("plural selection = %q", got)
	}

	integer := localeSnapshot(t, "en", "{$n :integer maximumSignificantDigits=2 useGrouping=never}", ArgumentSpec{Name: "n", Type: TypeInteger, Required: true})
	message, err = integer.Bind("app.m", Integer("n", 1234))
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutIsolation(mustRender(t, mustView(t, integer, "en", "", "", PresentationDefault), message).Text); got != "1200" {
		t.Fatalf("integer formatting = %q", got)
	}

	inheritedNumber := localeSnapshot(t, "en", ".input {$n :number minimumFractionDigits=2 signDisplay=always}\n{{{$n :number minimumFractionDigits=1}}}", ArgumentSpec{Name: "n", Type: TypeDecimal, Required: true})
	message, err = inheritedNumber.Bind("app.m", Decimal("n", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutIsolation(mustRender(t, mustView(t, inheritedNumber, "en", "", "", PresentationDefault), message).Text); got != "+1.0" {
		t.Fatalf("inherited number options = %q", got)
	}

	inheritedCurrency := localeSnapshot(t, "en", ".input {$n :currency currency=USD fractionDigits=2}\n{{{$n :currency currencySign=accounting}}}", ArgumentSpec{Name: "n", Type: TypeDecimal, Required: true})
	message, err = inheritedCurrency.Bind("app.m", Decimal("n", "-2.5"))
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutIsolation(mustRender(t, mustView(t, inheritedCurrency, "en", "", "", PresentationDefault), message).Text); got != "($2.50)" {
		t.Fatalf("inherited currency options = %q", got)
	}

	currency := localeSnapshot(t, "en", "{$amount :currency fractionDigits=3}", ArgumentSpec{Name: "amount", Type: TypeMoney, Required: true})
	message, err = currency.Bind("app.m", Money("amount", "2.5", "USD"))
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutIsolation(mustRender(t, mustView(t, currency, "en", "", "", PresentationDefault), message).Text); got != "$2.500" {
		t.Fatalf("currency fraction digits = %q", got)
	}

	composed := "é"
	decomposed := "e\u0301"
	selected := localeSnapshot(t, "en", ".input {$value :string}\n.match $value\n|"+composed+"| {{matched}}\n* {{other}}", ArgumentSpec{Name: "value", Type: TypeText, Required: true})
	message, err = selected.Bind("app.m", Text("value", decomposed))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRender(t, mustView(t, selected, "en", "", "", PresentationDefault), message).Text; got != "matched" {
		t.Fatalf("NFC selection = %q", got)
	}
	formatted := localeSnapshot(t, "en", "{$value :string}", ArgumentSpec{Name: "value", Type: TypeText, Required: true})
	message, err = formatted.Bind("app.m", Text("value", decomposed))
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutIsolation(mustRender(t, mustView(t, formatted, "en", "", "", PresentationDefault), message).Text); got != decomposed {
		t.Fatalf("formatted string was normalized: %q", got)
	}

	_, err = New(testCatalog(simpleMessage("decomposed", ".input {$value :string}\n.match $value\n|"+decomposed+"| {{matched}}\n* {{other}}", ArgumentSpec{Name: "value", Type: TypeText, Required: true})))
	if !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("decomposed selector key error = %v", err)
	}
}

func TestPluralGrammarLocaleIsIndependentFromFormattingLocaleAndRounding(t *testing.T) {
	template := ".input {$n :number select=plural maximumFractionDigits=0}\n.match $n\none {{one {$n}}}\nfew {{few {$n}}}\nmany {{many {$n}}}\n* {{other {$n}}}"
	snapshot := localeSnapshot(t, "ru", template, ArgumentSpec{Name: "n", Type: TypeDecimal, Required: true})
	view := mustView(t, snapshot, "ru", "de", "", PresentationDefault)
	message, err := snapshot.Bind("app.m", Decimal("n", "1234.5"))
	if err != nil {
		t.Fatal(err)
	}
	got := withoutIsolation(mustRender(t, view, message).Text)
	if !strings.HasPrefix(got, "many ") || !strings.Contains(got, "1.235") {
		t.Fatalf("split grammar/format output = %q", got)
	}
	one, err := snapshot.Bind("app.m", Decimal("n", "1.0"))
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutIsolation(mustRender(t, view, one).Text); !strings.HasPrefix(got, "one ") {
		t.Fatalf("rounding selector drifted from formatter: %q", got)
	}
}

func TestRichPartsBidiIsolationAndPlainTextSafety(t *testing.T) {
	richSpec := testCatalog(MessageSpec{
		ID:        "rich",
		Revision:  "r",
		Source:    "Hello {#strong}{$name}{/strong}!",
		Arguments: []ArgumentSpec{{Name: "name", Type: TypeText, Required: true}},
		Output:    OutputRich,
		Markup:    []string{"strong"},
	})
	snapshot := mustSnapshot(t, richSpec)
	message, err := snapshot.Bind("app.rich", Text("name", "مرحبا"))
	if err != nil {
		t.Fatal(err)
	}
	defaultRendered := mustRender(t, mustView(t, snapshot, "en", "ar", "", PresentationDefault), message)
	kinds := make([]PartKind, len(defaultRendered.Parts))
	for i, part := range defaultRendered.Parts {
		kinds[i] = part.Kind
	}
	if !slices.Contains(kinds, PartMarkupOpen) || !slices.Contains(kinds, PartMarkupClose) || !slices.Contains(kinds, PartBidiIsolation) {
		t.Fatalf("safe parts = %+v", defaultRendered.Parts)
	}
	if strings.Contains(defaultRendered.Text, "strong") {
		t.Fatalf("markup leaked into text: %q", defaultRendered.Text)
	}
	plain := withoutIsolation(defaultRendered.Text)
	if plain != "Hello مرحبا!" {
		t.Fatalf("plain projection = %q", plain)
	}
	noIsolation := mustRender(t, mustView(t, snapshot, "en", "ar", "", PresentationNoIsolation), message)
	for _, part := range noIsolation.Parts {
		if part.Kind == PartBidiIsolation {
			t.Fatal("no-isolation view retained bidi controls")
		}
	}
	if strings.ContainsAny(noIsolation.Text, "\u2066\u2067\u2068\u2069") {
		t.Fatalf("no-isolation text retained controls: %q", noIsolation.Text)
	}

	plainSnapshot := localeSnapshot(t, "en", "{$value}", ArgumentSpec{Name: "value", Type: TypeText, Required: true})
	plainMessage, err := plainSnapshot.Bind("app.m", Text("value", "<script>&"))
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutIsolation(mustRender(t, mustView(t, plainSnapshot, "en", "", "", PresentationDefault), plainMessage).Text); got != "<script>&" {
		t.Fatalf("plain output was escaped or trusted as markup: %q", got)
	}

	for _, source := range []string{"{#em}x{/em}", "{#strong}x{/em}", "{#strong}x"} {
		spec := testCatalog(MessageSpec{ID: "bad", Revision: "r", Source: source, Output: OutputRich, Markup: []string{"strong"}})
		if _, err := New(spec); err == nil {
			t.Fatalf("forbidden or unbalanced markup accepted: %q", source)
		}
	}
}

func TestDateInstantMoneyPercentAndExplicitZone(t *testing.T) {
	spec := testCatalog(MessageSpec{
		ID:       "values",
		Revision: "r",
		Source:   "{$date :date dateStyle=medium}|{$instant :datetime dateStyle=medium timeStyle=short}|{$money :currency}|{$rate :percent}",
		Arguments: []ArgumentSpec{
			{Name: "date", Type: TypeDate, Required: true},
			{Name: "instant", Type: TypeInstant, Required: true},
			{Name: "money", Type: TypeMoney, Required: true},
			{Name: "rate", Type: TypeDecimal, Required: true},
		},
	})
	spec.Capabilities = []Capability{CapabilityDateTime}
	spec.TimeZoneDataVersion = "test-tzdb-2026a"
	snapshot := mustSnapshot(t, spec)
	message, err := snapshot.Bind("app.values",
		Date("date", 2024, time.January, 2),
		Instant("instant", time.Date(2024, time.January, 2, 1, 30, 0, 0, time.UTC)),
		Money("money", "12.50", "usd"),
		Decimal("rate", "0.25"),
	)
	if err != nil {
		t.Fatal(err)
	}
	view := mustView(t, snapshot, "en", "en", "Pacific/Honolulu", PresentationNoIsolation)
	got := mustRender(t, view, message).Text
	if !strings.Contains(got, "Jan 2, 2024") || !strings.Contains(got, "Jan 1, 2024") || !strings.Contains(got, "$12.50") || !strings.Contains(got, "25%") {
		t.Fatalf("typed formatting = %q", got)
	}
}

func TestRenderBudgetsCancellationNilContextAndObservation(t *testing.T) {
	var mu sync.Mutex
	observations := make([]Observation, 0)
	spec := testCatalog(simpleMessage("m", "abcdef"))
	spec.Limits.MaxOutputBytes = 4
	spec.Observer = func(_ context.Context, observation Observation) {
		mu.Lock()
		observations = append(observations, observation)
		mu.Unlock()
		panic("isolated")
	}
	snapshot := mustSnapshot(t, spec)
	resolution := snapshot.Resolve(Exact(SourceExplicit, "en"))
	view, err := snapshot.View(ViewSpec{Resolution: resolution})
	if err != nil {
		t.Fatal(err)
	}
	message, err := snapshot.Bind("app.m")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.Render(nil, message); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("output budget error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := view.Render(canceled, message); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled render error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observations) != 3 {
		t.Fatalf("observations = %d, want resolve + 2 renders", len(observations))
	}
	if observations[1].Outcome != OutcomeLimited || observations[1].Reason != ReasonOutputLimit || observations[2].Outcome != OutcomeCanceled {
		t.Fatalf("render observations = %+v", observations)
	}
}

func TestRenderKeyIncludesEveryOutputAffectingInput(t *testing.T) {
	spec := testCatalog(MessageSpec{
		ID:       "all",
		Revision: "r",
		Source:   "{$text}|{$bool}|{$integer :number}|{$unsigned :number}|{$big :number}|{$decimal :number}|{$money :currency}|{$date :date}|{$instant :datetime}|{$enum}",
		Arguments: []ArgumentSpec{
			{Name: "text", Type: TypeText, Required: true},
			{Name: "bool", Type: TypeBool, Required: true},
			{Name: "integer", Type: TypeInteger, Required: true},
			{Name: "unsigned", Type: TypeUnsignedInteger, Required: true},
			{Name: "big", Type: TypeBigInteger, Required: true},
			{Name: "decimal", Type: TypeDecimal, Required: true},
			{Name: "money", Type: TypeMoney, Required: true},
			{Name: "date", Type: TypeDate, Required: true},
			{Name: "instant", Type: TypeInstant, Required: true},
			{Name: "enum", Type: TypeEnum, Required: true, Values: []string{"a", "b"}},
		},
	})
	spec.Capabilities = []Capability{CapabilityDateTime}
	spec.TimeZoneDataVersion = "test-tzdb-2026a"
	snapshot := mustSnapshot(t, spec)
	base := []Argument{
		Text("text", "a"), Bool("bool", false), Integer("integer", -1), UnsignedInteger("unsigned", 1),
		BigInteger("big", big.NewInt(2)), Decimal("decimal", "1.0"), Money("money", "2.50", "USD"),
		Date("date", 2024, time.January, 2), Instant("instant", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)), Enum("enum", "a"),
	}
	message, err := snapshot.Bind("app.all", base...)
	if err != nil {
		t.Fatal(err)
	}
	view := mustView(t, snapshot, "en", "en", "UTC", PresentationDefault)
	key, err := view.RenderKey(message)
	if err != nil {
		t.Fatal(err)
	}
	if repeated, _ := view.RenderKey(message); repeated != key {
		t.Fatal("render key is not deterministic")
	}
	changes := []struct {
		index int
		value Argument
	}{
		{0, Text("text", "b")}, {1, Bool("bool", true)}, {2, Integer("integer", -2)}, {3, UnsignedInteger("unsigned", 2)},
		{4, BigInteger("big", big.NewInt(3))}, {5, Decimal("decimal", "1.00")}, {6, Money("money", "2.50", "EUR")},
		{7, Date("date", 2024, time.January, 3)}, {8, Instant("instant", time.Date(2024, 1, 2, 3, 4, 6, 0, time.UTC))}, {9, Enum("enum", "b")},
	}
	for _, change := range changes {
		arguments := slices.Clone(base)
		arguments[change.index] = change.value
		changed, err := snapshot.Bind("app.all", arguments...)
		if err != nil {
			t.Fatal(err)
		}
		changedKey, err := view.RenderKey(changed)
		if err != nil {
			t.Fatal(err)
		}
		if changedKey == key {
			t.Fatalf("argument index %d did not change render key", change.index)
		}
	}
	exactResolution := snapshot.Resolve(Exact(SourceExplicit, "en"))
	viewCases := []ViewSpec{
		{Resolution: snapshot.Resolve(Exact(SourceUser, "en")), FormattingLocale: "en", TimeZone: "UTC"},
		{Resolution: snapshot.Resolve(Exact(SourceExplicit, "en-US")), FormattingLocale: "en", TimeZone: "UTC"},
		{Resolution: exactResolution, FormattingLocale: "de", TimeZone: "UTC"},
		{Resolution: exactResolution, FormattingLocale: "en", TimeZone: "+05:00"},
		{Resolution: exactResolution, FormattingLocale: "en", TimeZone: "UTC", Presentation: PresentationNoIsolation},
	}
	for i, viewSpec := range viewCases {
		other, err := snapshot.View(viewSpec)
		if err != nil {
			t.Fatal(err)
		}
		otherKey, err := other.RenderKey(message)
		if err != nil {
			t.Fatal(err)
		}
		if otherKey == key {
			t.Fatalf("view change %d did not change render key", i)
		}
	}
}

func TestZeroViewAndSchemaMismatchNeverPanic(t *testing.T) {
	var view *View
	if _, err := view.Render(nil, Message{}); err == nil {
		t.Fatal("nil view render succeeded")
	}
	if _, err := view.RenderKey(Message{}); err == nil {
		t.Fatal("nil view render key succeeded")
	}
	if _, err := view.Explain(Message{}); err == nil {
		t.Fatal("nil view explain succeeded")
	}
	snapshot := localeSnapshot(t, "en", "ok")
	realView := mustView(t, snapshot, "en", "", "", PresentationDefault)
	if _, err := realView.Render(context.Background(), Message{key: "app.m", revision: "wrong"}); err == nil {
		t.Fatal("schema mismatch succeeded")
	}
}

func withoutIsolation(value string) string {
	return strings.NewReplacer("\u2066", "", "\u2067", "", "\u2068", "", "\u2069", "").Replace(value)
}

func FuzzDecimalConstructionAndRenderKeyNeverPanic(f *testing.F) {
	f.Add("1.0")
	f.Add("-1.5e2")
	f.Add("NaN")
	snapshot := localeSnapshotForFuzz(f, "en", "{$n :number}", ArgumentSpec{Name: "n", Type: TypeDecimal, Required: true})
	view := mustViewForFuzz(f, snapshot)
	f.Fuzz(func(t *testing.T, lexical string) {
		message, err := snapshot.Bind("app.m", Decimal("n", lexical))
		if err != nil {
			return
		}
		if _, err := view.RenderKey(message); err != nil {
			t.Fatal(err)
		}
	})
}

func localeSnapshotForFuzz(f *testing.F, localeName, source string, arguments ...ArgumentSpec) *Snapshot {
	f.Helper()
	snapshot, err := New(CatalogSpec{
		Revision: "r", SourceLocale: localeName, DefaultLocale: localeName, Supported: []string{localeName},
		Modules: []Module{{Name: "app", Messages: []MessageSpec{simpleMessage("m", source, arguments...)}}},
	})
	if err != nil {
		f.Fatal(err)
	}
	return snapshot
}

func mustViewForFuzz(f *testing.F, snapshot *Snapshot) *View {
	f.Helper()
	view, err := snapshot.View(ViewSpec{Resolution: snapshot.Resolve(Exact(SourceExplicit, "en"))})
	if err != nil {
		f.Fatal(err)
	}
	return view
}
