package i18n

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGenerateGoCoversEveryArgumentTypeAndResolvesIdentifiers(t *testing.T) {
	message := MessageSpec{
		ID:          "type",
		Revision:    "contract-1",
		Source:      "Generated contract",
		Description: "Every generated argument shape",
		Arguments: []ArgumentSpec{
			{Name: "text", Type: TypeText, Required: true},
			{Name: "flag", Type: TypeBool},
			{Name: "signed", Type: TypeInteger, Required: true, Nullable: true},
			{Name: "unsigned", Type: TypeUnsignedInteger, Required: true},
			{Name: "huge", Type: TypeBigInteger, Required: true},
			{Name: "decimal", Type: TypeDecimal, Nullable: true},
			{Name: "price", Type: TypeMoney, Required: true},
			{Name: "day", Type: TypeDate, Required: true},
			{Name: "moment", Type: TypeInstant, Required: true},
			{Name: "state", Type: TypeEnum, Required: true, Values: []string{"in-progress", "in_progress", "type"}},
		},
		Output: OutputPlain,
	}
	enumOne := simpleMessage("foo", "Enum one", ArgumentSpec{Name: "bar", Type: TypeEnum, Required: true, Values: []string{"args"}})
	enumTwo := simpleMessage("_", "Enum two", ArgumentSpec{Name: "foo-bar", Type: TypeEnum, Required: true, Values: []string{"args"}})
	spec := testCatalog(
		message,
		simpleMessage("foo-bar", "First"),
		simpleMessage("foo_bar", "Second"),
		simpleMessage("foo-bar-enum", "Constant namespace collision"),
		enumOne,
		enumTwo,
	)
	spec.Capabilities = []Capability{CapabilityDateTime}
	snapshot := mustSnapshot(t, spec)
	first, err := GenerateGo(snapshot, GoGeneratorSpec{Package: "messages"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateGo(snapshot, GoGeneratorSpec{Package: "messages"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Go generation is not byte deterministic")
	}
	var unformatted []byte
	third, err := generateGoContext(context.Background(), snapshot, GoGeneratorSpec{Package: "messages"}, func(source []byte) ([]byte, error) {
		unformatted = bytes.Clone(source)
		return format.Source(source)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unformatted, third) {
		t.Fatalf("generated source is not canonical before formatter:\ninput: %q\nformatted: %q", unformatted, third)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "messages.go", first, parser.AllErrors)
	if err != nil {
		t.Fatalf("generated Go is not parseable: %v\n%s", err, first)
	}
	assertGeneratedGoIdentifiersUnique(t, parsed)
	text := string(first)
	for _, want := range []string{
		"vvi18n.Optional[bool]",
		"vvi18n.Optional[int64]",
		"uint64",
		"*big.Int",
		"vvi18n.Optional[string]",
		"Money",
		"vvi18n.DateValue",
		"time.Time",
		"Contract: vvi18n.ContractRef{",
		"vvi18n.Define",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated source does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "map[") {
		t.Fatalf("generated public contracts contain a map:\n%s", text)
	}
}

func TestGenerateGoCompilesRunsAndRejectsContractDrift(t *testing.T) {
	message := MessageSpec{
		ID:          "notice",
		Revision:    "contract-1",
		Source:      "Generated notice",
		Description: "Generated consumer fixture",
		Arguments: []ArgumentSpec{
			{Name: "text", Type: TypeText, Required: true},
			{Name: "flag", Type: TypeBool},
			{Name: "signed", Type: TypeInteger, Required: true, Nullable: true},
			{Name: "unsigned", Type: TypeUnsignedInteger, Required: true},
			{Name: "huge", Type: TypeBigInteger, Required: true},
			{Name: "decimal", Type: TypeDecimal, Nullable: true},
			{Name: "price", Type: TypeMoney, Required: true},
			{Name: "day", Type: TypeDate, Required: true},
			{Name: "moment", Type: TypeInstant, Required: true},
			{Name: "state", Type: TypeEnum, Required: true, Values: []string{"ready", "done"}},
		},
		Output: OutputPlain,
	}
	spec := testCatalog(message)
	spec.Supported = []string{"en"}
	spec.Capabilities = []Capability{CapabilityDateTime}
	snapshot := mustSnapshot(t, spec)
	generated, err := GenerateGo(snapshot, GoGeneratorSpec{Package: "messages"})
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	packageDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module generated.test/messages\n\ngo 1.26.5\n\nrequire github.com/frostgrove/vv/i18n v0.0.0\n\nreplace github.com/frostgrove/vv/i18n => " + packageDirectory + "\n"
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(packageDirectory, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "go.sum"), sums, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "messages.go"), generated, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "messages_test.go"), []byte(generatedConsumerTest), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-race", "-count=1", "./...")
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated consumer failed: %v\n%s\n%s", err, output, generated)
	}
}

func TestGenerateGoRejectsIncompleteRequests(t *testing.T) {
	if _, err := GenerateGo(nil, GoGeneratorSpec{Package: "messages"}); err == nil {
		t.Fatal("nil snapshot was accepted")
	}
	snapshot := mustSnapshot(t, testCatalog(simpleMessage("m", "message")))
	for _, name := range []string{"", "package", "bad-name"} {
		if _, err := GenerateGo(snapshot, GoGeneratorSpec{Package: name}); err == nil {
			t.Fatalf("invalid package %q was accepted", name)
		}
	}
}

func TestGeneratedNamesRemainStableWhenANormalizedCollisionIsAdded(t *testing.T) {
	before, err := uniqueGeneratedIdentifiers([]string{"app.foo-bar"}, "Message")
	if err != nil {
		t.Fatal(err)
	}
	after, err := uniqueGeneratedIdentifiers([]string{"app.foo-bar", "app.foo_bar"}, "Message")
	if err != nil {
		t.Fatal(err)
	}
	if before[0] != after[0] {
		t.Fatalf("existing generated name changed from %q to %q", before[0], after[0])
	}
	if after[0] == after[1] || !strings.HasPrefix(after[0], "AppFooBarX") || !strings.HasPrefix(after[1], "AppFooBarX") {
		t.Fatalf("stable collision names = %v", after)
	}
}

func assertGeneratedGoIdentifiersUnique(t *testing.T, file *ast.File) {
	t.Helper()
	topLevel := make(map[string]bool)
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if topLevel[spec.Name.Name] {
						t.Fatalf("duplicate generated type %q", spec.Name.Name)
					}
					topLevel[spec.Name.Name] = true
					structure, ok := spec.Type.(*ast.StructType)
					if !ok {
						continue
					}
					fields := make(map[string]bool)
					for _, field := range structure.Fields.List {
						for _, name := range field.Names {
							if fields[name.Name] {
								t.Fatalf("duplicate field %q in %q", name.Name, spec.Name.Name)
							}
							fields[name.Name] = true
						}
					}
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						if topLevel[name.Name] {
							t.Fatalf("duplicate generated constant %q", name.Name)
						}
						topLevel[name.Name] = true
					}
				}
			}
		case *ast.FuncDecl:
			if topLevel[declaration.Name.Name] {
				t.Fatalf("duplicate generated function %q", declaration.Name.Name)
			}
			topLevel[declaration.Name.Name] = true
		}
	}
}

