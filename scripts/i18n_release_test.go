package scripts

import (
	"os"
	"strings"
	"testing"
)

func TestI18nReleaseRunsConsumerGateBeforeTags(t *testing.T) {
	content, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	consumer := strings.Index(source, `"$SCRIPT_DIR/i18n-consumer.sh"`)
	tag := strings.Index(source, "git tag -a")
	if consumer < 0 || tag < 0 || consumer >= tag {
		t.Fatal("the i18n standalone consumer gate must run before tag creation")
	}
}

func TestI18nConsumerFixtureIsUsedWithoutWorkspaceOrLocalReplace(t *testing.T) {
	content, err := os.ReadFile("i18n-consumer.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	if !strings.Contains(source, "GOWORK=off") || !strings.Contains(source, "i18n-consumer-fixture/main.go.txt") {
		t.Fatal("consumer gate is not strict or does not use its fixture")
	}
	if strings.Contains(source, "go mod edit -replace") {
		t.Fatal("consumer gate must not install a local replace")
	}
	for _, fragment := range []string{
		"git archive",
		`HEAD:i18n`,
		`HEAD:crud/rpc/crudgrpc`,
		`GOPROXY="$consumer_proxy"`,
		`GONOSUMDB="$VV_MODULE,$VV_MODULE/*"`,
		`"$VV_MODULE/cmd/vv@$version" -dir . -types Product -adapter -out vv_gen.go`,
		`grep -q 'func MountProduct' vv_gen.go`,
		`"$VV_MODULE/crud/rpc/crudgrpc@$version"`,
		`"$GO" run -race .`,
		`"$GO" install "$VV_MODULE/i18n/cmd/vv-i18n@$version"`,
		`VV_I18N_SOURCE_OUT="$directory/messages.source.json"`,
		`"$directory/bin/vv-i18n" export-ts -source "$directory/messages.source.json" -publication-root "$directory/public.i18n"`,
		`-publication-root "$directory/public.i18n" -check`,
		`i18n-consumer-fixture/publication_reader.go.txt`,
		`run publication_reader.go "$directory/public.i18n"`,
		`frostgrove.i18n.publication/v1`,
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("consumer gate does not contain %q", fragment)
		}
	}
	if strings.Contains(source, "\n\t"+`GOWORK=off "$GO" get "$VV_MODULE/i18n@$version"`) {
		t.Fatal("consumer gate must resolve the unpublished version through its local proxy")
	}
}

func TestI18nPublicationReaderVerifiesTheCompleteWireContract(t *testing.T) {
	content, err := os.ReadFile("i18n-consumer-fixture/publication_reader.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	for _, fragment := range []string{
		`decoder.DisallowUnknownFields()`,
		`publication pointer is not canonical`,
		`messages.public.json`,
		`messages.d.ts`,
		`i18n.ExpectedPublicExportAddress`,
		`frostgrove.i18n.publication-generation/v1`,
		`binary.BigEndian.PutUint64`,
		`os.OpenRoot`,
		`os.SameFile`,
		`Readdirnames`,
		`io.LimitReader`,
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("publication reader does not verify %q", fragment)
		}
	}
	for _, fragment := range []string{`os.ReadDir(`, `os.ReadFile(`, `filepath.EvalSymlinks(`} {
		if strings.Contains(source, fragment) {
			t.Fatalf("publication reader uses unbounded path-based read %q", fragment)
		}
	}
}

func TestI18nConsumerUsesTheNestedModuleProvenanceSeam(t *testing.T) {
	content, err := os.ReadFile("i18n-consumer-fixture/main.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	if !strings.Contains(source, "i18n.LocalizedMessageSource") {
		t.Fatal("consumer fixture does not exercise the nested-module provenance seam")
	}
	if !strings.Contains(source, "snapshot.ErrorPlan") || !strings.Contains(source, "view.ErrorMessages") {
		t.Fatal("consumer fixture does not exercise the one-view error plan seam")
	}
	for _, fragment := range []string{
		"MountProduct(",
		"ProductMapper{}",
		"crudgrpc.ServingFor",
		"crudhttp.WithMessages(source)",
		"crudgrpc.WithMessages(source)",
		`response.Header().Get("Content-Language") != "fr"`,
		`violation.GetLocalizedMessage().GetLocale() != "fr"`,
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("consumer fixture does not exercise %q", fragment)
		}
	}
	if strings.Contains(source, "errs.LocalizedMessageSource") {
		t.Fatal("consumer fixture must consume the public localized source through the shared errs contract")
	}
}
