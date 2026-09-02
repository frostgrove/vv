package health_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/health"
)

func failing(reason string) health.Probe {
	return health.ProbeFunc(func(context.Context) error { return errors.New(reason) })
}

func passing() health.Probe {
	return health.ProbeFunc(func(context.Context) error { return nil })
}

func registry(t *testing.T, spec health.Spec) *health.Registry {
	t.Helper()
	built, err := health.New(spec)
	if err != nil {
		t.Fatalf("a well-formed registry was refused: %v", err)
	}
	return built
}

func detailOf(t *testing.T, report health.Detail, name string) health.CheckDetail {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("the operator detail says nothing about %q", name)
	return health.CheckDetail{}
}

func TestARequiredDependencyThatFailsTakesTheReplicaOutOfRotation(t *testing.T) {
	registry := registry(t, health.Spec{Contributions: []health.Contribution{
		{Name: "postgres.primary", Code: "database", Importance: health.Required, Probe: failing("connection refused")},
		{Name: "minio.documents", Code: "storage", Importance: health.Required, Probe: passing()},
	}})

	report := registry.Ready(context.Background())

	if report.Status != health.StatusDown {
		t.Fatalf("a replica whose required dependency is down reported %q", report.Status)
	}
	if len(report.Codes) != 1 || report.Codes[0] != "database" {
		t.Fatalf("the public report does not name the failing dependency's code: %v", report.Codes)
	}
}

func TestADegradingDependencyKeepsTheReplicaServingAndSaysSo(t *testing.T) {
	registry := registry(t, health.Spec{Contributions: []health.Contribution{
		{Name: "postgres.primary", Code: "database", Importance: health.Required, Probe: passing()},
		{Name: "redis.sessions", Code: "cache", Importance: health.Degrading, Probe: failing("i/o timeout")},
	}})

	report := registry.Ready(context.Background())

	if report.Status != health.StatusDegraded {
		t.Fatalf("a replica that can still serve reported %q instead of degraded", report.Status)
	}
	if len(report.Codes) != 1 || report.Codes[0] != "cache" {
		t.Fatalf("the degraded report does not say which half is missing: %v", report.Codes)
	}
}

func TestAnInformationalFailureChangesNothingAnyoneRoutesOn(t *testing.T) {
	registry := registry(t, health.Spec{Contributions: []health.Contribution{
		{Name: "smtp.relay", Code: "mail", Importance: health.Informational, Probe: failing("no route to host")},
	}})

	report := registry.Ready(context.Background())

	if report.Status != health.StatusReady {
		t.Fatalf("an informational failure moved the status to %q", report.Status)
	}
	if len(report.Codes) != 0 {
		t.Fatalf("an informational failure reached the public report: %v", report.Codes)
	}
	if state := detailOf(t, registry.Inspect(context.Background()), "smtp.relay").State; state != health.StateFailing {
		t.Fatalf("the operator projection hides the informational failure: %q", state)
	}
}

func TestTheStatusMovesWithoutNamingACheckThatPublishesNoCode(t *testing.T) {
	registry := registry(t, health.Spec{Contributions: []health.Contribution{
		{Name: "vault.unseal-key", Importance: health.Required, Probe: failing("sealed")},
	}})

	report := registry.Ready(context.Background())

	if report.Status != health.StatusDown {
		t.Fatalf("a failing required check with no public code left the status at %q", report.Status)
	}
	if len(report.Codes) != 0 {
		t.Fatalf("a check that published no code was named to the public anyway: %v", report.Codes)
	}
	detail := detailOf(t, registry.Inspect(context.Background()), "vault.unseal-key")
	if !strings.Contains(detail.Message, "sealed") {
		t.Fatalf("the operator projection lost the reason: %q", detail.Message)
	}
}

