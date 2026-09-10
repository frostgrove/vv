package i18n

import (
	"context"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type structText string
type structBool bool
type structInteger int64
type structUnsigned uint64
type structDecimal string
type structEnum string
type structMoneyAlias MoneyValue
type structDateAlias DateValue
type structInstantAlias time.Time

type allStructArguments struct {
	Mode       structEnum
	OccurredAt time.Time
	BirthDate  DateValue
	Price      MoneyValue
	Ratio      structDecimal
	Huge       *big.Int
	Total      structUnsigned
	Delta      structInteger
	Ready      structBool
	Title      structText
}

type allOptionalStructArguments struct {
	Title      Optional[structText]
	Ready      Optional[structBool]
	Delta      Optional[structInteger]
	Total      Optional[structUnsigned]
	Huge       Optional[*big.Int]
	Ratio      Optional[structDecimal]
	Price      Optional[MoneyValue]
	BirthDate  Optional[DateValue]
	OccurredAt Optional[time.Time]
	Mode       Optional[structEnum]
}

func allStructArgumentSpecs() []ArgumentSpec {
	return []ArgumentSpec{
		{Name: "title", Type: TypeText, Required: true},
		{Name: "ready", Type: TypeBool, Required: true},
		{Name: "delta", Type: TypeInteger, Required: true},
		{Name: "total", Type: TypeUnsignedInteger, Required: true},
		{Name: "huge", Type: TypeBigInteger, Required: true},
		{Name: "ratio", Type: TypeDecimal, Required: true},
		{Name: "price", Type: TypeMoney, Required: true},
		{Name: "birth_date", Type: TypeDate, Required: true},
		{Name: "occurred-at", Type: TypeInstant, Required: true},
		{Name: "mode", Type: TypeEnum, Required: true, Values: []string{"quiet", "loud"}},
	}
}

func TestStructDefinitionBindsEveryExactArgumentTypeInDescriptorOrder(t *testing.T) {
	spec := testCatalog(simpleMessage("all", "ok", allStructArgumentSpecs()...))
	spec.Capabilities = []Capability{CapabilityDateTime, CapabilityUnit}
	snapshot := mustSnapshot(t, spec)
	contract, ok := snapshot.ContractRef("app.all")
	if !ok {
		t.Fatal("contract is missing")
	}
	definition, err := DefineStruct[allStructArguments](snapshot, contract)
	if err != nil {
		t.Fatal(err)
	}
	huge := new(big.Int).Lsh(big.NewInt(1), 200)
	instant := time.Date(2026, time.September, 9, 12, 30, 0, 123, time.UTC)
	message, err := definition.Bind(allStructArguments{
		Title:      "hello",
		Ready:      true,
		Delta:      math.MinInt64,
		Total:      math.MaxUint64,
		Huge:       huge,
		Ratio:      "1.0",
		Price:      MoneyValue{Amount: "9007199254740993.01", Currency: "usd"},
		BirthDate:  DateValue{Year: 2000, Month: time.February, Day: 29},
		OccurredAt: instant,
		Mode:       "loud",
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := message.Arguments()
	if len(arguments) != len(allStructArgumentSpecs()) {
		t.Fatalf("argument count = %d", len(arguments))
	}
	for index, spec := range allStructArgumentSpecs() {
		if arguments[index].Name() != spec.Name || arguments[index].Type() != spec.Type {
			t.Fatalf("argument %d = %q/%s, want %q/%s", index, arguments[index].Name(), arguments[index].Type(), spec.Name, spec.Type)
		}
	}
	if arguments[2].signed != math.MinInt64 || arguments[3].unsigned != math.MaxUint64 || arguments[4].big.Cmp(huge) != 0 {
		t.Fatal("exact integer values changed")
	}
	if arguments[5].text != "1.0" || arguments[6].text != "9007199254740993.01\x00USD" {
		t.Fatalf("exact decimal values = %q, %q", arguments[5].text, arguments[6].text)
	}
	if arguments[7].date != (DateValue{Year: 2000, Month: time.February, Day: 29}) || !arguments[8].instant.Equal(instant) || arguments[9].text != "loud" {
		t.Fatal("structured values changed")
	}
	huge.SetInt64(1)
	arguments[4].big.SetInt64(2)
	if message.Arguments()[4].big.BitLen() <= 2 {
		t.Fatal("bound big integer aliases caller or returned arguments")
	}
}

func TestStructDefinitionBindsOptionalFormsOfEveryArgumentType(t *testing.T) {
	specs := allStructArgumentSpecs()
	for index := range specs {
		specs[index].Required = false
	}
	spec := testCatalog(simpleMessage("all_optional", "ok", specs...))
	spec.Capabilities = []Capability{CapabilityDateTime, CapabilityUnit}
	snapshot := mustSnapshot(t, spec)
	definition, err := NewStructDefinition[allOptionalStructArguments](snapshot, "app.all_optional")
	if err != nil {
		t.Fatal(err)
	}
	message, err := definition.Bind(allOptionalStructArguments{
		Title:      Some(structText("hello")),
		Ready:      Some(structBool(true)),
		Delta:      Some(structInteger(math.MaxInt64)),
		Total:      Some(structUnsigned(math.MaxUint64)),
		Huge:       Some(new(big.Int).Lsh(big.NewInt(1), 200)),
		Ratio:      Some(structDecimal("1.0")),
		Price:      Some(MoneyValue{Amount: "2.00", Currency: "EUR"}),
		BirthDate:  Some(DateValue{Year: 2024, Month: time.February, Day: 29}),
		OccurredAt: Some(time.Date(2026, time.September, 9, 1, 2, 3, 4, time.UTC)),
		Mode:       Some(structEnum("quiet")),
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := message.Arguments()
	if len(arguments) != len(specs) {
		t.Fatalf("argument count = %d", len(arguments))
	}
	for index, argument := range arguments {
		if argument.Name() != specs[index].Name || argument.Type() != specs[index].Type {
			t.Fatalf("argument %d = %q/%s", index, argument.Name(), argument.Type())
		}
	}
}

type presenceStructArguments struct {
	OptionalNullable Optional[string]
	RequiredNullable Optional[string]
	OptionalValue    Optional[string]
	RequiredValue    string
}

func presenceSnapshot(t *testing.T) *Snapshot {
	t.Helper()
	return mustSnapshot(t, testCatalog(simpleMessage("presence", "ok",
		ArgumentSpec{Name: "required_value", Type: TypeText, Required: true},
		ArgumentSpec{Name: "optional_value", Type: TypeText},
		ArgumentSpec{Name: "required_nullable", Type: TypeText, Required: true, Nullable: true},
		ArgumentSpec{Name: "optional_nullable", Type: TypeText, Nullable: true},
	)))
}

func TestStructDefinitionPreservesAbsentNullAndPresentStates(t *testing.T) {
	snapshot := presenceSnapshot(t)
	definition, err := NewStructDefinition[presenceStructArguments](snapshot, "app.presence")
	if err != nil {
		t.Fatal(err)
	}
	message, err := definition.Bind(presenceStructArguments{
		RequiredValue:    "required",
		OptionalValue:    Some("present"),
		RequiredNullable: NullValue[string](),
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := message.Arguments()
	if len(arguments) != 3 || arguments[0].text != "required" || arguments[1].text != "present" || !arguments[2].IsNull() {
		t.Fatalf("presence arguments = %#v", arguments)
	}
	if _, err := definition.Bind(presenceStructArguments{RequiredValue: "required"}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("absent required nullable error = %v", err)
	}
	if _, err := definition.Bind(presenceStructArguments{
		RequiredValue:    "required",
		RequiredNullable: Some("value"),
		OptionalValue:    NullValue[string](),
	}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("null non-nullable error = %v", err)
	}
}

func TestStructDefinitionMatchesFieldsForgivinglyAndTagsExplicitOverrides(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("names", "ok",
		ArgumentSpec{Name: "customer_id", Type: TypeUnsignedInteger, Required: true},
		ArgumentSpec{Name: "display-name", Type: TypeText, Required: true},
	)))
	type arguments struct {
		Ignored    string `i18n:"-"`
		Extra      string
		Label      string `i18n:"display-name"`
		CustomerID uint64
	}
	definition, err := NewStructDefinition[arguments](snapshot, "app.names")
	if err != nil {
		t.Fatal(err)
	}
	message, err := definition.Bind(arguments{CustomerID: 42, Label: "Ada", Extra: "ignored", Ignored: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	got := message.Arguments()
	if len(got) != 2 || got[0].unsigned != 42 || got[1].text != "Ada" {
		t.Fatalf("arguments = %#v", got)
	}
}

func TestStructDefinitionRejectsInvalidMappingsAtConstruction(t *testing.T) {
	one := mustSnapshot(t, testCatalog(simpleMessage("one", "ok", ArgumentSpec{Name: "value", Type: TypeText, Required: true})))
	two := mustSnapshot(t, testCatalog(simpleMessage("two", "ok",
		ArgumentSpec{Name: "user_id", Type: TypeText, Required: true},
		ArgumentSpec{Name: "userid", Type: TypeText, Required: true},
	)))
	type missing struct{ Other string }
	type unknown struct {
		Value string `i18n:"missing"`
	}
	type duplicate struct {
		Value string
		Other string `i18n:"value"`
	}
	type malformed struct {
		Value string `i18n:"value,omitempty"`
	}
	type emptyTag struct {
		Value string `i18n:""`
	}
	type private struct {
		value string `i18n:"value"`
	}
	type embeddedValue struct{ Value string }
	type embedded struct {
		embeddedValue `i18n:"value"`
	}
	type ambiguous struct{ UserID string }
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "missing", run: func() error { _, err := NewStructDefinition[missing](one, "app.one"); return err }},
		{name: "unknown", run: func() error { _, err := NewStructDefinition[unknown](one, "app.one"); return err }},
		{name: "duplicate", run: func() error { _, err := NewStructDefinition[duplicate](one, "app.one"); return err }},
		{name: "malformed", run: func() error { _, err := NewStructDefinition[malformed](one, "app.one"); return err }},
		{name: "empty tag", run: func() error { _, err := NewStructDefinition[emptyTag](one, "app.one"); return err }},
		{name: "unexported", run: func() error { _, err := NewStructDefinition[private](one, "app.one"); return err }},
		{name: "embedded", run: func() error { _, err := NewStructDefinition[embedded](one, "app.one"); return err }},
		{name: "ambiguous", run: func() error { _, err := NewStructDefinition[ambiguous](two, "app.two"); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("construction panicked: %v", recovered)
				}
			}()
			if err := test.run(); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := NewStructDefinition[struct {
		First  string `i18n:"user_id"`
		Second string `i18n:"userid"`
	}](two, "app.two"); err != nil {
		t.Fatalf("explicit tags did not resolve folded ambiguity: %v", err)
	}
}

func TestStructDefinitionRejectsWrongGoShapesAndTypesAtConstruction(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("typed", "ok", ArgumentSpec{Name: "value", Type: TypeInteger, Required: true})))
	type wrongWidth struct{ Value int }
	type wrongPresence struct{ Value Optional[int64] }
	type valueStruct struct{ Value int64 }
	type tooMany struct {
		Value int64
		Extra string
	}
	tests := []struct {
		name string
		run  func() error
		want error
	}{
		{name: "pointer", run: func() error { _, err := NewStructDefinition[*valueStruct](snapshot, "app.typed"); return err }, want: ErrInvalidMessage},
		{name: "interface", run: func() error { _, err := NewStructDefinition[any](snapshot, "app.typed"); return err }, want: ErrInvalidMessage},
		{name: "map", run: func() error { _, err := NewStructDefinition[map[string]int64](snapshot, "app.typed"); return err }, want: ErrInvalidMessage},
		{name: "integer width", run: func() error { _, err := NewStructDefinition[wrongWidth](snapshot, "app.typed"); return err }, want: ErrInvalidMessage},
		{name: "wrong presence", run: func() error { _, err := NewStructDefinition[wrongPresence](snapshot, "app.typed"); return err }, want: ErrInvalidMessage},
	}
	limitedSpec := testCatalog(simpleMessage("typed", "ok", ArgumentSpec{Name: "value", Type: TypeInteger, Required: true}))
	limitedSpec.Limits.MaxArguments = 1
	limited := mustSnapshot(t, limitedSpec)
	tests = append(tests, struct {
		name string
		run  func() error
		want error
	}{name: "field bound", run: func() error { _, err := NewStructDefinition[tooMany](limited, "app.typed"); return err }, want: ErrLimitExceeded})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestStructDefinitionRejectsLossyOrStructuralTypeCoercions(t *testing.T) {
	tests := []struct {
		name         string
		argumentType ArgumentType
		run          func(*Snapshot) error
	}{
		{name: "text bytes", argumentType: TypeText, run: func(snapshot *Snapshot) error {
			_, err := NewStructDefinition[struct{ Value []byte }](snapshot, "app.typed")
			return err
		}},
		{name: "bool integer", argumentType: TypeBool, run: func(snapshot *Snapshot) error {
			_, err := NewStructDefinition[struct{ Value int }](snapshot, "app.typed")
			return err
		}},
		{name: "integer native width", argumentType: TypeInteger, run: func(snapshot *Snapshot) error {
			_, err := NewStructDefinition[struct{ Value int }](snapshot, "app.typed")
			return err
		}},
		{name: "unsigned native width", argumentType: TypeUnsignedInteger, run: func(snapshot *Snapshot) error {
			_, err := NewStructDefinition[struct{ Value uint }](snapshot, "app.typed")
			return err
		}},
		{name: "big integer value", argumentType: TypeBigInteger, run: func(snapshot *Snapshot) error {
			_, err := NewStructDefinition[struct{ Value big.Int }](snapshot, "app.typed")
			return err
		}},
		{name: "decimal float", argumentType: TypeDecimal, run: func(snapshot *Snapshot) error {
			_, err := NewStructDefinition[struct{ Value float64 }](snapshot, "app.typed")
			return err
		}},
		{name: "money structural alias", argumentType: TypeMoney, run: func(snapshot *Snapshot) error {
			_, err := NewStructDefinition[struct{ Value structMoneyAlias }](snapshot, "app.typed")
			return err
		}},
		{name: "date structural alias", argumentType: TypeDate, run: func(snapshot *Snapshot) error {
			_, err := NewStructDefinition[struct{ Value structDateAlias }](snapshot, "app.typed")
			return err
		}},
		{name: "instant structural alias", argumentType: TypeInstant, run: func(snapshot *Snapshot) error {
			_, err := NewStructDefinition[struct{ Value structInstantAlias }](snapshot, "app.typed")
			return err
		}},
		{name: "enum integer", argumentType: TypeEnum, run: func(snapshot *Snapshot) error {
			_, err := NewStructDefinition[struct{ Value int64 }](snapshot, "app.typed")
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			argument := ArgumentSpec{Name: "value", Type: test.argumentType, Required: true}
			if test.argumentType == TypeEnum {
				argument.Values = []string{"valid"}
			}
			spec := testCatalog(simpleMessage("typed", "ok", argument))
			if test.argumentType == TypeDate || test.argumentType == TypeInstant {
				spec.Capabilities = []Capability{CapabilityDateTime}
			}
			snapshot := mustSnapshot(t, spec)
			if err := test.run(snapshot); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestStructDefinitionBoundsReflectiveNamesBeforeFoldingOrDiagnostics(t *testing.T) {
	spec := testCatalog(simpleMessage("typed", "ok", ArgumentSpec{Name: "value", Type: TypeText, Required: true}))
	spec.Limits.MaxIdentifierBytes = len("app.typed")
	snapshot := mustSnapshot(t, spec)
	const longName = "ValueWithAnIntentionallyOversizedCompiledGoIdentifier"
	type automatic struct {
		ValueWithAnIntentionallyOversizedCompiledGoIdentifier string
	}
	if _, err := NewStructDefinition[automatic](snapshot, "app.typed"); !errors.Is(err, ErrInvalidMessage) || strings.Contains(err.Error(), longName) {
		t.Fatalf("automatic oversized field error = %v", err)
	}
	type tagged struct {
		ValueWithAnIntentionallyOversizedCompiledGoIdentifier string `i18n:"value"`
	}
	definition, err := NewStructDefinition[tagged](snapshot, "app.typed")
	if err != nil {
		t.Fatalf("bounded explicit tag was rejected: %v", err)
	}
	if _, err := definition.Bind(tagged{ValueWithAnIntentionallyOversizedCompiledGoIdentifier: "ok"}); err != nil {
		t.Fatal(err)
	}
	type oversizedTag struct {
		Value string `i18n:"value_over"`
	}
	if _, err := NewStructDefinition[oversizedTag](snapshot, "app.typed"); !errors.Is(err, ErrInvalidMessage) || strings.Contains(err.Error(), "value_over") {
		t.Fatalf("oversized tag error = %v", err)
	}
}

func TestStructDefinitionPreflightsBoundedKeysBeforeLookup(t *testing.T) {
	const maximum = 16
	const messageID = "abcdefghijkl"
	spec := testCatalog(simpleMessage(messageID, "ok", ArgumentSpec{Name: "value", Type: TypeText, Required: true}))
	spec.Limits.MaxIdentifierBytes = maximum
	snapshot := mustSnapshot(t, spec)
	type arguments struct{ Value string }
	atBound := Key("app." + messageID)
	if len(atBound) != maximum {
		t.Fatalf("test key length = %d", len(atBound))
	}
	if _, err := NewStructDefinition[arguments](snapshot, atBound); err != nil {
		t.Fatalf("at-bound NewStructDefinition error = %v", err)
	}
	contract, ok := snapshot.ContractRef(atBound)
	if !ok {
		t.Fatal("at-bound contract is missing")
	}
	if _, err := DefineStruct[arguments](snapshot, contract); err != nil {
		t.Fatalf("at-bound DefineStruct error = %v", err)
	}

	oneOver := Key(strings.Repeat("x", maximum+1))
	for name, run := range map[string]func() error{
		"lookup constructor": func() error {
			_, err := NewStructDefinition[arguments](snapshot, oneOver)
			return err
		},
		"pinned constructor": func() error {
			_, err := DefineStruct[arguments](snapshot, ContractRef{Key: oneOver, Revision: "r", Digest: "d"})
			return err
		},
		"explicit constructor": func() error {
			_, err := NewDefinition(snapshot, oneOver, func(arguments) ([]Argument, error) { return nil, nil })
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := run()
			if !errors.Is(err, ErrLimitExceeded) || strings.Contains(err.Error(), string(oneOver)) {
				t.Fatalf("one-over error = %v", err)
			}
		})
	}
	invalid := Key("invalid key")
	if _, err := NewStructDefinition[arguments](snapshot, invalid); !errors.Is(err, ErrInvalidMessage) || strings.Contains(err.Error(), string(invalid)) {
		t.Fatalf("invalid key error = %v", err)
	}
}

func TestStructDefinitionBoundsWholeRawStructTagBeforeLookup(t *testing.T) {
	type atBound struct {
		Value string `json:"a" i18n:"value"`
	}
	type oneOver struct {
		Value string `json:"aa" i18n:"value"`
	}
	atBoundTag := reflect.TypeFor[atBound]().Field(0).Tag
	oneOverTag := reflect.TypeFor[oneOver]().Field(0).Tag
	if len(oneOverTag) != len(atBoundTag)+1 {
		t.Fatalf("test tag lengths = %d/%d", len(atBoundTag), len(oneOverTag))
	}
	spec := testCatalog(simpleMessage("typed", "ok", ArgumentSpec{Name: "value", Type: TypeText, Required: true}))
	spec.Limits.MaxDescriptionBytes = len(atBoundTag)
	snapshot := mustSnapshot(t, spec)
	if _, err := NewStructDefinition[atBound](snapshot, "app.typed"); err != nil {
		t.Fatalf("at-bound raw tag error = %v", err)
	}
	if _, err := NewStructDefinition[oneOver](snapshot, "app.typed"); !errors.Is(err, ErrLimitExceeded) || strings.Contains(err.Error(), string(oneOverTag)) {
		t.Fatalf("one-over raw tag error = %v", err)
	}
}

func TestStructDefinitionStrictlyParsesEveryStructTagBeforeMapping(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("typed", "ok", ArgumentSpec{Name: "value", Type: TypeText, Required: true})))
	descriptor, ok := snapshot.Descriptor("app.typed")
	if !ok {
		t.Fatal("descriptor is missing")
	}
	malformed := map[string]string{
		"missing colon":      `i18n"value"`,
		"space before colon": `i18n :"value"`,
		"unquoted value":     `i18n:value`,
		"unterminated value": `i18n:"value`,
		"invalid escape":     `i18n:"\q"`,
		"malformed before":   `json:value i18n:"value"`,
		"malformed after":    `i18n:"value" broken`,
		"tab separator":      "json:\"value\"\ti18n:\"value\"",
		"duplicate i18n":     `i18n:"value" json:"value" i18n:"value"`,
	}
	for name, raw := range malformed {
		t.Run(name, func(t *testing.T) {
			shape := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(raw)}})
			if _, err := compileStructArgumentFields(shape, descriptor, snapshot.limits); !errors.Is(err, ErrInvalidMessage) || strings.Contains(err.Error(), raw) {
				t.Fatalf("malformed tag error = %v", err)
			}
		})
	}

	valid := []struct {
		raw     string
		value   string
		present bool
	}{
		{raw: `json:"value,omitempty" xml:"value,omitempty" validate:"required" i18n:"value"`, value: "value", present: true},
		{raw: `json:"say\"hello,omitempty" i18n:"value"`, value: "value", present: true},
		{raw: `i18n:"\x76alue"`, value: "value", present: true},
		{raw: `json:"value"i18n:"value"`, value: "value", present: true},
		{raw: `  json:"value"   i18n:"value"  `, value: "value", present: true},
		{raw: `json:"value,omitempty" validate:"required"`},
	}
	for _, test := range valid {
		value, present, err := parseI18nStructTag(reflect.StructTag(test.raw))
		if err != nil || value != test.value || present != test.present {
			t.Fatalf("valid tag %q = %q, %t, %v", test.raw, value, present, err)
		}
	}

	type arguments struct {
		Value string `json:"value,omitempty" i18n:"\x76alue" validate:"required"`
	}
	definition, err := NewStructDefinition[arguments](snapshot, "app.typed")
	if err != nil {
		t.Fatal(err)
	}
	message, err := definition.Bind(arguments{Value: "exact"})
	if err != nil {
		t.Fatal(err)
	}
	bound := message.Arguments()
	if len(bound) != 1 || bound[0].Name() != "value" || bound[0].text != "exact" {
		t.Fatalf("bound arguments = %#v", bound)
	}
}

func TestStructDefinitionChecksContractBeforeFirstBind(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("contract", "ok", ArgumentSpec{Name: "value", Type: TypeText, Required: true})))
	type arguments struct{ Value string }
	contract, _ := snapshot.ContractRef("app.contract")
	staleRevision := contract
	staleRevision.Revision = "stale"
	staleDigest := contract
	staleDigest.Digest = "stale"
	for name, run := range map[string]func() error{
		"nil snapshot":   func() error { _, err := DefineStruct[arguments](nil, contract); return err },
		"stale revision": func() error { _, err := DefineStruct[arguments](snapshot, staleRevision); return err },
		"stale digest":   func() error { _, err := DefineStruct[arguments](snapshot, staleDigest); return err },
		"missing key":    func() error { _, err := NewStructDefinition[arguments](snapshot, "app.missing"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("invalid definition was accepted")
			}
		})
	}
}

