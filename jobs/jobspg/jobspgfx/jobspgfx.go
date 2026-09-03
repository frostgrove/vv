package jobspgfx

import (
	"database/sql"
	"fmt"
	"io"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobspg"
	"github.com/frostgrove/vv/runtime/runtimefx"
)

type Settings struct {
	Namespace        jobs.Namespace
	Schema           string
	Backend          jobs.BackendID
	Entropy          io.Reader
	SchemaManagement jobspg.SchemaManagement
	Housekeeping     HousekeepingSettings
}

type HousekeepingSettings struct {
	Disabled     bool
	Interval     time.Duration
	SweepTimeout time.Duration
	BatchSize    int
	MaxBatches   int
}

// Module wires the driver every deployment needs, and retention housekeeping
// only where it was not switched off. Housekeeping is a background activity, so
// what it wires is a contribution to the supervisor's runner group rather than a
// goroutine this module starts ([[D-092]]).
func Module(settings Settings) fx.Option {
	constructor := func(database *sql.DB, source crud.Source, catalog jobs.Catalog) (*jobspg.Driver, error) {
		return New(settings, database, source, catalog)
	}
	options := []fx.Option{
		fx.Provide(
			fx.Annotate(
				constructor,
				fx.As(new(jobsfx.Backend)),
				fx.As(new(jobs.Admin)),
				fx.As(new(jobs.Controller)),
				fx.As(new(jobs.Operations)),
				fx.As(new(jobs.FencedTransactions)),
				fx.As(new(jobs.RetentionSweeper)),
				fx.As(fx.Self()),
			),
			func() (housekeepingConfig, error) {
				return normalizeHousekeeping(settings.Housekeeping)
			},
		),
	}
	if !settings.Housekeeping.Disabled {
		options = append(options,
			fx.Provide(runtimefx.AsRunner(newRetentionRunner)),
			fx.Invoke(bindRetentionSupervision),
		)
	}
	return fx.Module("vv.jobspg", options...)
}

func New(settings Settings, database *sql.DB, source crud.Source, catalog jobs.Catalog) (*jobspg.Driver, error) {
	driver, err := jobspg.New(jobspg.Spec{
		DB:               database,
		Source:           source,
		Namespace:        settings.Namespace,
		Catalog:          catalog,
		Schema:           settings.Schema,
		Backend:          settings.Backend,
		Entropy:          settings.Entropy,
		SchemaManagement: settings.SchemaManagement,
	})
	if err != nil {
		return nil, fmt.Errorf("jobspgfx: configure PostgreSQL jobs backend: %w", err)
	}
	return driver, nil
}
