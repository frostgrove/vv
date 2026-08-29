package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/frostgrove/vv/port"
)

// A Seeder is one unit of seed data.
//
// Every one of them must be runnable twice. That is not politeness: a seed
// command is re-run after every migration by whoever is not sure whether it was
// run already, and one that inserts a second row the second time is a command
// nobody dares use — which means the data it writes ends up being typed in by
// hand instead.
//
// What a seeder writes is data the product decided: that this deployment has a
// lawyer role, that somebody who signs up is a user. It is not what the code
// declares — which permissions exist follows from which modules are compiled in,
// and that is folded in at start-up, before any of this runs.
type Seeder struct {
	// Name is what the log and a failure name it by. It is also what makes two
	// contributors registering the same seeder a refusal rather than a double
	// run.
	Name string
	// Order decides what runs first, the same way [Ordered.Order] does: the
	// roles have to exist before anything is granted one, and that is not
	// something to leave to registration order.
	Order int
	// Envs are the environments this seeder runs in. Empty means every one of
	// them, which is the common half; a non-empty list is an overlay.
	//
	// Empty rather than a sentinel listing them all, because the common case
	// should be the one you cannot get wrong: a seeder that forgot to name its
	// environments runs everywhere, which is visible, rather than nowhere, which
	// is not.
	Envs []string
	// Run does the work. It owns its own transaction: one transaction around the
	// whole command would hold locks across every contributor for as long as the
	// slowest seeder, and a partial run is not a problem here because re-running
	// finishes it.
	Run func(ctx context.Context) error
}

// Common reports whether this seeder runs everywhere. It is what a log line
// distinguishes, so somebody reading a production seed can see at a glance which
// rows were the product's and which were the environment's.
func (this Seeder) Common() bool { return len(this.Envs) == 0 }

// runsIn reports whether this seeder belongs in the given environment.
func (this Seeder) runsIn(environment string) bool {
	return len(this.Envs) == 0 || slices.Contains(this.Envs, environment)
}

// ErrSeeder reports a seeder that cannot be run: one with no name, one with
// nothing to run, two with the same name, or one naming an environment the
// deployment does not have.
var ErrSeeder = errors.New("app: the seed set is not runnable")

// A Seeding is what a [Runner] is built for.
type Seeding struct {
	// Env is the environment being seeded.
	Env string
	// Known is every environment this deployment has, and it is optional.
	//
	// When it is set, a seeder naming an environment outside it is refused
	// rather than silently never running. That failure mode is the reason to
	// pass it: a typo in an environment name is invisible — the seeder is simply
	// skipped, in every environment, and the rows it was meant to write are
	// missing somewhere nobody is looking.
	Known []string
	// Logger is where the run is reported. Nil means the one on the context,
	// which is [port.Logger]'s answer and ultimately slog.Default ([[D-062]]).
	Logger *slog.Logger
}

// A Runner is the whole of a seed command's orchestration: pick what this
// environment runs, put it in a defined order, and run it.
//
// It decides nothing about the data. Every rule about what a role holds or which
// one a sign-up grants lives in the module that contributed the seeder, or below
// that in the library — this only sequences them, which is what keeps "the seed
// command" from slowly becoming a second place the security model is written.
type Runner struct {
	seeders []Seeder
	env     string
	logger  *slog.Logger
}

// NewRunner sorts and validates the set.
//
// Every failure it reports is a wiring mistake, and every one is worth failing
// the command over rather than working around: a seeder with no name cannot be
// reported when it breaks, and two with the same name means somebody registered
// one twice and half the run is doing the other's work.
func NewRunner(seeders []Seeder, spec Seeding) (*Runner, error) {
	ordered := slices.Clone(seeders)
	slices.SortFunc(ordered, func(a, b Seeder) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.Name, b.Name)
	})

	seen := make(map[string]struct{}, len(ordered))
	for _, seeder := range ordered {
		switch {
		case seeder.Name == "":
			return nil, fmt.Errorf("%w: a seeder was registered with no name", ErrSeeder)
		case seeder.Run == nil:
			return nil, fmt.Errorf("%w: the seeder %q has nothing to run", ErrSeeder, seeder.Name)
		}
		if _, duplicate := seen[seeder.Name]; duplicate {
			return nil, fmt.Errorf("%w: two seeders are named %q", ErrSeeder, seeder.Name)
		}
		seen[seeder.Name] = struct{}{}

		if len(spec.Known) == 0 {
			continue
		}
		for _, environment := range seeder.Envs {
			if !slices.Contains(spec.Known, environment) {
				return nil, fmt.Errorf("%w: the seeder %q names the environment %q, which this deployment does not have",
					ErrSeeder, seeder.Name, environment)
			}
		}
	}

	return &Runner{seeders: ordered, env: spec.Env, logger: spec.Logger}, nil
}

// Env answers which environment this runner seeds, for a caller that wants to
// say so before it starts.
func (this *Runner) Env() string { return this.env }

// Run seeds this environment.
//
// It stops at the first failure rather than carrying on. A later seeder usually
// depends on an earlier one having run — an account cannot be given a role that
// was not created — so continuing produces a second, misleading failure and
// buries the real one.
//
// Nothing here retries or rolls back. Re-running the command is the recovery,
// which is the whole reason every seeder has to be idempotent.
func (this *Runner) Run(ctx context.Context) error {
	log := this.log(ctx)
	log.InfoContext(ctx, "seeding",
		slog.String("env", this.env), slog.Int("registered", len(this.seeders)))

	ran := 0
	for _, seeder := range this.seeders {
		if !seeder.runsIn(this.env) {
			log.DebugContext(ctx, "seeder skipped",
				slog.String("seeder", seeder.Name), slog.String("env", this.env))
			continue
		}
		if err := seeder.Run(ctx); err != nil {
			return fmt.Errorf("seeding %q: %w", seeder.Name, err)
		}
		ran++
		log.InfoContext(ctx, "seeded",
			slog.String("seeder", seeder.Name), slog.Bool("common", seeder.Common()))
	}

	log.InfoContext(ctx, "seeding complete",
		slog.String("env", this.env), slog.Int("ran", ran))
	return nil
}

func (this *Runner) log(ctx context.Context) *slog.Logger {
	if this.logger != nil {
		return this.logger
	}
	return port.Logger(ctx)
}