func TestStructDefinitionRuntimeValidationAndExplicitDefinitionParity(t *testing.T) {
	snapshot := localeSnapshot(t, "en", "Hello, {$name}!", ArgumentSpec{Name: "name", Type: TypeText, Required: true})
	type arguments struct{ Name string }
	reflected, err := NewStructDefinition[arguments](snapshot, "app.m")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := NewDefinition(snapshot, Key("app.m"), func(value arguments) ([]Argument, error) {
		return []Argument{Text("name", value.Name)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reflectedMessage, err := reflected.Bind(arguments{Name: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	explicitMessage, err := explicit.Bind(arguments{Name: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reflectedMessage.Arguments(), explicitMessage.Arguments()) {
		t.Fatal("reflective and explicit arguments differ")
	}
	view := mustView(t, snapshot, "en", "", "", PresentationDefault)
	reflectedRendered, err := view.Render(context.Background(), reflectedMessage)
	if err != nil {
		t.Fatal(err)
	}
	explicitRendered, err := view.Render(context.Background(), explicitMessage)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reflectedRendered, explicitRendered) || reflectedRendered.Text != "Hello, \u2068Ada\u2069!" {
		t.Fatalf("render parity = %#v / %#v", reflectedRendered, explicitRendered)
	}
}

func TestStructDefinitionRejectsInvalidRuntimeValuesAndBindsConcurrently(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("runtime", "ok",
		ArgumentSpec{Name: "huge", Type: TypeBigInteger, Required: true},
		ArgumentSpec{Name: "mode", Type: TypeEnum, Required: true, Values: []string{"valid"}},
	)))
	type arguments struct {
		Huge *big.Int
		Mode string
	}
	definition, err := NewStructDefinition[arguments](snapshot, "app.runtime")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Bind(arguments{Mode: "valid"}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("typed nil error = %v", err)
	}
	if _, err := definition.Bind(arguments{Huge: big.NewInt(1), Mode: "invalid"}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("enum error = %v", err)
	}
	const workers = 64
	var group sync.WaitGroup
	errorsOut := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func(value int64) {
			defer group.Done()
			message, bindErr := definition.Bind(arguments{Huge: big.NewInt(value), Mode: "valid"})
			if bindErr != nil {
				errorsOut <- bindErr
				return
			}
			if got := message.Arguments()[0].big.Int64(); got != value {
				errorsOut <- errors.New("concurrent value changed")
			}
		}(int64(worker + 1))
	}
	group.Wait()
	close(errorsOut)
	for err := range errorsOut {
		t.Error(err)
	}
}

func TestStructDefinitionRejectsCorruptOptionalStateWithoutPanicking(t *testing.T) {
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("optional", "ok", ArgumentSpec{Name: "value", Type: TypeText})))
	type arguments struct{ Value Optional[string] }
	definition, err := NewStructDefinition[arguments](snapshot, "app.optional")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Bind panicked: %v", recovered)
		}
	}()
	if _, err := definition.Bind(arguments{Value: Optional[string]{state: 3}}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("error = %v", err)
	}
}

