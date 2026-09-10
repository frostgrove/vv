package i18n

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/kaptinlin/messageformat-go/pkg/bidi"
	"github.com/kaptinlin/messageformat-go/pkg/messagevalue"
)

func valueParts(parts []Part) []Part {
	out := make([]Part, 0, len(parts))
	for _, part := range parts {
		if part.Kind == PartValue {
			out = append(out, part)
		}
	}
	return out
}

func joinedSubparts(parts []Subpart) string {
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

func TestUniversalDirectionAndIDFollowTheOfficialPartsShape(t *testing.T) {
	tests := []struct {
		name      string
		direction string
		isolate   string
	}{
		{name: "ltr", direction: "ltr", isolate: "\u2066"},
		{name: "rtl", direction: "rtl", isolate: "\u2067"},
		{name: "auto", direction: "auto", isolate: "\u2068"},
		{name: "inherit", direction: "inherit", isolate: "\u2068"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := localeSnapshot(t, "en", "hello {$world :string u:dir="+test.direction+" u:id=|person: #1 / ✓|}", ArgumentSpec{Name: "world", Type: TypeText, Required: true})
			message, err := snapshot.Bind("app.m", Text("world", "world"))
			if err != nil {
				t.Fatal(err)
			}
			rendered := mustRender(t, mustView(t, snapshot, "en", "", "", PresentationDefault), message)
			if rendered.Text != "hello "+test.isolate+"world\u2069" {
				t.Fatalf("rendered text = %q", rendered.Text)
			}
			values := valueParts(rendered.Parts)
			expectedDirection := test.direction
			if expectedDirection == "inherit" {
				expectedDirection = "auto"
			}
			if len(values) != 1 || values[0].Type != "string" || values[0].ID != "person: #1 / ✓" || values[0].Direction != expectedDirection {
				t.Fatalf("value metadata = %#v", values)
			}

			without := mustRender(t, mustView(t, snapshot, "en", "", "", PresentationNoIsolation), message)
			if without.Text != "hello world" {
				t.Fatalf("no-isolation text = %q", without.Text)
			}
			for _, part := range without.Parts {
				if part.Kind == PartBidiIsolation {
					t.Fatalf("no-isolation output retained controls: %#v", without.Parts)
				}
			}
			values = valueParts(without.Parts)
			if len(values) != 1 || values[0].ID != "person: #1 / ✓" {
				t.Fatalf("no-isolation removed value metadata: %#v", values)
			}
		})
	}

	number := localeSnapshot(t, "en", "{$n :number u:dir=inherit u:id=n}", ArgumentSpec{Name: "n", Type: TypeInteger, Required: true})
	message, err := number.Bind("app.m", Integer("n", 7))
	if err != nil {
		t.Fatal(err)
	}
	rendered := mustRender(t, mustView(t, number, "en", "", "", PresentationDefault), message)
	values := valueParts(rendered.Parts)
	if len(values) != 1 || values[0].Direction != "ltr" || values[0].ID != "n" || strings.ContainsAny(rendered.Text, "\u2066\u2067\u2068\u2069") {
		t.Fatalf("inherited number metadata = %#v, text %q", values, rendered.Text)
	}
}

