package i18n

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestExportPublicIsDeterministicTypedAndLeakFree(t *testing.T) {
	publicMessage := MessageSpec{
		ID:          "public",
		Revision:    "public-contract-1",
		Source:      "{#strong}SECRET_SOURCE {$required}{/strong}",
		Description: "SECRET_DESCRIPTION",
		Arguments: []ArgumentSpec{
			{Name: "required", Type: TypeText, Required: true},
			{Name: "optional", Type: TypeBool},
			{Name: "nullable", Type: TypeInteger, Required: true, Nullable: true},
			{Name: "both", Type: TypeEnum, Nullable: true, Values: []string{"alpha", "beta"}},
			{Name: "unsigned", Type: TypeUnsignedInteger, Required: true},
			{Name: "huge", Type: TypeBigInteger, Required: true},
			{Name: "decimal", Type: TypeDecimal, Required: true},
			{Name: "price", Type: TypeMoney, Required: true},
			{Name: "day", Type: TypeDate, Required: true},
			{Name: "moment", Type: TypeInstant, Required: true},
		},
		Output:   OutputRich,
		Markup:   []string{"strong"},
		Override: OverrideAny,
		Public:   true,
		Translations: []Translation{{
			Locale:           "ru",
			Text:             "{#strong}SECRET_TRANSLATION {$required}{/strong}",
			Review:           ReviewApproved,
			ContractRevision: "public-contract-1",
		}},
	}
	privateMessage := MessageSpec{
		ID:          "private-id",
		Revision:    "private-contract-1",
		Source:      "SECRET_PRIVATE_SOURCE",
		Description: "SECRET_PRIVATE_DESCRIPTION",
		Output:      OutputPlain,
	}
	spec := testCatalog(publicMessage, privateMessage)
	spec.Supported = []string{"en", "ru"}
	spec.Capabilities = []Capability{CapabilityDateTime}
	spec.Overrides = []Override{{
		Key:              "app.public",
		Locale:           "en",
		Text:             "{#strong}SECRET_APPLICATION_OVERRIDE {$required}{/strong}",
		ContractRevision: "public-contract-1",
		Review:           ReviewApproved,
	}}
	snapshot := mustSnapshot(t, spec)

	first, err := ExportPublic(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ExportPublic(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("public export is not byte deterministic")
	}
	if want := ExpectedPublicExportAddress(first.Manifest, first.TypeScript); first.Address != want {
		t.Fatalf("content address = %q, want %q", first.Address, want)
	}
	mutatedTypes := append([]byte(nil), first.TypeScript...)
	mutatedTypes = append(mutatedTypes, []byte("\nexport type Added = never;\n")...)
	if ExpectedPublicExportAddress(first.Manifest, mutatedTypes) == first.Address {
		t.Fatal("TypeScript bytes are absent from the public content address")
	}
	for _, secret := range []string{
		"SECRET_SOURCE",
		"SECRET_DESCRIPTION",
		"SECRET_TRANSLATION",
		"SECRET_APPLICATION_OVERRIDE",
		"SECRET_PRIVATE_SOURCE",
		"SECRET_PRIVATE_DESCRIPTION",
		"app.private-id",
	} {
		if strings.Contains(string(first.Manifest), secret) || strings.Contains(string(first.TypeScript), secret) {
			t.Fatalf("public export leaked %q", secret)
		}
	}

	var manifest struct {
		Schema           string   `json:"schema"`
		Generator        string   `json:"generator"`
		ValueContract    string   `json:"valueContract"`
		TypeScriptTarget string   `json:"typeScriptTarget"`
		WireFormat       string   `json:"wireFormat"`
		Capabilities     []string `json:"capabilities"`
		FormattingParity bool     `json:"formattingParity"`
		Messages         []struct {
			ID        string `json:"id"`
			Revision  string `json:"revision"`
			Digest    string `json:"digest"`
			Arguments []struct {
				Name     string   `json:"name"`
				Type     string   `json:"type"`
				Required bool     `json:"required"`
				Nullable bool     `json:"nullable"`
				Values   []string `json:"values"`
			} `json:"arguments"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(first.Manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != PublicContractSchema || manifest.Generator != PublicTypeScriptGenerator || manifest.ValueContract != PublicValueContract || manifest.TypeScriptTarget != PublicTypeScriptTarget || manifest.WireFormat != PublicWireFormat || manifest.FormattingParity || !reflect.DeepEqual(manifest.Capabilities, []string{"date_time"}) || len(manifest.Messages) != 1 {
		t.Fatalf("manifest envelope = %#v", manifest)
	}
	message := manifest.Messages[0]
	if message.ID != "app.public" || message.Revision != "public-contract-1" || len(message.Digest) != 64 || len(message.Arguments) != 10 {
		t.Fatalf("public contract identity = %#v", message)
	}
	if message.Arguments[0].Name != "required" || !message.Arguments[0].Required || message.Arguments[0].Nullable {
		t.Fatalf("required shape = %#v", message.Arguments[0])
	}
	if message.Arguments[3].Type != TypeEnum.String() || !reflect.DeepEqual(message.Arguments[3].Values, []string{"alpha", "beta"}) {
		t.Fatalf("enum shape = %#v", message.Arguments[3])
	}

	types := string(first.TypeScript)
	for _, want := range []string{
		"export type FormattingParity = false;",
		`readonly "required": string;`,
		`readonly "optional"?: boolean;`,
		`readonly "nullable": Int64String | null;`,
		`readonly "both"?: "alpha" | "beta" | null;`,
		`readonly "unsigned": UInt64String;`,
		`readonly "huge": BigIntegerString;`,
		`readonly "decimal": DecimalString;`,
		`readonly "price": MoneyValue;`,
		`readonly "day": CalendarDate;`,
		`readonly "moment": Instant;`,
		`readonly "app.public":`,
		`readonly output: "rich";`,
	} {
		if !strings.Contains(types, want) {
			t.Fatalf("TypeScript contract does not contain %q:\n%s", want, types)
		}
	}
	if !strings.Contains(strings.ToLower(types), "formatting parity is not provided") {
		t.Fatalf("TypeScript export does not disclaim formatting parity:\n%s", types)
	}

	tenant := reviewedOverride(t, snapshot, Override{
		Key:              "app.public",
		Locale:           "en",
		Text:             "{#strong}SECRET_TENANT_OVERRIDE {$required}{/strong}",
		ContractRevision: "public-contract-1",
		Review:           ReviewApproved,
	})
	overlaid, err := snapshot.Overlay(TenantOverlay("tenant-1", tenant))
	if err != nil {
		t.Fatal(err)
	}
	afterOverlay, err := ExportPublic(overlaid)
	if err != nil {
		t.Fatal(err)
	}
	if afterOverlay.Address != first.Address || !reflect.DeepEqual(afterOverlay.Manifest, first.Manifest) || !reflect.DeepEqual(afterOverlay.TypeScript, first.TypeScript) {
		t.Fatal("tenant override changed a type-only public export")
	}
	if strings.Contains(string(afterOverlay.Manifest), "SECRET_TENANT_OVERRIDE") {
		t.Fatal("tenant override leaked into the public manifest")
	}

	first.Manifest[0] ^= 0xff
	first.TypeScript[0] ^= 0xff
	fresh, err := ExportPublic(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fresh, second) {
		t.Fatal("caller mutation changed later public exports")
	}
}

func TestExportPublicAddressChangesOnlyForPublicContracts(t *testing.T) {
	public := simpleMessage("public", "public")
	public.Public = true
	private := simpleMessage("private", "private")
	base := testCatalog(public, private)
	base.Supported = []string{"en"}
	baseExport, err := ExportPublic(mustSnapshot(t, base))
	if err != nil {
		t.Fatal(err)
	}

	privateChange := cloneCatalogSpecForPseudo(base)
	privateChange.Modules[0].Messages[1].Revision = "private-2"
	privateExport, err := ExportPublic(mustSnapshot(t, privateChange))
	if err != nil {
		t.Fatal(err)
	}
	if privateExport.Address != baseExport.Address {
		t.Fatal("private contract changed the public content address")
	}

	publicChange := cloneCatalogSpecForPseudo(base)
	publicChange.Modules[0].Messages[0].Revision = "public-2"
	publicExport, err := ExportPublic(mustSnapshot(t, publicChange))
	if err != nil {
		t.Fatal(err)
	}
	if publicExport.Address == baseExport.Address {
		t.Fatal("public contract change did not change the content address")
	}
}

func TestExportPublicUsesStableDistinctTypeNamesAndRejectsNilSnapshot(t *testing.T) {
	if _, err := ExportPublic(nil); err == nil {
		t.Fatal("nil snapshot was accepted")
	}
	first := simpleMessage("foo-bar", "first")
	first.Public = true
	second := simpleMessage("foo_bar", "second")
	second.Public = true
	spec := testCatalog(first, second)
	spec.Supported = []string{"en"}
	exported, err := ExportPublic(mustSnapshot(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	firstName := goExportedIdentifier("app.foo-bar", "Message") + "Args"
	secondName := goExportedIdentifier("app.foo_bar", "Message") + "Args"
	if firstName == secondName || !strings.Contains(string(exported.TypeScript), "interface "+firstName) || !strings.Contains(string(exported.TypeScript), "interface "+secondName) {
		t.Fatalf("stable TypeScript names = %q and %q\n%s", firstName, secondName, exported.TypeScript)
	}
}
