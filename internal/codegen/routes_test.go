package codegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const guardedUseCase = `package ops

import (
	"context"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/access"
)

const PermJobsRead auth.Permission = "job.read"

type DeadJobsUseCase struct{}

func (this *DeadJobsUseCase) List(ctx context.Context) error {
	if _, err := access.Require(ctx, PermJobsRead); err != nil {
		return err
	}
	return nil
}
`

const declaringHandler = `package ops

import (
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/gofiber/fiber/v3"
)

type Handler struct{}

func (this *Handler) Access() []authhttp.Endpoint {
	return []authhttp.Endpoint{
		authhttp.Requires(fiber.MethodGet, "/ops/jobs/dead", PermJobsRead),
	}
}
`

func guardedPackage(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func routeOptions(dir string) *RouteOptions {
	return &RouteOptions{Dir: dir}
}

func readManifest(t *testing.T, dir string) routesManifestDocument {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(dir, DefaultRoutesFile))
	if err != nil {
		t.Fatal(err)
	}
	var document routesManifestDocument
	if err := json.Unmarshal(source, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func confirmEveryOperation(t *testing.T, dir string) {
	t.Helper()
	document := readManifest(t, dir)
	changed := false
	for index := range document.Operations {
		if !document.Operations[index].Confirmed {
			document.Operations[index].Confirmed = true
			changed = true
		}
	}
	if !changed {
		t.Fatal("nothing was waiting for confirmation, so confirming it proves nothing")
	}
	source, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultRoutesFile), append(source, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTheRouteOfAnOperationIsInferredFromTheGuardThatEnforcesIt(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"dead-jobs.usecase.go": guardedUseCase,
		"ops.http-handler.go":  declaringHandler,
	})

	err := RunRoutes(routeOptions(dir))
	var confirmation *RouteConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("a route nobody had confirmed was generated: %v", err)
	}
	if len(confirmation.Operations) != 1 || confirmation.Operations[0] != "DeadJobsUseCase.List" {
		t.Fatalf("the refusal names %v, not the operation whose route was inferred", confirmation.Operations)
	}

	document := readManifest(t, dir)
	if len(document.Operations) != 1 {
		t.Fatalf("the manifest carries %d operations", len(document.Operations))
	}
	operation := document.Operations[0]
	if operation.Operation != "DeadJobsUseCase.List" || operation.Source != sourceInferred {
		t.Fatalf("operation = %+v", operation)
	}
	if len(operation.Policy) != 1 || operation.Policy[0] != "PermJobsRead" {
		t.Fatalf("the policy came from somewhere other than the guard: %v", operation.Policy)
	}
	if operation.Method != "GET" || operation.Path != "/ops/jobs/dead" {
		t.Fatalf("route = %s %s, not the one declared beside the guard", operation.Method, operation.Path)
	}
	if operation.Confirmed || operation.Fingerprint == "" {
		t.Fatalf("an inferred pair arrived already confirmed: %+v", operation)
	}
}

func TestAnUnconfirmedOperationLeavesAFileThatWillNotCompile(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"dead-jobs.usecase.go": guardedUseCase,
		"ops.http-handler.go":  declaringHandler,
	})

	if err := RunRoutes(routeOptions(dir)); err == nil {
		t.Fatal("an unconfirmed inference generated a usable file")
	}
	generated := readFile(t, filepath.Join(dir, DefaultRoutesOut))
	if !strings.Contains(generated, confirmationHint) {
		t.Fatalf("the generated file does not say what is missing:\n%s", generated)
	}
	if strings.Contains(generated, "OperationDeadJobsUseCaseList") {
		t.Fatalf("an unconfirmed operation was published anyway:\n%s", generated)
	}
	if !strings.Contains(generated, "vvRouteSet = ") {
		t.Fatalf("the placeholder would compile, so nothing stops a start-up:\n%s", generated)
	}
}

