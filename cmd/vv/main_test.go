package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostgrove/vv/internal/codegen"
)

func TestCacheGenerateSubcommandHasDedicatedFlags(t *testing.T) {
	var output bytes.Buffer
	err := run([]string{"generate", "cache", "-help"}, &output, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("cache help error = %v", err)
	}
	help := output.String()
	if !strings.Contains(help, "-manifest") || !strings.Contains(help, "-check") || strings.Contains(help, "-adapter") {
		t.Fatalf("cache help dispatched to the wrong command:\n%s", help)
	}
}

func TestJobsGenerateSubcommandDoesNotExist(t *testing.T) {
	var output bytes.Buffer
	err := run([]string{"generate", "jobs"}, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("jobs generator error = %v", err)
	}
}

func TestLegacyGenerateKeepsModelFlags(t *testing.T) {
	var output bytes.Buffer
	err := run([]string{"generate", "-help"}, &output, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("model help error = %v", err)
	}
	help := output.String()
	if !strings.Contains(help, "-recursive") || !strings.Contains(help, "-adapter") || strings.Contains(help, "-manifest") {
		t.Fatalf("legacy generate flags changed:\n%s", help)
	}
}

func TestTheModelGeneratorTakesTheSameReadOnlyCheckAsCache(t *testing.T) {
	dir := t.TempDir()
	model := "package m\n\ntype Invoice struct {\n\tID     int64  `db:\"id,pk,auto\"`\n\tNumber string `db:\"number\"`\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := run([]string{"generate", "-dir", dir, "-recursive=false", "-check"}, &output, &output)
	var drift *codegen.DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("a package that had never been generated passed the check: %v (%s)", err, output.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "vv_gen.go")); !os.IsNotExist(err) {
		t.Fatal("the check wrote the file it was asked only to read")
	}

	if err := run([]string{"generate", "-dir", dir, "-recursive=false"}, &output, &output); err != nil {
		t.Fatalf("generating: %v", err)
	}
	if err := run([]string{"generate", "-dir", dir, "-recursive=false", "-check"}, &output, &output); err != nil {
		t.Fatalf("the file it had just written was called stale: %v", err)
	}
}

func TestTheRoutesSubcommandRefusesAnInferredPairNobodyConfirmed(t *testing.T) {
	dir := t.TempDir()
	useCase := "package ops\n\nimport (\n\t\"context\"\n\n\t\"github.com/frostgrove/vv/auth\"\n\t\"github.com/frostgrove/vv/auth/access\"\n)\n\n" +
		"const PermJobsRead auth.Permission = \"job.read\"\n\ntype DeadJobsUseCase struct{}\n\n" +
		"func (this *DeadJobsUseCase) List(ctx context.Context) error {\n\t_, err := access.Require(ctx, PermJobsRead)\n\treturn err\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "dead-jobs.usecase.go"), []byte(useCase), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := run([]string{"generate", "routes", "-dir", dir, "-recursive=false"}, &output, &output)
	var confirmation *codegen.RouteConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("routes error = %v (%s)", err, output.String())
	}
	if len(confirmation.Operations) != 1 || confirmation.Operations[0] != "DeadJobsUseCase.List" {
		t.Fatalf("the refusal names %v", confirmation.Operations)
	}
}

func TestTheRoutesSubcommandHasItsOwnFlags(t *testing.T) {
	var output bytes.Buffer
	err := run([]string{"generate", "routes", "-help"}, &output, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("routes help error = %v", err)
	}
	help := output.String()
	if !strings.Contains(help, "-guard") || !strings.Contains(help, "-manifest") || strings.Contains(help, "-adapter") {
		t.Fatalf("routes help dispatched to the wrong command:\n%s", help)
	}
}

func TestTheModuleSubcommandRefusesAContributionNobodyConfirmed(t *testing.T) {
	dir := t.TempDir()
	source := "package ops\n\ntype Repo struct{}\n\nfunc NewRepo() *Repo { return &Repo{} }\n"
	if err := os.WriteFile(filepath.Join(dir, "repo.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := run([]string{"generate", "module", "-dir", dir, "-import", "example.test/ops"}, &output, &output)
	var confirmation *codegen.ModuleConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("module error = %v (%s)", err, output.String())
	}
	if len(confirmation.Contributions) != 1 || confirmation.Contributions[0] != "NewRepo" {
		t.Fatalf("the refusal names %v", confirmation.Contributions)
	}
}

func TestTheModuleSubcommandHasItsOwnFlags(t *testing.T) {
	var output bytes.Buffer
	err := run([]string{"generate", "module", "-help"}, &output, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("module help error = %v", err)
	}
	help := output.String()
	if !strings.Contains(help, "-order") || !strings.Contains(help, "-check-type") ||
		strings.Contains(help, "-guard") || strings.Contains(help, "-adapter") {
		t.Fatalf("module help dispatched to the wrong command:\n%s", help)
	}
}
