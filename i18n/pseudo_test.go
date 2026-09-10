package i18n

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kaptinlin/messageformat-go/pkg/datamodel"
)

func TestPseudoTransformsOnlyPatternTextAndRequiresReview(t *testing.T) {
	source := ".input {$count :number select=cardinal}\n.match $count\none {{{#strong}One item for {$name}{/strong}}}\n* {{{#strong}{$count} items for {$name}{/strong}}}"
	spec := CatalogSpec{
		Revision:      "catalog-1",
		SourceLocale:  "en",
		DefaultLocale: "en",
		Supported:     []string{"en", "fr", "ar"},
		Modules: []Module{
			{
				Name: "app",
				Messages: []MessageSpec{
					{
						ID:          "items",
						Revision:    "message-1",
						Source:      source,
						Description: "Number of items for a person",
						Arguments: []ArgumentSpec{
							{Name: "count", Type: TypeInteger, Required: true},
							{Name: "name", Type: TypeText, Required: true},
						},
						Output: OutputRich,
						Markup: []string{"strong"},
					},
				},
			},
		},
	}
	original := cloneCatalogSpecForPseudo(spec)

	for _, test := range []struct {
		name   string
		locale string
		mode   PseudoMode
		marker string
	}{
		{name: "accent", locale: "fr", mode: PseudoAccent, marker: "⟦"},
		{name: "rtl", locale: "ar", mode: PseudoRTL, marker: "‏"},
	} {
		t.Run(test.name, func(t *testing.T) {
			first, err := Pseudo(spec, PseudoSpec{Locale: test.locale, Mode: test.mode})
			if err != nil {
				t.Fatal(err)
			}
			second, err := Pseudo(spec, PseudoSpec{Locale: test.locale, Mode: test.mode})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatal("pseudolocale output is not deterministic")
			}
			if !reflect.DeepEqual(spec, original) {
				t.Fatal("pseudolocale generation mutated its input")
			}

			translation := first.Modules[0].Messages[0].Translations[0]
			if translation.Review != ReviewRequired || translation.ContractRevision != "message-1" || translation.SourceDigest == "" {
				t.Fatalf("translation provenance = %#v", translation)
			}
			expectedDigest, err := ExpectedSourceDigestForLocale(first.Profile, first.SourceLocale, "app", first.Modules[0].Messages[0])
			if err != nil || translation.SourceDigest != expectedDigest {
				t.Fatalf("translation source digest = %q, want %q: %v", translation.SourceDigest, expectedDigest, err)
			}
			if !strings.Contains(translation.Text, test.marker) {
				t.Fatalf("pseudo text %q does not contain mode marker", translation.Text)
			}
			if test.mode == PseudoAccent {
				combined, err := Pseudo(first, PseudoSpec{Locale: "ar", Mode: PseudoRTL})
				if err != nil {
					t.Fatalf("second unreviewed pseudolocale: %v", err)
				}
				translations := combined.Modules[0].Messages[0].Translations
				if len(translations) != 2 || translations[0].Review != ReviewRequired || translations[1].Review != ReviewRequired {
					t.Fatalf("sequential pseudolocale review states = %#v", translations)
				}
			}
			before, err := datamodel.ParseMessage(source)
			if err != nil {
				t.Fatal(err)
			}
			after, err := datamodel.ParseMessage(translation.Text)
			if err != nil {
				t.Fatalf("generated text is not valid MF2: %v", err)
			}
			if got, want := pseudoStructure(after), pseudoStructure(before); !reflect.DeepEqual(got, want) {
				t.Fatalf("non-text MF2 structure changed:\n got %v\nwant %v", got, want)
			}

			if _, err := New(first); err == nil || !strings.Contains(err.Error(), "not approved") {
				t.Fatalf("unreviewed pseudolocale compiled: %v", err)
			}
			first.Modules[0].Messages[0].Translations[0].Review = ReviewApproved
			snapshot, err := New(first)
			if err != nil {
				t.Fatal(err)
			}
			message, err := snapshot.Bind("app.items", Integer("count", 2), Text("name", "Ada"))
			if err != nil {
				t.Fatal(err)
			}
			view, err := snapshot.For(test.locale)
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := view.Render(context.Background(), message)
			if err != nil {
				t.Fatal(err)
			}
			if rendered.TemplateLocale != test.locale || !strings.Contains(rendered.Text, "Ada") {
				t.Fatalf("rendered pseudolocale = %#v", rendered)
			}
			markup := 0
			for _, part := range rendered.Parts {
				if part.Name == "strong" {
					markup++
				}
			}
			if markup != 2 {
				t.Fatalf("rich markup boundaries changed: %#v", rendered.Parts)
			}
		})
	}
}

