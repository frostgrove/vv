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

type Seeder struct {
	Name string

	Order int

	Envs []string

	Run func(ctx context.Context) error
}

func (this Seeder) Common() bool { return len(this.Envs) == 0 }

func (this Seeder) runsIn(environment string) bool {
	return len(this.Envs) == 0 || slices.Contains(this.Envs, environment)
}

var ErrSeeder = errors.New("app: the seed set is not runnable")

type Seeding struct {
	Env string

	Known []string

	Logger *slog.Logger
}

type Runner struct {
	seeders []Seeder
	env     string
	logger  *slog.Logger
}

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

func (this *Runner) Env() string { return this.env }

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