func TestUniversalMetadataCoversNullThroughEveryFunction(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		argumentType ArgumentType
		capabilities []Capability
	}{
		{name: "number", source: "{$v :number u:id=nil u:dir=rtl}", argumentType: TypeInteger},
		{name: "integer", source: "{$v :integer u:id=nil u:dir=rtl}", argumentType: TypeInteger},
		{name: "currency", source: "{$v :currency u:id=nil u:dir=rtl}", argumentType: TypeMoney},
		{name: "percent", source: "{$v :percent u:id=nil u:dir=rtl}", argumentType: TypeDecimal},
		{name: "unit", source: "{$v :unit unit=meter u:id=nil u:dir=rtl}", argumentType: TypeInteger, capabilities: []Capability{CapabilityUnit}},
		{name: "offset", source: "{$v :offset add=1 u:id=nil u:dir=rtl}", argumentType: TypeInteger},
		{name: "string", source: "{$v :string u:id=nil u:dir=rtl}", argumentType: TypeText},
		{name: "date", source: "{$v :date u:id=nil u:dir=rtl}", argumentType: TypeDate, capabilities: []Capability{CapabilityDateTime}},
		{name: "time", source: "{$v :time u:id=nil u:dir=rtl}", argumentType: TypeInstant, capabilities: []Capability{CapabilityDateTime}},
		{name: "datetime", source: "{$v :datetime u:id=nil u:dir=rtl}", argumentType: TypeInstant, capabilities: []Capability{CapabilityDateTime}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := testCatalog(simpleMessage("m", test.source, ArgumentSpec{Name: "v", Type: test.argumentType, Nullable: true}))
			spec.Capabilities = test.capabilities
			snapshot := mustSnapshot(t, spec)
			message, err := snapshot.Bind("app.m", Null("v"))
			if err != nil {
				t.Fatal(err)
			}
			values := valueParts(mustRender(t, mustView(t, snapshot, "en", "", "", PresentationNoIsolation), message).Parts)
			if len(values) != 1 || values[0].Type != "null" || values[0].ID != "nil" || values[0].Direction != "rtl" || values[0].Text != "" || values[0].Subparts != nil {
				t.Fatalf("null function part = %#v", values)
			}
		})
	}
}

func TestUniversalMetadataSurvivesLocalsAndResetsAtFormatterBoundaries(t *testing.T) {
	local := localeSnapshot(t, "en", ".local $world = {$name :string u:dir=ltr u:id=person}\n{{hello {$world}}}", ArgumentSpec{Name: "name", Type: TypeText, Required: true})
	message, err := local.Bind("app.m", Text("name", "world"))
	if err != nil {
		t.Fatal(err)
	}
	rendered := mustRender(t, mustView(t, local, "en", "", "", PresentationNoIsolation), message)
	values := valueParts(rendered.Parts)
	if len(values) != 1 || values[0].ID != "person" || values[0].Direction != "ltr" {
		t.Fatalf("local metadata = %#v", values)
	}

	tests := []struct {
		name     string
		local    string
		wantID   string
		wantDir  string
		wantText string
	}{
		{name: "replacement", local: "{$n :integer u:id=new u:dir=ltr}", wantID: "new", wantDir: "ltr", wantText: "7"},
		{name: "default inherit", local: "{$n :integer}", wantDir: "rtl", wantText: "7"},
		{name: "explicit inherit", local: "{$n :integer u:dir=inherit}", wantDir: "rtl", wantText: "7"},
		{name: "offset", local: "{$n :offset add=1 u:id=next u:dir=ltr}", wantID: "next", wantDir: "ltr", wantText: "8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := ".input {$n :number u:id=old u:dir=rtl}\n.local $next = " + test.local + "\n{{{$next}}}"
			snapshot := localeSnapshot(t, "en", template, ArgumentSpec{Name: "n", Type: TypeInteger, Required: true})
			message, err := snapshot.Bind("app.m", Integer("n", 7))
			if err != nil {
				t.Fatal(err)
			}
			rendered := mustRender(t, mustView(t, snapshot, "en", "", "", PresentationNoIsolation), message)
			values := valueParts(rendered.Parts)
			if len(values) != 1 || values[0].ID != test.wantID || values[0].Direction != test.wantDir || values[0].Text != test.wantText {
				t.Fatalf("chained metadata = %#v", values)
			}
		})
	}

	nullable := localeSnapshot(t, "en", "{$n :number u:id=nil u:dir=rtl}", ArgumentSpec{Name: "n", Type: TypeInteger, Nullable: true})
	nullMessage, err := nullable.Bind("app.m", Null("n"))
	if err != nil {
		t.Fatal(err)
	}
	values = valueParts(mustRender(t, mustView(t, nullable, "en", "", "", PresentationNoIsolation), nullMessage).Parts)
	if len(values) != 1 || values[0].Type != "null" || values[0].ID != "nil" || values[0].Direction != "rtl" || values[0].Text != "" {
		t.Fatalf("null metadata = %#v", values)
	}
}