func TestPseudoPreservesApostrophesEscapedBracesAndBalancedIsolates(t *testing.T) {
	spec := testCatalog(MessageSpec{
		ID:          "literal",
		Revision:    "r1",
		Source:      "⁦Apostrophe's braces: {{ and }}; {$name} tail⁩",
		Description: "Literal punctuation",
		Arguments:   []ArgumentSpec{{Name: "name", Type: TypeText, Required: true}},
		Output:      OutputPlain,
	})
	spec.Supported = []string{"en", "ar"}
	result, err := Pseudo(spec, PseudoSpec{Locale: "ar", Mode: PseudoRTL})
	if err != nil {
		t.Fatal(err)
	}
	translation := &result.Modules[0].Messages[0].Translations[0]
	if hasUnsafeAuthoredBidiControls(translation.Text) {
		t.Fatalf("generated RTL isolates are unbalanced: %q", translation.Text)
	}
	translation.Review = ReviewApproved
	snapshot, err := New(result)
	if err != nil {
		t.Fatal(err)
	}
	message, err := snapshot.Bind("app.literal", Text("name", "N"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := snapshot.For("ar")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := view.Render(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Text, "'") || !strings.Contains(rendered.Text, "{") || !strings.Contains(rendered.Text, "}") {
		t.Fatalf("literal punctuation was lost: %q", rendered.Text)
	}
}

func TestPseudoTransformsVisibleLiteralExpressionsAndFulfillsRequiredTarget(t *testing.T) {
	spec := testCatalog(MessageSpec{
		ID: "literal", Revision: "r1", Source: ".local $label = {|Save changes|}\n{{Visible {|literal text|}: {$label}}}",
		Description: "Visible literal expression", Output: OutputPlain,
	})
	spec.Supported = []string{"en", "fr"}
	spec.Required = []string{"en", "fr"}
	result, err := Pseudo(spec, PseudoSpec{Locale: "fr", Mode: PseudoAccent})
	if err != nil {
		t.Fatal(err)
	}
	translation := result.Modules[0].Messages[0].Translations[0]
	if !strings.Contains(translation.Text, "ĺīŧ") {
		t.Fatalf("visible literal was not transformed: %q", translation.Text)
	}
	if !strings.Contains(translation.Text, "Šåṽ") {
		t.Fatalf("visible local literal was not transformed: %q", translation.Text)
	}
	translation.Review = ReviewApproved
	result.Modules[0].Messages[0].Translations[0] = translation
	if _, err := New(result); err != nil {
		t.Fatalf("generated target did not fulfill required locale: %v", err)
	}
}

func TestPseudoRejectsInvalidTargetsCollisionsMalformedInputAndExpansionLimits(t *testing.T) {
	base := testCatalog(simpleMessage("m", "Hello"))
	base.Supported = []string{"en", "fr", "ar"}
	approved := base
	approved.Modules = []Module{{Name: "app", Messages: []MessageSpec{simpleMessage("m", "Hello")}}}
	digest, err := ExpectedSourceDigestForLocale(approved.Profile, approved.SourceLocale, "app", approved.Modules[0].Messages[0])
	if err != nil {
		t.Fatal(err)
	}
	approved.Modules[0].Messages[0].Translations = []Translation{{Locale: "fr", Text: "Bonjour", Review: ReviewApproved, ContractRevision: "message-1", SourceDigest: digest}}

	tests := []struct {
		name   string
		spec   CatalogSpec
		pseudo PseudoSpec
	}{
		{name: "source locale", spec: base, pseudo: PseudoSpec{Locale: "en", Mode: PseudoAccent}},
		{name: "unsupported locale", spec: base, pseudo: PseudoSpec{Locale: "de", Mode: PseudoAccent}},
		{name: "translation collision", spec: approved, pseudo: PseudoSpec{Locale: "FR", Mode: PseudoAccent}},
		{name: "unknown mode", spec: base, pseudo: PseudoSpec{Locale: "fr", Mode: PseudoMode(99)}},
		{name: "malformed source", spec: testCatalog(simpleMessage("m", "Hello {$name")), pseudo: PseudoSpec{Locale: "fr", Mode: PseudoAccent}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Pseudo(test.spec, test.pseudo); err == nil {
				t.Fatal("invalid pseudolocale input was accepted")
			}
		})
	}

	limited := base
	limited.Limits.MaxTemplateBytes = len("Hello")
	if _, err := Pseudo(limited, PseudoSpec{Locale: "fr", Mode: PseudoAccent}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expanded template limit error = %v", err)
	}
}

func pseudoStructure(message datamodel.Message) []string {
	structure := []string{"message:" + message.Type()}
	datamodel.Visit(message, &datamodel.Visitor{
		Declaration: func(declaration datamodel.Declaration) func() {
			structure = append(structure, "declaration:"+declaration.Type()+":"+declaration.Name())
			return nil
		},
		FunctionRef: func(function *datamodel.FunctionRef, context datamodel.VisitContext, argument datamodel.ExpressionArg) func() {
			structure = append(structure, "function:"+function.Name())
			return nil
		},
		Key: func(key datamodel.VariantKey, index int, keys []datamodel.VariantKey) {
			structure = append(structure, "key:"+key.String())
		},
		Markup: func(markup *datamodel.Markup, context datamodel.VisitContext) func() {
			structure = append(structure, "markup:"+string(markup.Kind())+":"+markup.Name())
			return nil
		},
		Options: func(options datamodel.Options, context datamodel.VisitContext) func() {
			names := make([]string, 0, len(options))
			for name := range options {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				structure = append(structure, "option:"+name+"="+options[name].String())
			}
			return nil
		},
		Value: func(value datamodel.ExpressionArg, context datamodel.VisitContext, position datamodel.ValuePosition) {
			switch value := value.(type) {
			case *datamodel.VariableRef:
				structure = append(structure, "variable:"+value.Name())
			case *datamodel.Literal:
				structure = append(structure, "literal:"+value.Value())
			}
		},
	})
	return structure
}