const generatedConsumerTest = `package messages

import (
	"math/big"
	"testing"
	"time"

	vvi18n "github.com/frostgrove/vv/i18n"
)

func consumerSpec(stale bool) vvi18n.CatalogSpec {
	textType := vvi18n.TypeText
	if stale {
		textType = vvi18n.TypeBool
	}
	return vvi18n.CatalogSpec{
		Revision: "catalog-1",
		SourceLocale: "en",
		DefaultLocale: "en",
		Supported: []string{"en"},
		Capabilities: []vvi18n.Capability{vvi18n.CapabilityDateTime},
		Modules: []vvi18n.Module{{Name: "app", Messages: []vvi18n.MessageSpec{{
			ID: "notice",
			Revision: "contract-1",
			Source: "Generated notice",
			Description: "Generated consumer fixture",
			Arguments: []vvi18n.ArgumentSpec{
				{Name: "text", Type: textType, Required: true},
				{Name: "flag", Type: vvi18n.TypeBool},
				{Name: "signed", Type: vvi18n.TypeInteger, Required: true, Nullable: true},
				{Name: "unsigned", Type: vvi18n.TypeUnsignedInteger, Required: true},
				{Name: "huge", Type: vvi18n.TypeBigInteger, Required: true},
				{Name: "decimal", Type: vvi18n.TypeDecimal, Nullable: true},
				{Name: "price", Type: vvi18n.TypeMoney, Required: true},
				{Name: "day", Type: vvi18n.TypeDate, Required: true},
				{Name: "moment", Type: vvi18n.TypeInstant, Required: true},
				{Name: "state", Type: vvi18n.TypeEnum, Required: true, Values: []string{"ready", "done"}},
			},
			Output: vvi18n.OutputPlain,
		}}}},
	}
}

func TestGeneratedContract(t *testing.T) {
	snapshot, err := vvi18n.New(consumerSpec(false))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCatalog(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	args := AppNoticeArgs{
		Text: "hello",
		Signed: vvi18n.NullValue[int64](),
		Unsigned: 7,
		Huge: big.NewInt(8),
		Decimal: vvi18n.Some("1.20"),
		Price: Money{Amount: "4.50", Currency: "KZT"},
		Day: vvi18n.DateValue{Year: 2026, Month: time.September, Day: 9},
		Moment: time.Date(2026, time.September, 9, 10, 0, 0, 0, time.UTC),
		State: AppNoticeStateEnumReady,
	}
	message, err := catalog.AppNotice.Bind(args)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Arguments()) != 9 || !message.Arguments()[1].IsNull() {
		t.Fatalf("generated encoder lost presence semantics: %#v", message.Arguments())
	}
	args.Decimal = vvi18n.Optional[string]{}
	if _, err := catalog.AppNotice.Bind(args); err != nil {
		t.Fatalf("absent optional nullable value was rejected: %v", err)
	}
	args.Flag = vvi18n.NullValue[bool]()
	if _, err := catalog.AppNotice.Bind(args); err == nil {
		t.Fatal("explicit null for non-null optional value was accepted")
	}
	args.Flag = vvi18n.Optional[bool]{}
	args.Price.Currency = "X"
	if _, err := catalog.AppNotice.Bind(args); err == nil {
		t.Fatal("generated money representation accepted an invalid currency")
	}
	args.Price.Currency = "KZT"
	args.Signed = vvi18n.Optional[int64]{}
	if _, err := catalog.AppNotice.Bind(args); err == nil {
		t.Fatal("absent required nullable value was accepted")
	}

	stale, err := vvi18n.New(consumerSpec(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCatalog(stale); err == nil {
		t.Fatal("generated literal ContractRef accepted a changed schema")
	}
}
`