func TestUniversalDirectionInheritanceAcrossFormatterChains(t *testing.T) {
	functions := []struct {
		name         string
		input        string
		argumentSpec ArgumentSpec
		argument     Argument
		capabilities []Capability
	}{
		{name: "string", input: ":string", argumentSpec: ArgumentSpec{Name: "v", Type: TypeText, Required: true}, argument: Text("v", "value")},
		{name: "number", input: ":number", argumentSpec: ArgumentSpec{Name: "v", Type: TypeInteger, Required: true}, argument: Integer("v", 7)},
		{name: "date", input: ":date", argumentSpec: ArgumentSpec{Name: "v", Type: TypeDate, Required: true}, argument: Date("v", 2026, time.September, 9), capabilities: []Capability{CapabilityDateTime}},
		{name: "null", input: ":string", argumentSpec: ArgumentSpec{Name: "v", Type: TypeText, Nullable: true}, argument: Null("v")},
	}
	directions := []struct {
		name      string
		option    string
		want      string
		isolation string
	}{
		{name: "omitted", want: "rtl", isolation: string(bidi.RLI)},
		{name: "inherit", option: " u:dir=inherit", want: "rtl", isolation: string(bidi.RLI)},
		{name: "override", option: " u:dir=ltr", want: "ltr", isolation: string(bidi.LRI)},
	}
	for _, function := range functions {
		for _, direction := range directions {
			t.Run(function.name+"/"+direction.name, func(t *testing.T) {
				source := ".input {$v " + function.input + " u:id=old u:dir=rtl}\n.local $next = {$v " + function.input + direction.option + "}\n{{{$next}}}"
				spec := testCatalog(simpleMessage("m", source, function.argumentSpec))
				spec.Capabilities = function.capabilities
				snapshot := mustSnapshot(t, spec)
				message, err := snapshot.Bind("app.m", function.argument)
				if err != nil {
					t.Fatal(err)
				}
				for _, presentation := range []Presentation{PresentationDefault, PresentationNoIsolation} {
					rendered := mustRender(t, mustView(t, snapshot, "en", "", "", presentation), message)
					values := valueParts(rendered.Parts)
					if len(values) != 1 || values[0].Direction != direction.want || values[0].ID != "" {
						t.Fatalf("%s metadata = %#v", presentation, values)
					}
					if presentation == PresentationDefault {
						if !strings.HasPrefix(rendered.Text, direction.isolation) || !strings.HasSuffix(rendered.Text, string(bidi.PDI)) {
							t.Fatalf("isolated output = %q", rendered.Text)
						}
					} else if strings.ContainsAny(rendered.Text, "\u2066\u2067\u2068\u2069") {
						t.Fatalf("no-isolation output = %q", rendered.Text)
					}
				}
			})
		}
	}
}

