package i18n

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestGeneratedOutputsHonorSnapshotBounds(t *testing.T) {
	message := simpleMessage("public", "Public")
	message.Public = true
	spec := testCatalog(message)
	spec.Supported = []string{"en", "fr"}
	probe := mustSnapshot(t, spec)
	maximum := snapshotCatalogBytes(probe)
	spec.Limits.MaxCatalogBytes = maximum
	snapshot := mustSnapshot(t, spec)

	if _, err := GenerateGo(snapshot, GoGeneratorSpec{Package: "messages"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("GenerateGo() limit error = %v", err)
	}
	if _, err := ExportPublic(snapshot); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ExportPublic() limit error = %v", err)
	}
	if _, err := GenerateGo(snapshot, GoGeneratorSpec{Package: strings.Repeat("a", snapshot.limits.MaxIdentifierBytes+1)}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("GenerateGo() package limit error = %v", err)
	}
}

func TestPseudoRejectsAddedMaterialAndCardinalityBeforeCloning(t *testing.T) {
	spec := testCatalog(simpleMessage("notice", "Notice"))
	spec.Supported = []string{"en", "fr"}
	probe := mustSnapshot(t, spec)
	counter, _, err := pseudoInputMaterial(spec, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	spec.Limits.MaxCatalogBytes = max(counter.bytes, snapshotCatalogBytes(probe))
	if _, err := Pseudo(spec, PseudoSpec{Locale: "fr", Mode: PseudoAccent}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Pseudo() material limit error = %v", err)
	}

	spec.Limits.MaxCatalogBytes = 0
	spec.Limits.MaxTranslations = 1
	if _, err := Pseudo(spec, PseudoSpec{Locale: "fr", Mode: PseudoAccent}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Pseudo() translation limit error = %v", err)
	}
}

func TestBoundedPublicManifestMatchesStandardJSON(t *testing.T) {
	message := simpleMessage("public", "Public", ArgumentSpec{Name: "state", Type: TypeEnum, Required: true, Values: []string{"<ready>", "done"}})
	message.Public = true
	snapshot := mustSnapshot(t, testCatalog(message))
	messages, err := publicContractMessages(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	manifest := publicContractManifest{
		Schema: PublicContractSchema, Profile: snapshot.Profile(), Generator: PublicTypeScriptGenerator,
		ValueContract: PublicValueContract, TypeScriptTarget: PublicTypeScriptTarget, WireFormat: PublicWireFormat,
		Capabilities: publicCapabilities(snapshot), FormattingParity: false, Messages: messages,
	}
	want, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := marshalPublicManifest(manifest, snapshot.limits.MaxCatalogBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded manifest = %s\nstandard manifest = %s", got, want)
	}
}

func TestBoundedTextBuilderStopsAtTheExactLimit(t *testing.T) {
	builder := newBoundedTextBuilder(5)
	builder.WriteString("123")
	builder.Write([]byte("45"))
	if builder.Overflow() || builder.String() != "12345" {
		t.Fatalf("exact builder = %q, overflow=%v", builder.String(), builder.Overflow())
	}
	builder.WriteString("6")
	if !builder.Overflow() || builder.String() != "12345" {
		t.Fatalf("overflow builder = %q, overflow=%v", builder.String(), builder.Overflow())
	}
}