func TestStructDefinitionsDoNotSharePlansAcrossSnapshots(t *testing.T) {
	first := mustSnapshot(t, testCatalog(simpleMessage("same", "first", ArgumentSpec{Name: "value", Type: TypeText, Required: true})))
	secondSpec := testCatalog(simpleMessage("same", "second", ArgumentSpec{Name: "value", Type: TypeText, Required: true}))
	secondSpec.Revision = "catalog-2"
	secondSpec.Modules[0].Messages[0].Revision = "message-2"
	second := mustSnapshot(t, secondSpec)
	type arguments struct{ Value string }
	firstDefinition, err := NewStructDefinition[arguments](first, "app.same")
	if err != nil {
		t.Fatal(err)
	}
	secondDefinition, err := NewStructDefinition[arguments](second, "app.same")
	if err != nil {
		t.Fatal(err)
	}
	firstMessage, err := firstDefinition.Bind(arguments{Value: "one"})
	if err != nil {
		t.Fatal(err)
	}
	secondMessage, err := secondDefinition.Bind(arguments{Value: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if firstMessage.ContractRevision() != "message-1" || secondMessage.ContractRevision() != "message-2" {
		t.Fatalf("revisions = %q / %q", firstMessage.ContractRevision(), secondMessage.ContractRevision())
	}
}

func FuzzStructDefinitionBindNeverPanics(f *testing.F) {
	snapshot, err := New(CatalogSpec{
		Revision: "fuzz/v1", SourceLocale: "en", DefaultLocale: "en", DefaultTimeZone: "UTC", Supported: []string{"en"},
		Modules: []Module{{Name: "fuzz", Messages: []MessageSpec{simpleMessage("bind", "ok", ArgumentSpec{Name: "value", Type: TypeText})}}},
	})
	if err != nil {
		f.Fatal(err)
	}
	type arguments struct{ Value Optional[string] }
	definition, err := NewStructDefinition[arguments](snapshot, "fuzz.bind")
	if err != nil {
		f.Fatal(err)
	}
	f.Add("value", uint8(1))
	f.Add("", uint8(0))
	f.Add("", uint8(2))
	f.Add("value", uint8(255))
	f.Fuzz(func(t *testing.T, value string, state uint8) {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("Bind panicked: %v", recovered)
			}
		}()
		_, _ = definition.Bind(arguments{Value: Optional[string]{value: value, state: state}})
	})
}

func FuzzStructDefinitionTagParserNeverPanics(f *testing.F) {
	for _, seed := range []string{
		`i18n:"value"`,
		`json:"value,omitempty" i18n:"\x76alue"`,
		`i18n:value`,
		`i18n:"value" broken`,
		`i18n:"value" i18n:"other"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("tag parser panicked: %v", recovered)
			}
		}()
		_, _, _ = parseI18nStructTag(reflect.StructTag(raw))
	})
}