func TestUniversalIDCoversEveryProfileFunctionAndMarkup(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		argumentSpec ArgumentSpec
		argument     Argument
		capabilities []Capability
		wantType     string
	}{
		{name: "number", source: "{$v :number u:id=x}", argumentSpec: ArgumentSpec{Name: "v", Type: TypeBigInteger, Required: true}, argument: BigInteger("v", new(big.Int).SetUint64(^uint64(0))), wantType: "number"},
		{name: "integer", source: "{$v :integer u:id=x}", argumentSpec: ArgumentSpec{Name: "v", Type: TypeInteger, Required: true}, argument: Integer("v", 12), wantType: "number"},
		{name: "currency", source: "{$v :currency u:id=x}", argumentSpec: ArgumentSpec{Name: "v", Type: TypeMoney, Required: true}, argument: Money("v", "12.50", "USD"), wantType: "number"},
		{name: "percent", source: "{$v :percent u:id=x}", argumentSpec: ArgumentSpec{Name: "v", Type: TypeDecimal, Required: true}, argument: Decimal("v", "0.25"), wantType: "number"},
		{name: "unit", source: "{$v :unit unit=meter u:id=x}", argumentSpec: ArgumentSpec{Name: "v", Type: TypeInteger, Required: true}, argument: Integer("v", 3), capabilities: []Capability{CapabilityUnit}, wantType: "number"},
		{name: "offset", source: "{$v :offset add=1 u:id=x}", argumentSpec: ArgumentSpec{Name: "v", Type: TypeInteger, Required: true}, argument: Integer("v", 2), wantType: "number"},
		{name: "string", source: "{$v :string u:id=x}", argumentSpec: ArgumentSpec{Name: "v", Type: TypeText, Required: true}, argument: Text("v", "value"), wantType: "string"},
		{name: "date", source: "{$v :date dateStyle=short u:id=x}", argumentSpec: ArgumentSpec{Name: "v", Type: TypeDate, Required: true}, argument: Date("v", 2026, time.May, 8), capabilities: []Capability{CapabilityDateTime}, wantType: "datetime"},
		{name: "time", source: "{$v :time timeStyle=short u:id=x}", argumentSpec: ArgumentSpec{Name: "v", Type: TypeInstant, Required: true}, argument: Instant("v", time.Date(2026, time.May, 8, 9, 7, 0, 0, time.UTC)), capabilities: []Capability{CapabilityDateTime}, wantType: "datetime"},
		{name: "datetime", source: "{$v :datetime dateStyle=short timeStyle=short u:id=x}", argumentSpec: ArgumentSpec{Name: "v", Type: TypeInstant, Required: true}, argument: Instant("v", time.Date(2026, time.May, 8, 9, 7, 0, 0, time.UTC)), capabilities: []Capability{CapabilityDateTime}, wantType: "datetime"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := testCatalog(simpleMessage("m", test.source, test.argumentSpec))
			spec.Capabilities = test.capabilities
			snapshot := mustSnapshot(t, spec)
			message, err := snapshot.Bind("app.m", test.argument)
			if err != nil {
				t.Fatal(err)
			}
			values := valueParts(mustRender(t, mustView(t, snapshot, "en", "", "", PresentationNoIsolation), message).Parts)
			if len(values) != 1 || values[0].ID != "x" || values[0].Type != test.wantType {
				t.Fatalf("function value part = %#v", values)
			}
			if test.wantType != "string" && len(values[0].Subparts) == 0 {
				t.Fatalf("formatted value has no typed subparts: %#v", values[0])
			}
		})
	}

	spec := testCatalog(MessageSpec{ID: "m", Revision: "r", Source: "{#strong u:id=|same id|}content{/strong u:id=|same id|}{#br u:id=|same id| /}", Output: OutputRich, Markup: []string{"br", "strong"}})
	snapshot := mustSnapshot(t, spec)
	message, err := snapshot.Bind("app.m")
	if err != nil {
		t.Fatal(err)
	}
	rendered := mustRender(t, mustView(t, snapshot, "en", "", "", PresentationNoIsolation), message)
	markup := make([]Part, 0, 3)
	for _, part := range rendered.Parts {
		if part.Kind == PartMarkupOpen || part.Kind == PartMarkupClose || part.Kind == PartMarkupStandalone {
			markup = append(markup, part)
		}
	}
	if len(markup) != 3 {
		t.Fatalf("markup parts = %#v", rendered.Parts)
	}
	for _, part := range markup {
		if part.Type != "markup" || part.ID != "same id" {
			t.Fatalf("markup metadata = %#v", markup)
		}
	}
}