func TestAConfirmedOperationBecomesTheOneValueTheRouteAndTheGuardBothRead(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"dead-jobs.usecase.go": guardedUseCase,
		"ops.http-handler.go":  declaringHandler,
	})
	if err := RunRoutes(routeOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryOperation(t, dir)

	if err := RunRoutes(routeOptions(dir)); err != nil {
		t.Fatalf("a confirmed operation was still refused: %v", err)
	}
	generated := readFile(t, filepath.Join(dir, DefaultRoutesOut))
	for _, want := range []string{
		"var OperationDeadJobsUseCaseList = Operation{",
		`method:      "GET"`,
		`path:        "/ops/jobs/dead"`,
		"permissions: []auth.Permission{PermJobsRead}",
		"func Declarations() []authhttp.Endpoint {",
		"func (this Operation) Permissions() []auth.Permission {",
	} {
		if !strings.Contains(generated, want) {
			t.Fatalf("the generated carrier is missing %q:\n%s", want, generated)
		}
	}
	if strings.Contains(generated, confirmationHint) {
		t.Fatalf("the confirmed run left the placeholder behind:\n%s", generated)
	}
}

func TestChangingThePermissionTheGuardEnforcesWithdrawsTheConfirmation(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"dead-jobs.usecase.go": guardedUseCase,
		"ops.http-handler.go":  declaringHandler,
	})
	if err := RunRoutes(routeOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryOperation(t, dir)
	if err := RunRoutes(routeOptions(dir)); err != nil {
		t.Fatalf("a confirmed operation was refused: %v", err)
	}
	before := readManifest(t, dir).Operations[0]

	moved := strings.ReplaceAll(guardedUseCase, "PermJobsRead", "PermJobsRetry")
	if err := os.WriteFile(filepath.Join(dir, "dead-jobs.usecase.go"), []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ops.http-handler.go"),
		[]byte(strings.ReplaceAll(declaringHandler, "PermJobsRead", "PermJobsRetry")), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RunRoutes(routeOptions(dir))
	var confirmation *RouteConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("a policy nobody re-confirmed generated anyway: %v", err)
	}
	after := readManifest(t, dir).Operations[0]
	if after.Confirmed {
		t.Fatal("the stale confirmation survived a change to the permission the guard enforces")
	}
	if after.Fingerprint == before.Fingerprint {
		t.Fatalf("the fingerprint did not move with the policy: %s", after.Fingerprint)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, DefaultRoutesOut)), confirmationHint) {
		t.Fatal("the previous generated carrier is still there, so the build would not stop")
	}
}

func TestFillingInARouteTheGeneratorCouldNotInferDoesNotWithdrawItsOwnConfirmation(t *testing.T) {
	dir := guardedPackage(t, map[string]string{"dead-jobs.usecase.go": guardedUseCase})

	if err := RunRoutes(routeOptions(dir)); err == nil {
		t.Fatal("an operation with no route at all generated")
	}
	document := readManifest(t, dir)
	if document.Operations[0].Method != "" || document.Operations[0].Path != "" {
		t.Fatalf("a route was invented for a guard no declaration names: %+v", document.Operations[0])
	}
	document.Operations[0].Method = "GET"
	document.Operations[0].Path = "/ops/jobs/dead"
	document.Operations[0].Confirmed = true
	source, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultRoutesFile), append(source, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunRoutes(routeOptions(dir)); err != nil {
		t.Fatalf("the route a person wrote and confirmed in one edit was refused: %v", err)
	}
	written := readManifest(t, dir).Operations[0]
	if written.Source != sourceFromManifest || !written.Confirmed {
		t.Fatalf("operation = %+v", written)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, DefaultRoutesOut)), `path:        "/ops/jobs/dead"`) {
		t.Fatal("the carrier does not name the route the manifest gave it")
	}
}

func TestADeclarationNamingAPermissionNoUseCaseEnforcesIsRefused(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"dead-jobs.usecase.go": guardedUseCase,
		"ops.http-handler.go": strings.Replace(declaringHandler,
			`authhttp.Requires(fiber.MethodGet, "/ops/jobs/dead", PermJobsRead),`,
			`authhttp.Requires(fiber.MethodGet, "/ops/jobs/dead", PermJobsRead),
		authhttp.Requires(fiber.MethodPost, "/ops/jobs/dead/:id/restart", PermJobsRetry),`, 1),
	})

	err := RunRoutes(routeOptions(dir))
	var unenforced *UnenforcedDeclarationError
	if !errors.As(err, &unenforced) {
		t.Fatalf("a declaration with nothing behind it was accepted: %v", err)
	}
	if len(unenforced.Declarations) != 1 || !strings.Contains(unenforced.Declarations[0], "/ops/jobs/dead/:id/restart") {
		t.Fatalf("the refusal names %v, not the route whose permission no use case checks", unenforced.Declarations)
	}
	if _, statErr := os.Stat(filepath.Join(dir, DefaultRoutesOut)); !os.IsNotExist(statErr) {
		t.Fatal("a file was generated for a package whose declarations do not match its guards")
	}
}