func TestLivenessAsksNoDependencyAnything(t *testing.T) {
	var asked atomic.Int64
	registry := registry(t, health.Spec{Contributions: []health.Contribution{
		{Name: "postgres.primary", Importance: health.Required, Probe: health.ProbeFunc(func(context.Context) error {
			asked.Add(1)
			return errors.New("connection refused")
		})},
	}})

	report := registry.Live()

	if report.Status != health.StatusLive {
		t.Fatalf("a running process reported liveness %q", report.Status)
	}
	if asked.Load() != 0 {
		t.Fatal("liveness pinged a dependency, so a slow database would restart every replica of the service")
	}
}

func TestADisabledContributionIsNeverAsked(t *testing.T) {
	var asked atomic.Int64
	registry := registry(t, health.Spec{Contributions: []health.Contribution{
		{Name: "clamav", Importance: health.Disabled, Probe: health.ProbeFunc(func(context.Context) error {
			asked.Add(1)
			return errors.New("not deployed here")
		})},
	}})

	if status := registry.Ready(context.Background()).Status; status != health.StatusReady {
		t.Fatalf("a disabled check moved the status to %q", status)
	}
	if asked.Load() != 0 {
		t.Fatal("a disabled check was asked anyway")
	}
	if state := detailOf(t, registry.Inspect(context.Background()), "clamav").State; state != health.StateDisabled {
		t.Fatalf("the operator projection reports a disabled check as %q", state)
	}
}

func TestAProbeThatPanicsFailsItsOwnCheckAndNothingElse(t *testing.T) {
	registry := registry(t, health.Spec{Contributions: []health.Contribution{
		{Name: "wps.converter", Code: "converter", Importance: health.Degrading, Probe: health.ProbeFunc(func(context.Context) error {
			panic("nil client")
		})},
		{Name: "postgres.primary", Code: "database", Importance: health.Required, Probe: passing()},
	}})

	report := registry.Ready(context.Background())

	if report.Status != health.StatusDegraded {
		t.Fatalf("a panicking probe produced status %q", report.Status)
	}
	detail := detailOf(t, registry.Inspect(context.Background()), "wps.converter")
	if !strings.Contains(detail.Message, "panicked") {
		t.Fatalf("the operator projection does not say the probe panicked: %q", detail.Message)
	}
}

func TestAProbeThatOverrunsItsTimeoutFailsInsteadOfHangingTheEndpoint(t *testing.T) {
	registry := registry(t, health.Spec{Contributions: []health.Contribution{
		{
			Name:       "postgres.primary",
			Code:       "database",
			Importance: health.Required,
			Timeout:    20 * time.Millisecond,
			Probe: health.ProbeFunc(func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}),
		},
	}})

	started := time.Now()
	report := registry.Ready(context.Background())

	if report.Status != health.StatusDown {
		t.Fatalf("a dependency that never answered reported %q", report.Status)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("the readiness call waited %s on a probe with a 20ms budget", elapsed)
	}
}

func TestOneSharedPassAnswersEveryConcurrentReadinessCall(t *testing.T) {
	var asked atomic.Int64
	registry := registry(t, health.Spec{
		Freshness: time.Nanosecond,
		Contributions: []health.Contribution{
			{Name: "postgres.primary", Importance: health.Required, Probe: health.ProbeFunc(func(context.Context) error {
				asked.Add(1)
				time.Sleep(200 * time.Millisecond)
				return nil
			})},
		},
	})

	var arrived, finished sync.WaitGroup
	const callers = 8
	arrived.Add(callers)
	finished.Add(callers)
	for range callers {
		go func() {
			defer finished.Done()
			arrived.Done()
			arrived.Wait()
			registry.Ready(context.Background())
		}()
	}
	finished.Wait()

	if asked.Load() != 1 {
		t.Fatalf("%d concurrent scrapes produced %d passes against the dependency", callers, asked.Load())
	}
}