func TestUniversalOptionsFailClosedAtConstruction(t *testing.T) {
	tests := []struct {
		name   string
		source string
		limit  int
		output OutputKind
		markup []string
	}{
		{name: "variable id", source: "{$v :string u:id=$id}"},
		{name: "variable direction", source: "{$v :string u:dir=$direction}"},
		{name: "unknown", source: "{$v :string u:future=x}"},
		{name: "invalid direction", source: "{$v :string u:dir=sideways}"},
		{name: "empty direction", source: "{$v :string u:dir=||}"},
		{name: "empty id", source: "{$v :string u:id=||}"},
		{name: "non NFC id", source: "{$v :string u:id=|e\u0301|}"},
		{name: "unsafe id", source: "{$v :string u:id=|left\u2066right|}"},
		{name: "oversize id", source: "{$v :string u:id=long}", limit: 3},
		{name: "markup direction", source: "{#strong u:dir=rtl}x{/strong}", output: OutputRich, markup: []string{"strong"}},
		{name: "markup variable id", source: "{#strong u:id=$id}x{/strong}", output: OutputRich, markup: []string{"strong"}},
		{name: "markup unknown", source: "{#strong u:future=x}x{/strong}", output: OutputRich, markup: []string{"strong"}},
		{name: "markup ordinary option", source: "{#strong tone=loud}x{/strong}", output: OutputRich, markup: []string{"strong"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := MessageSpec{
				ID:       "m",
				Revision: "r",
				Source:   test.source,
				Arguments: []ArgumentSpec{
					{Name: "v", Type: TypeText, Required: true},
					{Name: "id", Type: TypeText},
					{Name: "direction", Type: TypeText},
				},
				Output: test.output,
				Markup: test.markup,
			}
			spec := testCatalog(message)
			if test.limit != 0 {
				spec.Limits.MaxIdentifierBytes = test.limit
			}
			_, err := New(spec)
			if !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("New error = %v, want ErrInvalidCatalog", err)
			}
			if test.limit != 0 && !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("oversize ID error = %v, want ErrLimitExceeded", err)
			}
		})
	}
}

