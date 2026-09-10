package auditflow_test

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/app/module"
	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/health"
	"github.com/frostgrove/vv/test/auditflow/auditdeployment"
	"github.com/frostgrove/vv/test/auditflow/auditserving"
)

func TestGeneratedAuditProfilesSeparateServingRuntimeFromDeploymentAuthority(t *testing.T) {
	runtime := newApplicationJobAuditRuntime(t)
	assertGeneratedAuditDefinition(t, auditserving.VVModule, "audit", 3, 1)
	assertGeneratedAuditDefinition(t, auditdeployment.VVModule, "audit-deployment", 1, 0)

	servingCatalog := module.MustCatalog(auditserving.VVModule)
	if err := servingCatalog.Check(module.Serving); err != nil {
		t.Fatal(err)
	}
	inputs := auditserving.Inputs{
		Recorder: runtime.recorder,
		History:  runtime.history,
		Attempts: runtime.attempts,
		Store:    runtime.store,
		Active:   runtime.catalogs.Active(),
	}
	var recorder *audit.Recorder
	var history *audit.History
	var attempts *audit.Attempts
	var selected health.Contribution
	serving := fx.New(
		fx.NopLogger,
		fx.Supply(inputs),
		fx.Provide(auditserving.VVModule.Active(module.Serving)...),
		fx.Populate(&recorder, &history, &attempts, &selected),
	)
	if err := serving.Err(); err != nil {
		t.Fatalf("build generated audit serving graph: %v", err)
	}
	startAndStopAuditGraph(t, serving)
	if recorder != runtime.recorder || history != runtime.history || attempts != runtime.attempts || history.Profile() != audit.PublicOnePageDevelopmentAlpha {
		t.Fatal("generated serving graph did not expose the selected Recorder, History and Attempts")
	}
	if selected.Name != "audit" || selected.Code != "audit_unready" || selected.Importance != health.Required || selected.Probe == nil {
		t.Fatalf("generated serving health = %+v", selected)
	}
	if err := selected.Probe.Check(t.Context()); err != nil {
		t.Fatalf("generated serving health check: %v", err)
	}
	withoutAttempts := inputs
	withoutAttempts.Attempts = nil
	if err := auditserving.NewHealth(withoutAttempts).Probe.Check(t.Context()); err == nil {
		t.Fatal("generated serving health accepted a missing Attempts runtime")
	}

	var forbidden audit.CatalogAdmin
	unsafeServing := fx.New(
		fx.NopLogger,
		fx.Supply(inputs),
		fx.Provide(auditserving.VVModule.Active(module.Serving)...),
		fx.Populate(&forbidden),
	)
	if unsafeServing.Err() == nil || forbidden != nil {
		t.Fatal("generated serving graph exposed catalog or schema lifecycle authority")
	}

	deploymentCatalog := module.MustCatalog(auditdeployment.VVModule)
	if err := deploymentCatalog.Check(module.Base); err != nil {
		t.Fatal(err)
	}
	var admin audit.CatalogAdmin
	deployment := fx.New(
		fx.NopLogger,
		fx.Supply(auditdeployment.Inputs{Admin: runtime.deployment}),
		fx.Provide(auditdeployment.VVModule.Active(module.Base)...),
		fx.Populate(&admin),
	)
	if err := deployment.Err(); err != nil {
		t.Fatalf("build generated audit deployment graph: %v", err)
	}
	startAndStopAuditGraph(t, deployment)
	if admin == nil {
		t.Fatal("generated deployment graph did not expose CatalogAdmin")
	}
	mutations, err := admin.CatalogMutations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(mutations.Mutations()) != 2 {
		t.Fatalf("deployment catalog mutation count = %d, want install and activation", len(mutations.Mutations()))
	}
}

func TestGeneratedAuditProfileArtifactsAreCurrent(t *testing.T) {
	for _, fixture := range []struct {
		dir   string
		name  string
		order int
	}{
		{dir: "auditserving", name: "audit", order: 400},
		{dir: "auditdeployment", name: "audit-deployment", order: 410},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			command := exec.Command("go", "run", "github.com/frostgrove/vv/cmd/vv", "generate", "module",
				"-dir", fixture.dir, "-name", fixture.name, "-order", strconv.Itoa(fixture.order), "-check")
			command.Dir = "."
			command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=mod")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("generated %s module drifted: %v\n%s", fixture.name, err, output)
			}
		})
	}
}

func assertGeneratedAuditDefinition(t *testing.T, definition module.Definition, name string, provides, checks int) {
	t.Helper()
	if definition.Name() != name {
		t.Fatalf("generated module name = %q, want %q", definition.Name(), name)
	}
	counts := map[module.Kind]int{}
	for _, contribution := range definition.Contributions() {
		counts[contribution.Kind]++
	}
	if counts[module.ProvideKind] != provides || counts[module.CheckKind] != checks {
		t.Fatalf("generated %s contributions = %v", name, counts)
	}
	if counts[module.RouteKind] != 0 || counts[module.WorkerKind] != 0 || counts[module.SeederKind] != 0 {
		t.Fatalf("generated %s carries deployment work = %v", name, counts)
	}
}

func startAndStopAuditGraph(t *testing.T, application *fx.App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := application.Start(ctx); err != nil {
		t.Fatalf("start generated graph: %v", err)
	}
	if err := application.Stop(ctx); err != nil {
		t.Fatalf("stop generated graph: %v", err)
	}
}
