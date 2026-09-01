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

func Module(settings Settings) fx.Option {
	constructor := func(database *sql.DB, source crud.Source, catalog jobs.Catalog) (*jobspg.Driver, error) {
		return New(settings, database, source, catalog)
	}
	return fx.Module("vv.jobspg",
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
		fx.Invoke(bindRetentionLifecycle),
	)
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