func TestFormattedSubpartsAreExactImmutableAndBoundedAsNodes(t *testing.T) {
	integer := new(big.Int)
	integer.SetString("1234567890123456789012345678901234567890", 10)
	spec := testCatalog(MessageSpec{
		ID:       "m",
		Revision: "r",
		Source:   "{$n :number useGrouping=never}|{$when :datetime dateStyle=medium timeStyle=long}",
		Arguments: []ArgumentSpec{
			{Name: "n", Type: TypeBigInteger, Required: true},
			{Name: "when", Type: TypeInstant, Required: true},
		},
	})
	spec.Capabilities = []Capability{CapabilityDateTime}
	snapshot := mustSnapshot(t, spec)
	message, err := snapshot.Bind("app.m", BigInteger("n", integer), Instant("when", time.Date(2026, time.May, 8, 9, 7, 6, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	rendered := mustRender(t, mustView(t, snapshot, "en", "", "", PresentationNoIsolation), message)
	values := valueParts(rendered.Parts)
	if len(values) != 2 {
		t.Fatalf("formatted values = %#v", values)
	}
	for _, part := range values {
		if len(part.Subparts) == 0 || joinedSubparts(part.Subparts) != part.Text {
			t.Fatalf("subpart join invariant failed: %#v", part)
		}
		for _, subpart := range part.Subparts {
			if subpart.Type == "" {
				t.Fatalf("untyped subpart: %#v", part)
			}
		}
	}
	if values[0].Text != integer.String() {
		t.Fatalf("big integer text = %q, want %q", values[0].Text, integer)
	}
	values[0].Subparts[0].Text = "mutated"
	repeated := valueParts(mustRender(t, mustView(t, snapshot, "en", "", "", PresentationNoIsolation), message).Parts)
	if repeated[0].Subparts[0].Text == "mutated" || joinedSubparts(repeated[0].Subparts) != repeated[0].Text {
		t.Fatalf("subpart mutation reached later render: %#v", repeated[0])
	}

	numberSpec := testCatalog(simpleMessage("m", "{$n :number useGrouping=never}", ArgumentSpec{Name: "n", Type: TypeInteger, Required: true}))
	control := mustSnapshot(t, numberSpec)
	controlMessage, err := control.Bind("app.m", Integer("n", 1234))
	if err != nil {
		t.Fatal(err)
	}
	controlRendered := mustRender(t, mustView(t, control, "en", "", "", PresentationNoIsolation), controlMessage)
	controlValue := valueParts(controlRendered.Parts)[0]
	nodes := 1 + len(controlValue.Subparts)
	numberSpec.Limits.MaxOutputParts = nodes
	atLimit := mustSnapshot(t, numberSpec)
	atLimitMessage, err := atLimit.Bind("app.m", Integer("n", 1234))
	if err != nil {
		t.Fatal(err)
	}
	mustRender(t, mustView(t, atLimit, "en", "", "", PresentationNoIsolation), atLimitMessage)

	numberSpec.Limits.MaxOutputParts = nodes - 1
	below := mustSnapshot(t, numberSpec)
	belowMessage, err := below.Bind("app.m", Integer("n", 1234))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mustView(t, below, "en", "", "", PresentationNoIsolation).Render(t.Context(), belowMessage); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("one-less node limit error = %v, want ErrLimitExceeded", err)
	}

	maximum, err := numericOutputBytes(numericInput{kind: numericSigned, signed: 1234}, "number", map[string]any{"useGrouping": "never"})
	if err != nil {
		t.Fatal(err)
	}
	budget := &renderBudget{bytes: maximum, parts: 32}
	value := &exactNumberValue{
		value:         numericInput{kind: numericSigned, signed: 1234},
		grammarLocale: "en",
		formatLocale:  "en",
		style:         styleNumber,
		options:       map[string]any{"useGrouping": "never"},
		budget:        budget,
	}
	formatted, err := value.ToParts()
	if err != nil {
		t.Fatal(err)
	}
	outer := formatted[0].(formattedMessagePart)
	if budget.bytes != maximum-len(outer.value) || budget.parts != 32-1-len(outer.parts) {
		t.Fatalf("budget after structured format = bytes %d parts %d", budget.bytes, budget.parts)
	}
}

func TestStructuredFormatterPartBoundsPrecedeFormatterAllocation(t *testing.T) {
	huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(50000), nil)
	input := numericInput{kind: numericBig, big: huge}
	options := map[string]any{"useGrouping": "auto"}
	maximumBytes, err := numericOutputBytes(input, "number", options)
	if err != nil {
		t.Fatal(err)
	}
	maximumParts, err := numericOutputParts(input, "number", options)
	if err != nil {
		t.Fatal(err)
	}
	value := &exactNumberValue{
		value:         input,
		grammarLocale: "en",
		formatLocale:  "\xff",
		style:         styleNumber,
		options:       options,
		budget:        &renderBudget{bytes: maximumBytes, parts: 2},
	}
	if _, err := value.ToParts(); !errors.Is(err, ErrLimitExceeded) || !value.budget.exhausted {
		t.Fatalf("tiny part budget error = %v, exhausted = %t", err, value.budget.exhausted)
	}

	boundary := *value
	boundary.budget = &renderBudget{bytes: maximumBytes, parts: maximumParts - 1}
	if _, err := boundary.ToParts(); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("one-less conservative bound error = %v, want ErrLimitExceeded", err)
	}
	atBound := *value
	atBound.budget = &renderBudget{bytes: maximumBytes, parts: maximumParts}
	if _, err := atBound.ToParts(); err == nil || errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("at-bound error = %v, want formatter locale error", err)
	}

	spec := testCatalog(simpleMessage("m", "{$n :number useGrouping=auto}", ArgumentSpec{Name: "n", Type: TypeBigInteger, Required: true}))
	spec.Limits.MaxArgumentBytes = 100000
	spec.Limits.MaxOutputBytes = 1 << 20
	spec.Limits.MaxOutputParts = 2
	snapshot := mustSnapshot(t, spec)
	message, err := snapshot.Bind("app.m", BigInteger("n", huge))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mustView(t, snapshot, "en", "", "", PresentationNoIsolation).Render(t.Context(), message); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("work preflight error = %v, want ErrLimitExceeded", err)
	}
}