func TestAGuardBoundToTheGeneratedOperationIsNotConfirmedAgain(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"dead-jobs.usecase.go": guardedUseCase,
		"ops.http-handler.go":  declaringHandler,
	})
	if err := RunRoutes(routeOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryOperation(t, dir)
	if err := RunRoutes(routeOptions(dir)); err != nil {
		t.Fatalf("a confirmed operation was refused: %v", err)
	}

	bound := strings.Replace(guardedUseCase,
		"access.Require(ctx, PermJobsRead)",
		"access.Require(ctx, OperationDeadJobsUseCaseList.Permissions()...)", 1)
	if err := os.WriteFile(filepath.Join(dir, "dead-jobs.usecase.go"), []byte(bound), 0o644); err != nil {
		t.Fatal(err)
	}
	adopted := `package ops

import "github.com/frostgrove/vv/auth/http/authhttp"

type Handler struct{}

func (this *Handler) Access() []authhttp.Endpoint { return Declarations() }
`
	if err := os.WriteFile(filepath.Join(dir, "ops.http-handler.go"), []byte(adopted), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunRoutes(routeOptions(dir)); err != nil {
		t.Fatalf("an operation the compiler now links to its guard was refused: %v", err)
	}
	operation := readManifest(t, dir).Operations[0]
	if operation.Source != sourceBound || !operation.Confirmed {
		t.Fatalf("operation = %+v", operation)
	}
	if len(operation.Policy) != 1 || operation.Policy[0] != "PermJobsRead" {
		t.Fatalf("the bound operation lost the policy it carries: %v", operation.Policy)
	}
}

func TestTheRouteCheckReportsDriftWithoutWriting(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"dead-jobs.usecase.go": guardedUseCase,
		"ops.http-handler.go":  declaringHandler,
	})
	if err := RunRoutes(routeOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryOperation(t, dir)
	if err := RunRoutes(routeOptions(dir)); err != nil {
		t.Fatalf("generating: %v", err)
	}
	generated := readFile(t, filepath.Join(dir, DefaultRoutesOut))

	options := routeOptions(dir)
	options.Check = true
	if err := RunRoutes(options); err != nil {
		t.Fatalf("the files it had just written were called stale: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "ops.http-handler.go"),
		[]byte(strings.Replace(declaringHandler, "/ops/jobs/dead", "/ops/jobs/terminal", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	err := RunRoutes(options)
	var confirmation *RouteConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("a moved route passed the check: %v", err)
	}
	if readFile(t, filepath.Join(dir, DefaultRoutesOut)) != generated {
		t.Fatal("the check rewrote the file it was asked only to read")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, DefaultRoutesFile)), `"confirmed": true`) {
		t.Fatal("the check rewrote the manifest it was asked only to read")
	}
}

func TestAPackageThatAlreadyDeclaresOperationsKeepsItsOwn(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"dead-jobs.usecase.go": guardedUseCase,
		"ops.http-handler.go":  declaringHandler,
		"authored.go":          "package ops\n\nfunc Declarations() []string { return nil }\n",
	})
	if err := RunRoutes(routeOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryOperation(t, dir)

	err := RunRoutes(routeOptions(dir))
	if err == nil || !strings.Contains(err.Error(), "already declares Declarations") {
		t.Fatalf("the generator was ready to collide with an authored name: %v", err)
	}
	if strings.Contains(readFile(t, filepath.Join(dir, DefaultRoutesOut)), "func Declarations()") {
		t.Fatal("the colliding file was written anyway")
	}
}

func TestOneUseCaseCannotEnforceTwoPoliciesUnderOneName(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"dead-jobs.usecase.go": strings.Replace(guardedUseCase,
			"if _, err := access.Require(ctx, PermJobsRead); err != nil {\n\t\treturn err\n\t}",
			"if _, err := access.Require(ctx, PermJobsRead); err != nil {\n\t\treturn err\n\t}\n\tif _, err := access.Require(ctx, PermJobsRetry); err != nil {\n\t\treturn err\n\t}", 1),
	})

	err := RunRoutes(routeOptions(dir))
	if err == nil || !strings.Contains(err.Error(), "two different policies") {
		t.Fatalf("two guards in one body were collapsed into one operation: %v", err)
	}
}

