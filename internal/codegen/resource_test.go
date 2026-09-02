package codegen

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const wireModel = `package m

import "time"

type Account struct {
	ID        int64     @db:"id,pk,auto"@
	Email     string    @db:"email"@
	Password  string    @db:"password,secret"@
	Locked    bool      @db:"locked"@
	CreatedAt time.Time @db:"created_at,generated"@
}
`

func wirePackage(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(tags(source)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func generateWire(t *testing.T, dir string) (string, string) {
	t.Helper()
	if err := RunResource(&ResourceOptions{Dir: dir}); err != nil {
		t.Fatalf("generating the wire bodies: %v", err)
	}
	return readFile(t, filepath.Join(dir, "vv_wire_gen.go")), readFile(t, filepath.Join(dir, "resource.manifest.yml"))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func publish(t *testing.T, dir, model, body string, fields []string, confirmed bool) {
	t.Helper()
	path := filepath.Join(dir, "resource.manifest.yml")
	var document manifestDocument
	if err := json.Unmarshal([]byte(readFile(t, path)), &document); err != nil {
		t.Fatal(err)
	}
	found := false
	for index := range document.Resources {
		if document.Resources[index].Model != model {
			continue
		}
		found = true
		entry := &document.Resources[index]
		switch body {
		case "create":
			entry.Create.Fields, entry.Create.Confirmed = fields, confirmed
		case "patch":
			entry.Patch.Fields, entry.Patch.Confirmed = fields, confirmed
		default:
			entry.Response.Fields, entry.Response.Confirmed = fields, confirmed
		}
	}
	if !found {
		t.Fatalf("the manifest has no %s to edit:\n%s", model, readFile(t, path))
	}
	edited, err := marshalManifest(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestThePatchBodyPublishesLessThanTheUpdateDTOWrites(t *testing.T) {
	dir := wirePackage(t, wireModel)
	out, _ := generateWire(t, dir)

	if err := Run(&Options{Dir: dir, WithDTO: true, NoRepo: true, Binding: "none"}); err != nil {
		t.Fatalf("generating the persistence DTO: %v", err)
	}
	persistence := readFile(t, filepath.Join(dir, "vv_gen.go"))

	if !strings.Contains(persistence, "Password *string") {
		t.Fatalf("the update DTO cannot write the column, so the split proves nothing:\n%s", persistence)
	}
	patch := decl(t, out, "type AccountPatch struct {")
	if strings.Contains(patch, "Password") {
		t.Fatalf("the public patch body carries a secret column:\n%s", patch)
	}
	if !declares(patch, "Email") {
		t.Fatalf("the public patch body lost an ordinary column:\n%s", patch)
	}
	if !strings.Contains(out, "out.Email = patch.Email") {
		t.Fatalf("the patch mapper does not carry the public field onto the update DTO:\n%s", out)
	}
	if !strings.Contains(out, `wire.MustCoverPatch[AccountUpdate, AccountPatch]("Password")`) {
		t.Fatalf("the omission is not declared, so nothing would notice a third body:\n%s", out)
	}
}

func TestTheResponseBodyLeavesOutWhatOnlyTheServerReads(t *testing.T) {
	dir := wirePackage(t, wireModel)
	out, _ := generateWire(t, dir)

	response := decl(t, out, "type AccountResponse struct {")
	if strings.Contains(response, "Password") {
		t.Fatalf("a secret column is answered to every client:\n%s", response)
	}
	for _, want := range []string{"ID", "CreatedAt"} {
		if !declares(response, want) {
			t.Fatalf("the response body dropped %s, which every client needs:\n%s", want, response)
		}
	}
	if !strings.Contains(out, `wire.MustCoverResponse[Account, AccountResponse]("Password")`) {
		t.Fatalf("the omission is not declared:\n%s", out)
	}
}

func TestNarrowingAPublicBodyInTheManifestNeedsNoConfirmation(t *testing.T) {
	dir := wirePackage(t, wireModel)
	generateWire(t, dir)

	publish(t, dir, "Account", "patch", []string{"Email"}, false)
	out, manifest := generateWire(t, dir)

	patch := decl(t, out, "type AccountPatch struct {")
	if strings.Contains(patch, "Locked") {
		t.Fatalf("the manifest narrowed the body and the generator ignored it:\n%s", patch)
	}
	if !strings.Contains(out, `wire.MustCoverPatch[AccountUpdate, AccountPatch]("Locked", "Password")`) {
		t.Fatalf("the narrowed column is not declared as an omission:\n%s", out)
	}
	if strings.Contains(manifest, `"widened": [`) && !strings.Contains(manifest, `"widened": []`) {
		t.Fatalf("narrowing was recorded as a widening:\n%s", manifest)
	}
}

func TestWideningAPublicBodyRefusesUntilTheManifestConfirmsIt(t *testing.T) {
	dir := wirePackage(t, wireModel)
	generateWire(t, dir)
	publish(t, dir, "Account", "patch", []string{"Email", "Locked", "Password"}, false)

	err := RunResource(&ResourceOptions{Dir: dir})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("publishing a column the narrowing excludes was accepted: %v", err)
	}
	if len(confirmation.Bodies) != 1 || confirmation.Bodies[0] != "Account patch" {
		t.Fatalf("the refusal does not name the body to confirm: %v", confirmation.Bodies)
	}
	out := readFile(t, filepath.Join(dir, "vv_wire_gen.go"))
	if strings.Contains(decl(t, out, "type AccountPatch struct {"), "Password") {
		t.Fatalf("the unconfirmed body was generated anyway:\n%s", out)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "resource.manifest.yml")), `"Password"`) {
		t.Fatal("the manifest does not record what is waiting to be confirmed")
	}

	t.Run("and the same widening, confirmed, generates", func(t *testing.T) {
		publish(t, dir, "Account", "patch", []string{"Email", "Locked", "Password"}, true)
		out, _ := generateWire(t, dir)
		if !declares(decl(t, out, "type AccountPatch struct {"), "Password") {
			t.Fatalf("a confirmed widening was still refused the column:\n%s", out)
		}
	})
}

func TestAConfirmationDoesNotSurviveAChangeToWhatItWasDerivedFrom(t *testing.T) {
	dir := wirePackage(t, wireModel)
	generateWire(t, dir)
	publish(t, dir, "Account", "patch", []string{"Email", "Locked", "Password"}, true)
	if err := RunResource(&ResourceOptions{Dir: dir}); err != nil {
		t.Fatalf("a widening confirmed beside the fields it names was still refused: %v", err)
	}

	grown := strings.Replace(tags(wireModel), "\tLocked    bool      `db:\"locked\"`",
		"\tLocked    bool      `db:\"locked\"`\n\tNickname  string    `db:\"nickname\"`", 1)
	if grown == tags(wireModel) {
		t.Fatal("the fixture did not gain a column, so this measures nothing")
	}
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(grown), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RunResource(&ResourceOptions{Dir: dir})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("a confirmation outlived the model shape it was given for: %v", err)
	}
	if len(confirmation.Bodies) != 1 || confirmation.Bodies[0] != "Account patch" {
		t.Fatalf("the refusal names %v, want the body whose derivation moved", confirmation.Bodies)
	}
}

func TestAFieldTheModelCannotPublishIsRefusedRatherThanConfirmed(t *testing.T) {
	dir := wirePackage(t, wireModel)
	generateWire(t, dir)
	publish(t, dir, "Account", "patch", []string{"Email", "CreatedAt"}, true)

	err := RunResource(&ResourceOptions{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "CreatedAt") {
		t.Fatalf("a generated column was publishable as a patch field: %v", err)
	}
	var confirmation *ConfirmationError
	if errors.As(err, &confirmation) {
		t.Fatal("a column no UPDATE can write was offered as something to confirm")
	}
}

func TestAStaleWireArtefactFailsTheCheck(t *testing.T) {
	dir := wirePackage(t, wireModel)
	generateWire(t, dir)

	if err := RunResource(&ResourceOptions{Dir: dir, Check: true}); err != nil {
		t.Fatalf("the artefacts it had just written were called stale: %v", err)
	}

	grown := strings.Replace(tags(wireModel), "\tLocked    bool      `db:\"locked\"`",
		"\tLocked    bool      `db:\"locked\"`\n\tNickname  string    `db:\"nickname\"`", 1)
	if grown == tags(wireModel) {
		t.Fatal("the fixture did not gain a column, so this measures nothing")
	}
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(grown), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RunResource(&ResourceOptions{Dir: dir, Check: true})
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("a column the wire bodies never saw passed the check: %v", err)
	}
	if len(drift.Paths) != 2 {
		t.Fatalf("the check names %v, but both artefacts are behind the model", drift.Paths)
	}
}

func TestTheCheckWritesNothing(t *testing.T) {
	dir := wirePackage(t, wireModel)
	if err := RunResource(&ResourceOptions{Dir: dir, Check: true}); err == nil {
		t.Fatal("a package with no artefacts at all passed the check")
	}
	for _, name := range []string{"vv_wire_gen.go", "resource.manifest.yml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("the check created %s", name)
		}
	}
}