func TestNumericOutputPartBoundCoversFormatterShapes(t *testing.T) {
	tests := []struct {
		name     string
		locale   string
		value    numericInput
		style    numericStyle
		currency string
		options  map[string]any
	}{
		{name: "signed grouping", locale: "en", value: numericInput{kind: numericSigned, signed: -9223372036854775808}, style: styleNumber, options: map[string]any{"signDisplay": "always"}},
		{name: "unsigned grouping", locale: "hi", value: numericInput{kind: numericUnsigned, unsigned: ^uint64(0)}, style: styleNumber},
		{name: "rounding carry", locale: "fr", value: numericInput{kind: numericDecimal, decimal: "999999.9"}, style: styleNumber, options: map[string]any{"maximumFractionDigits": 0}},
		{name: "implicit maximum rounds", locale: "en", value: numericInput{kind: numericDecimal, decimal: "999.9999"}, style: styleNumber, options: map[string]any{"minimumFractionDigits": 2}},
		{name: "minimum integer and significant", locale: "ar", value: numericInput{kind: numericDecimal, decimal: "1"}, style: styleNumber, options: map[string]any{"minimumIntegerDigits": 20, "minimumSignificantDigits": 21}},
		{name: "significant overrides fraction maximum", locale: "en", value: numericInput{kind: numericDecimal, decimal: "0.12345"}, style: styleNumber, options: map[string]any{"maximumFractionDigits": 0, "minimumSignificantDigits": 3}},
		{name: "percent rounding", locale: "fa", value: numericInput{kind: numericDecimal, decimal: "-0.9999"}, style: stylePercent, options: map[string]any{"maximumFractionDigits": 0, "signDisplay": "exceptZero"}},
		{name: "scientific", locale: "en", value: numericInput{kind: numericBig, big: new(big.Int).Exp(big.NewInt(10), big.NewInt(200), nil)}, style: styleNumber, options: map[string]any{"notation": "scientific", "maximumSignificantDigits": 3}},
		{name: "engineering", locale: "ja", value: numericInput{kind: numericDecimal, decimal: "0.000000000123456"}, style: styleNumber, options: map[string]any{"notation": "engineering", "maximumSignificantDigits": 4}},
		{name: "compact", locale: "fr", value: numericInput{kind: numericSigned, signed: -999999999}, style: styleNumber, options: map[string]any{"notation": "compact", "compactDisplay": "long", "signDisplay": "always"}},
		{name: "currency name", locale: "ar", value: numericInput{kind: numericDecimal, decimal: "-1234567.89"}, style: styleCurrency, currency: "USD", options: map[string]any{"currencyDisplay": "name", "currencySign": "accounting", "signDisplay": "always"}},
		{name: "currency fraction auto", locale: "en", value: numericInput{kind: numericDecimal, decimal: "12.3456"}, style: styleCurrency, currency: "KWD", options: map[string]any{"fractionDigits": "auto"}},
		{name: "compound unit", locale: "de", value: numericInput{kind: numericDecimal, decimal: "1234.50"}, style: styleUnit, options: map[string]any{"unit": "kilometer-per-hour", "unitDisplay": "long"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := &exactNumberValue{
				value:         test.value,
				grammarLocale: test.locale,
				formatLocale:  test.locale,
				style:         test.style,
				currency:      test.currency,
				options:       test.options,
			}
			_, subparts, err := value.formatParts()
			if err != nil {
				t.Fatal(err)
			}
			maximum, err := numericOutputParts(test.value, value.functionName(), test.options)
			if err != nil {
				t.Fatal(err)
			}
			if actual := 1 + len(subparts); actual > maximum {
				t.Fatalf("actual parts %d exceed conservative bound %d: %#v", actual, maximum, subparts)
			}
		})
	}
}

func TestDateOutputPartBoundIsConservativeAndPreflighted(t *testing.T) {
	tests := []exactDateValue{
		{value: time.Date(2026, time.May, 8, 9, 7, 6, 0, time.UTC), formatLocale: "en", timeZone: "UTC", style: styleDateTime, options: map[string]any{"dateStyle": "full", "timeStyle": "full"}},
		{value: time.Date(2026, time.May, 8, 9, 7, 6, 0, time.UTC), formatLocale: "ar", timeZone: "UTC", style: styleDateTime, options: map[string]any{"weekday": "long", "era": "long", "year": "numeric", "month": "long", "day": "numeric", "dayPeriod": "long", "hour": "numeric", "minute": "numeric", "second": "numeric", "timeZoneName": "long"}},
		{value: time.Date(2026, time.May, 8, 9, 7, 6, 0, time.UTC), formatLocale: "ja", timeZone: "UTC", style: styleDate, options: map[string]any{"dateStyle": "full"}},
	}
	for index := range tests {
		_, subparts, err := tests[index].formatParts()
		if err != nil {
			t.Fatal(err)
		}
		if actual := 1 + len(subparts); actual > maximumDateValueParts {
			t.Fatalf("actual date parts %d exceed conservative bound %d: %#v", actual, maximumDateValueParts, subparts)
		}
	}

	limited := tests[0]
	limited.budget = &renderBudget{bytes: formattedValueOverhead, parts: maximumDateValueParts - 1}
	if _, err := limited.ToParts(); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("one-less date bound error = %v, want ErrLimitExceeded", err)
	}
	atBound := tests[0]
	atBound.budget = &renderBudget{bytes: formattedValueOverhead, parts: maximumDateValueParts}
	if _, err := atBound.ToParts(); err != nil {
		t.Fatalf("at-bound date formatting: %v", err)
	}
}