func TestTheGeneratedCarrierCompilesAsBothTheDeclarationAndTheGuardsArgument(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ops := filepath.Join(dir, "ops")
	authz := filepath.Join(dir, "authz")
	for _, path := range []string{ops, authz} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "go.mod"), "module guarded\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => "+root+"\n")
	write(filepath.Join(authz, "authz.go"), `package authz

import (
	"context"

	"github.com/frostgrove/vv/auth"
)

func Require(_ context.Context, permissions ...auth.Permission) error {
	if len(permissions) == 0 {
		return nil
	}
	return nil
}
`)
	write(filepath.Join(ops, "dead-jobs.usecase.go"), `package ops

import (
	"context"

	"github.com/frostgrove/vv/auth"

	"guarded/authz"
)

const PermJobsRead auth.Permission = "job.read"

type DeadJobsUseCase struct{}

func (this *DeadJobsUseCase) List(ctx context.Context) error {
	return authz.Require(ctx, PermJobsRead)
}
`)
	write(filepath.Join(ops, "ops.http-handler.go"), `package ops

import (
	"net/http"

	"github.com/frostgrove/vv/auth/http/authhttp"
)

type Handler struct{}

func (this *Handler) Access() []authhttp.Endpoint {
	return []authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/ops/jobs/dead", PermJobsRead),
	}
}
`)
	options := &RouteOptions{Dir: ops, GuardPkg: "guarded/authz", GuardFunc: "Require"}
	if err := RunRoutes(options); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryOperation(t, ops)
	if err := RunRoutes(options); err != nil {
		t.Fatalf("generating: %v", err)
	}

	write(filepath.Join(ops, "dead-jobs.usecase.go"), `package ops

import (
	"context"

	"github.com/frostgrove/vv/auth"

	"guarded/authz"
)

const PermJobsRead auth.Permission = "job.read"

type DeadJobsUseCase struct{}

func (this *DeadJobsUseCase) List(ctx context.Context) error {
	return authz.Require(ctx, OperationDeadJobsUseCaseList.Permissions()...)
}
`)
	write(filepath.Join(ops, "ops.http-handler.go"), `package ops

import "github.com/frostgrove/vv/auth/http/authhttp"

type Handler struct{}

func (this *Handler) Access() []authhttp.Endpoint { return Declarations() }
`)
	if err := RunRoutes(options); err != nil {
		t.Fatalf("regenerating over the bound guard: %v", err)
	}

	command := exec.Command("go", "build", "./ops")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	if response, err := command.CombinedOutput(); err != nil {
		t.Fatalf("the package whose guard and declaration both read the generated operation does not compile: %v\n%s\n--- generated ---\n%s",
			err, response, readFile(t, filepath.Join(ops, DefaultRoutesOut)))
	}
}

func TestTheWalkNamesEveryPackageWaitingForConfirmationAndSkipsTheOnesWithNoGuard(t *testing.T) {
	root := t.TempDir()
	ops := filepath.Join(root, "ops")
	billing := filepath.Join(root, "billing")
	unguarded := filepath.Join(root, "reports")
	for _, path := range []string{ops, billing, unguarded} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ops, "dead-jobs.usecase.go"), []byte(guardedUseCase), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(billing, "invoices.usecase.go"),
		[]byte(strings.ReplaceAll(strings.Replace(guardedUseCase, "package ops", "package billing", 1),
			"DeadJobsUseCase", "InvoicesUseCase")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unguarded, "report.go"),
		[]byte("package reports\n\ntype Shape struct{}\n\nfunc (this Shape) Require() bool { return this.Require() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	options := routeOptions(root)
	options.Recursive = true
	err := RunRoutes(options)
	var confirmation *RouteConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("the walk did not stop on unconfirmed operations: %v", err)
	}
	if len(confirmation.Operations) != 2 ||
		confirmation.Operations[0] != "billing.InvoicesUseCase.List" ||
		confirmation.Operations[1] != "ops.DeadJobsUseCase.List" {
		t.Fatalf("the walk names %v, not every package waiting", confirmation.Operations)
	}
	if _, statErr := os.Stat(filepath.Join(unguarded, DefaultRoutesFile)); !os.IsNotExist(statErr) {
		t.Fatal("a package that guards nothing was given a manifest")
	}
}

const qualifiedUseCase = `package ops

import (
	"context"

	"github.com/frostgrove/vv/auth/access"

	perm "example.com/jobs/permissions"
)

type DeadJobsUseCase struct{}

func (this *DeadJobsUseCase) List(ctx context.Context) error {
	if _, err := access.Require(ctx, perm.Read); err != nil {
		return err
	}
	return nil
}
`

