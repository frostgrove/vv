package i18n_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

type manualWelcomeArguments struct {
	Note         i18n.Optional[string]
	PendingCount int64 `i18n:"count"`
}

type wrappedOptional struct {
	i18n.Optional[string]
	Extra string
}

func TestStructDefinitionPublicDeclarativeDX(t *testing.T) {
	snapshot, err := i18n.New(i18n.CatalogSpec{
		Revision:        "external/v1",
		SourceLocale:    "en",
		DefaultLocale:   "en",
		DefaultTimeZone: "UTC",
		Supported:       []string{"en"},
		Modules: []i18n.Module{{Name: "mail", Messages: []i18n.MessageSpec{{
			ID:          "welcome",
			Revision:    "welcome/v1",
			Description: "A public manual typed-definition example.",
			Source:      "{$count :number} pending for {$note}",
			Arguments: []i18n.ArgumentSpec{
				{Name: "count", Type: i18n.TypeInteger, Required: true},
				{Name: "note", Type: i18n.TypeText},
			},
			Output: i18n.OutputPlain,
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	contract, ok := snapshot.ContractRef("mail.welcome")
	if !ok {
		t.Fatal("contract is missing")
	}
	welcome, err := i18n.DefineStruct[manualWelcomeArguments](snapshot, contract)
	if err != nil {
		t.Fatal(err)
	}
	message, err := welcome.Bind(manualWelcomeArguments{PendingCount: 21, Note: i18n.Some("Ada")})
	if err != nil {
		t.Fatal(err)
	}
	view, err := snapshot.View(i18n.ViewSpec{
		Resolution:   snapshot.Resolve(i18n.Exact(i18n.SourceExplicit, "en")),
		Presentation: i18n.PresentationNoIsolation,
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := view.Render(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Text, "21") || !strings.Contains(rendered.Text, "Ada") {
		t.Fatalf("rendered text = %q", rendered.Text)
	}
}

func TestStructDefinitionRequiresTheExactPublicOptionalContainer(t *testing.T) {
	snapshot, err := i18n.New(i18n.CatalogSpec{
		Revision:        "external/v1",
		SourceLocale:    "en",
		DefaultLocale:   "en",
		DefaultTimeZone: "UTC",
		Supported:       []string{"en"},
		Modules: []i18n.Module{{Name: "mail", Messages: []i18n.MessageSpec{{
			ID:          "optional",
			Revision:    "optional/v1",
			Description: "An exact optional container contract.",
			Source:      "{$value}",
			Arguments:   []i18n.ArgumentSpec{{Name: "value", Type: i18n.TypeText}},
			Output:      i18n.OutputPlain,
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	type exactAlias = i18n.Optional[string]
	type exactArguments struct{ Value exactAlias }
	if _, err := i18n.NewStructDefinition[exactArguments](snapshot, "mail.optional"); err != nil {
		t.Fatalf("exact alias error = %v", err)
	}
	type wrappedArguments struct{ Value wrappedOptional }
	if _, err := i18n.NewStructDefinition[wrappedArguments](snapshot, "mail.optional"); !errors.Is(err, i18n.ErrInvalidMessage) {
		t.Fatalf("embedded wrapper error = %v", err)
	}
}