type richTestPart struct {
	typeName string
	text     string
	cancel   context.CancelFunc
}

func (p richTestPart) Type() string { return p.typeName }
func (p richTestPart) Value() any {
	if p.cancel != nil {
		p.cancel()
	}
	return p.text
}
func (p richTestPart) Source() string      { return "" }
func (p richTestPart) Locale() string      { return "" }
func (p richTestPart) Dir() bidi.Direction { return "" }

type richTestNestedPart struct {
	richTestPart
	parts []messagevalue.MessagePart
}

func (p richTestNestedPart) Parts() []messagevalue.MessagePart { return p.parts }

func TestSubpartConversionPollsContextAndRejectsUnsafeTypes(t *testing.T) {
	for _, value := range []string{"", strings.Repeat("x", maximumPartTypeBytes+1), "e\u0301", "left\u2066right"} {
		if validPartType(value) {
			t.Fatalf("validPartType(%q) = true", value)
		}
	}
	if !validPartType("fractionalSecond") {
		t.Fatal("known part type is invalid")
	}
	view := &View{snapshot: &Snapshot{limits: Limits{MaxOutputParts: 64}}}
	unsafe := richTestNestedPart{richTestPart: richTestPart{typeName: "number"}, parts: []messagevalue.MessagePart{richTestPart{typeName: "bad\u2066", text: "x"}}}
	if _, err := view.convertSubparts(t.Context(), unsafe, "x"); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("unsafe subpart type error = %v, want ErrInvalidMessage", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	children := make([]messagevalue.MessagePart, 64)
	for index := range children {
		children[index] = richTestPart{typeName: "integer", text: "x"}
	}
	children[1] = richTestPart{typeName: "integer", text: "x", cancel: cancel}
	outer := richTestNestedPart{richTestPart: richTestPart{typeName: "number"}, parts: children}
	if _, err := view.convertSubparts(ctx, outer, strings.Repeat("x", len(children))); !errors.Is(err, context.Canceled) {
		t.Fatalf("convertSubparts error = %v, want context cancellation", err)
	}
}

func TestPseudoPreservesUniversalMetadata(t *testing.T) {
	spec := testCatalog(simpleMessage("m", "Label {$value :string u:id=label u:dir=ltr}", ArgumentSpec{Name: "value", Type: TypeText, Required: true}))
	spec.Supported = []string{"en", "fr"}
	pseudo, err := Pseudo(spec, PseudoSpec{Locale: "fr", Mode: PseudoAccent})
	if err != nil {
		t.Fatal(err)
	}
	translation := &pseudo.Modules[0].Messages[0].Translations[0]
	translation.Review = ReviewApproved
	snapshot, err := New(pseudo)
	if err != nil {
		t.Fatal(err)
	}
	message, err := snapshot.Bind("app.m", Text("value", "unchanged"))
	if err != nil {
		t.Fatal(err)
	}
	values := valueParts(mustRender(t, mustView(t, snapshot, "fr", "", "", PresentationNoIsolation), message).Parts)
	if len(values) != 1 || values[0].ID != "label" || values[0].Direction != "ltr" || values[0].Text != "unchanged" {
		t.Fatalf("pseudolocale metadata = %#v", values)
	}
}