const qualifiedHandler = `package ops

import (
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/gofiber/fiber/v3"

	perm "%s"
)

type Handler struct{}

func (this *Handler) Access() []authhttp.Endpoint {
	return []authhttp.Endpoint{
		authhttp.Requires(fiber.MethodGet, "/ops/jobs/dead", perm.Read),
	}
}
`

func TestAnAliasThatNamesTwoPackagesIsRefusedRatherThanReadFromWhicheverFileSortsFirst(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"handler.go": fmt.Sprintf(qualifiedHandler, "example.com/billing/permissions"),
		"usecase.go": qualifiedUseCase,
	})

	err := RunRoutes(routeOptions(dir))
	if err == nil {
		t.Fatal("a package where perm means two things generated a permission the generator cannot have read")
	}
	for _, want := range []string{
		"DeadJobsUseCase.List",
		"perm.Read",
		`"example.com/billing/permissions" in handler.go`,
		`"example.com/jobs/permissions" in usecase.go`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q: %v", want, err)
		}
	}
	for _, name := range []string{DefaultRoutesOut, DefaultRoutesFile} {
		if _, statErr := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(statErr) {
			t.Fatalf("%s was written for a package whose permission could not be resolved", name)
		}
	}
}

func TestAnAliasCollisionNoPolicyReadsLeavesGenerationAlone(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"dead-jobs.usecase.go": guardedUseCase,
		"ops.http-handler.go":  declaringHandler,
		"billing.rows.go":      "package ops\n\nimport rows \"example.com/billing/rows\"\n\ntype InvoiceRow = rows.Row\n",
		"jobs.rows.go":         "package ops\n\nimport rows \"example.com/jobs/rows\"\n\ntype JobRow = rows.Row\n",
	})

	if err := RunRoutes(routeOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryOperation(t, dir)

	if err := RunRoutes(routeOptions(dir)); err != nil {
		t.Fatalf("a collision on an alias no permission is written with stopped generation: %v", err)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, DefaultRoutesOut)), "var OperationDeadJobsUseCaseList = Operation{") {
		t.Fatal("the confirmed operation is missing from the carrier")
	}
}

func TestACarriedPolicyWhoseQualifierNamesTwoPackagesIsRefusedBeforeTheCarrierIsRewritten(t *testing.T) {
	dir := guardedPackage(t, map[string]string{
		"handler.go": fmt.Sprintf(qualifiedHandler, "example.com/jobs/permissions"),
		"usecase.go": qualifiedUseCase,
	})
	if err := RunRoutes(routeOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryOperation(t, dir)
	if err := RunRoutes(routeOptions(dir)); err != nil {
		t.Fatalf("a confirmed operation was refused: %v", err)
	}
	generated := readFile(t, filepath.Join(dir, DefaultRoutesOut))

	bound := strings.Replace(qualifiedUseCase,
		"access.Require(ctx, perm.Read)",
		"access.Require(ctx, OperationDeadJobsUseCaseList.Permissions()...)", 1)
	if err := os.WriteFile(filepath.Join(dir, "usecase.go"), []byte(bound), 0o644); err != nil {
		t.Fatal(err)
	}
	adopted := `package ops

import (
	"github.com/frostgrove/vv/auth/http/authhttp"

	perm "example.com/billing/permissions"
)

type Handler struct{}

type Permissions = perm.Set

func (this *Handler) Access() []authhttp.Endpoint { return Declarations() }
`
	if err := os.WriteFile(filepath.Join(dir, "handler.go"), []byte(adopted), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RunRoutes(routeOptions(dir))
	if err == nil {
		t.Fatal("the policy the manifest carries was rendered under an alias that names two packages")
	}
	for _, want := range []string{
		"perm.Read",
		`"example.com/billing/permissions" in handler.go`,
		`"example.com/jobs/permissions" in usecase.go`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q: %v", want, err)
		}
	}
	if readFile(t, filepath.Join(dir, DefaultRoutesOut)) != generated {
		t.Fatal("the carrier was rewritten by a run that could not tell which package the permission comes from")
	}
	if !strings.Contains(generated, `perm "example.com/jobs/permissions"`) {
		t.Fatalf("the carrier this test guards never named the guard's own package:\n%s", generated)
	}
}