func TestAnUnrelatedManifestIsNeverOverwritten(t *testing.T) {
	dir := wirePackage(t, wireModel)
	path := filepath.Join(dir, "resource.manifest.yml")
	authored := "kind: something-else\n"
	if err := os.WriteFile(path, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}
	err := RunResource(&ResourceOptions{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "unrelated manifest") {
		t.Fatalf("RunResource over an authored file = %v, want a refusal", err)
	}
	if readFile(t, path) != authored {
		t.Fatal("the authored file was overwritten")
	}
}

func TestTheGeneratedWireBodiesRefuseToStartWhenTheyStopCoveringTheirShapes(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	dir := wirePackage(t, wireModel)
	out, _ := generateWire(t, dir)
	if err := Run(&Options{Dir: dir, WithDTO: true, NoRepo: true, Binding: "none"}); err != nil {
		t.Fatal(err)
	}
	persistence := readFile(t, filepath.Join(dir, "vv_gen.go"))

	t.Run("the untampered bodies start", func(t *testing.T) {
		if response, err := runWireGenerated(t, tags(wireModel), persistence, out); err != nil {
			t.Fatalf("the generated wire bodies refused to start: %v\n%s\n---- generated ----\n%s", err, response, out)
		}
	})

	t.Run("a field deleted from the response body", func(t *testing.T) {
		cut := withoutResponseEmail(t, out)
		response, err := runWireGenerated(t, tags(wireModel), persistence, cut)
		if err == nil {
			t.Fatalf("a response body that silently drops a column started cleanly:\n%s", response)
		}
		if !strings.Contains(response, "Email") {
			t.Fatalf("the refusal does not name the column somebody has to act on:\n%s", response)
		}
	})
}

func runWireGenerated(t *testing.T, model, persistence, wireSource string) (string, error) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "model"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module wirecheck\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => "+root+"\n")
	write(filepath.Join("model", "model.go"), model)
	write(filepath.Join("model", "vv_gen.go"), persistence)
	write(filepath.Join("model", "vv_wire_gen.go"), wireSource)
	write("main.go", "package main\n\nimport _ \"wirecheck/model\"\n\nfunc main() {}\n")

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	response, err := cmd.CombinedOutput()
	return string(response), err
}