func TestAPassIsReusedForTheFreshnessWindowAndNotBeyondIt(t *testing.T) {
	var asked atomic.Int64
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	registry := registry(t, health.Spec{
		Freshness: time.Second,
		Now:       func() time.Time { return now },
		Contributions: []health.Contribution{
			{Name: "postgres.primary", Importance: health.Required, Probe: health.ProbeFunc(func(context.Context) error {
				asked.Add(1)
				return nil
			})},
		},
	})

	registry.Ready(context.Background())
	registry.Ready(context.Background())
	if asked.Load() != 1 {
		t.Fatalf("two scrapes inside the freshness window cost %d passes", asked.Load())
	}

	now = now.Add(2 * time.Second)
	registry.Ready(context.Background())
	if asked.Load() != 2 {
		t.Fatalf("a scrape after the freshness window reused a stale pass: %d passes", asked.Load())
	}
}

func TestAScraperThatGivesUpDoesNotFailThePassOthersAreWaitingOn(t *testing.T) {
	var seen atomic.Value
	registry := registry(t, health.Spec{Contributions: []health.Contribution{
		{Name: "postgres.primary", Importance: health.Required, Probe: health.ProbeFunc(func(ctx context.Context) error {
			seen.Store(ctx.Err() != nil)
			return nil
		})},
	}})

	abandoned, cancel := context.WithCancel(context.Background())
	cancel()
	registry.Ready(abandoned)

	if cancelled, _ := seen.Load().(bool); cancelled {
		t.Fatal("the shared pass inherited a caller's cancellation, so one abandoned scrape marks the replica unhealthy for everyone")
	}
}

func TestARegistryReportsEveryRegistrationProblemAtOnce(t *testing.T) {
	_, err := health.New(health.Spec{Contributions: []health.Contribution{
		{Name: "postgres.primary", Importance: health.Required, Probe: passing()},
		{Name: "postgres.primary", Importance: health.Required, Probe: passing()},
		{Name: "redis.sessions", Importance: "important", Probe: passing()},
		{Name: "minio.documents", Importance: health.Required},
		{Importance: health.Required, Probe: passing()},
	}})

	if err == nil {
		t.Fatal("a registry with a duplicate name, an unknown importance and a missing probe was accepted")
	}
	if !errors.Is(err, health.ErrRegistration) {
		t.Fatalf("the refusal is not the registration error: %v", err)
	}
	var refusal *health.RegistrationError
	if !errors.As(err, &refusal) {
		t.Fatalf("the refusal does not carry its problems: %v", err)
	}
	if len(refusal.Problems()) != 4 {
		t.Fatalf("the four independent problems were not reported together: %v", refusal.Problems())
	}
}

func TestAMagicRegistryIsTheSameRegistryWithDefaults(t *testing.T) {
	contribution := health.Contribution{Name: "postgres.primary", Importance: health.Required, Probe: passing()}

	magic, err := health.Auto(contribution)
	if err != nil {
		t.Fatalf("Auto refused a well-formed contribution: %v", err)
	}
	explicit := registry(t, health.Spec{Contributions: []health.Contribution{contribution}})

	if magic.Ready(context.Background()).Status != explicit.Ready(context.Background()).Status {
		t.Fatal("Auto and the explicit constructor disagree, so the short form is a second implementation")
	}
	if timeout := magic.Contributions()[0].Timeout; timeout != health.DefaultTimeout {
		t.Fatalf("Auto left the check without the default budget: %s", timeout)
	}
}

func TestAnOperatorMessageIsBounded(t *testing.T) {
	registry := registry(t, health.Spec{Contributions: []health.Contribution{
		{Name: "postgres.primary", Importance: health.Required, Probe: failing(strings.Repeat("x", 4096))},
	}})

	detail := detailOf(t, registry.Inspect(context.Background()), "postgres.primary")

	if len(detail.Message) > health.MaxMessageBytes {
		t.Fatalf("a driver error of any length reaches the health page whole: %d bytes", len(detail.Message))
	}
}