func withoutResponseEmail(t *testing.T, generated string) string {
	t.Helper()
	start := strings.Index(generated, "type AccountResponse struct {")
	if start < 0 {
		t.Fatalf("the fixture has no response body to cut:\n%s", generated)
	}
	var kept []string
	for index, line := range strings.Split(generated, "\n") {
		trimmed := strings.TrimSpace(line)
		field, _, _ := strings.Cut(trimmed, " ")
		if index > strings.Count(generated[:start], "\n") && field == "Email" {
			continue
		}
		if trimmed == "out.Email = model.Email" {
			continue
		}
		kept = append(kept, line)
	}
	cut := strings.Join(kept, "\n")
	if cut == generated {
		t.Fatalf("nothing was cut, so this measures nothing:\n%s", generated)
	}
	return cut
}

func TestTheWireBodiesCanBeWrittenIntoAPackageThatOwnsNeitherTheModelNorItsGenerator(t *testing.T) {
	models := wirePackage(t, strings.Replace(wireModel, "package m", "package ent", 1))
	store := filepath.Join(t.TempDir(), "store")

	err := RunResource(&ResourceOptions{Dir: models, Into: store, Import: "example.com/app/ent", Recursive: false})
	if err != nil {
		t.Fatalf("generating beside an owned package: %v", err)
	}

	out := readFile(t, filepath.Join(store, "vv_wire_gen.go"))
	if !strings.HasPrefix(out, "// Code generated by vv. DO NOT EDIT.\n\npackage store\n") {
		t.Fatalf("the output package is not the directory it was written into:\n%s", out)
	}
	if !strings.Contains(out, `"example.com/app/ent"`) {
		t.Fatalf("the model package is not imported:\n%s", out)
	}
	if !strings.Contains(out, "func (AccountPresenter) Response(model ent.Account) AccountResponse {") {
		t.Fatalf("the presenter names the model unqualified, so the file cannot compile:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(models, "vv_wire_gen.go")); !os.IsNotExist(err) {
		t.Fatal("the generator wrote into the package it was only asked to read")
	}

	manifest := readFile(t, filepath.Join(store, "resource.manifest.yml"))
	if !strings.Contains(manifest, `"package": "store"`) {
		t.Fatalf("the manifest belongs to the package it was read from, not the one it sits in:\n%s", manifest)
	}

	t.Run("and -into without -import cannot name the model types", func(t *testing.T) {
		err := RunResource(&ResourceOptions{Dir: models, Into: filepath.Join(t.TempDir(), "store"), Recursive: false})
		if err == nil || !strings.Contains(err.Error(), "-import") {
			t.Fatalf("RunResource into another package with no import path = %v, want a refusal naming -import", err)
		}
	})
}
