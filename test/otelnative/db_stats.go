package otelnative

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/XSAM/otelsql"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	ErrInvalidDatabase           = errors.New("otelnative: database is required")
	ErrInvalidPGXPool            = errors.New("otelnative: pgx pool is required")
	ErrInvalidMetricRegistration = errors.New("otelnative: metric callback registration is required")
	ErrMetricCallback            = errors.New("otelnative: metric callback failed")
	ErrMetricRegistrationCleanup = errors.New("otelnative: metric callback cleanup failed")
)

const (
	pgxPoolAcquiredMetric         = "app.db.pool.connections.acquired"
	pgxPoolIdleMetric             = "app.db.pool.connections.idle"
	pgxPoolMaximumMetric          = "app.db.pool.connections.max"
	pgxPoolAcquireWaitMetric      = "app.db.pool.acquire.waits"
	pgxPoolAcquireTimeMetric      = "app.db.pool.acquire.wait.duration"
	pgxPoolAcquiredDescription    = "Current acquired connections in the pgx pool."
	pgxPoolIdleDescription        = "Current idle connections in the pgx pool."
	pgxPoolMaximumDescription     = "Configured maximum connections in the pgx pool."
	pgxPoolAcquireWaitDescription = "Acquire attempts that waited for a pgx pool connection."
	pgxPoolAcquireTimeDescription = "Total time spent waiting for a pgx pool connection."
)

type PGXPoolStatsSource interface {
	Stat() *pgxpool.Stat
}

type MetricRegistration struct {
	metric.Registration
	once sync.Once
	err  error
}

func (r *MetricRegistration) Unregister() error {
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		if !nilInterface(r.Registration) {
			r.err = r.Registration.Unregister()
		}
	})
	return r.err
}

func RegisterPGXPoolStats(providers Providers, pool PGXPoolStatsSource, poolName DatabasePoolName) (*MetricRegistration, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	if nilInterface(pool) {
		return nil, ErrInvalidPGXPool
	}
	if !validDatabasePoolName(poolName.value) {
		return nil, ErrInvalidDatabasePoolName
	}
	meter := providers.Meter.Meter(
		pgxPoolScopeName,
		metric.WithInstrumentationVersion(pgxPoolVersion),
	)
	acquired, err := meter.Int64ObservableGauge(pgxPoolAcquiredMetric, metric.WithDescription(pgxPoolAcquiredDescription), metric.WithUnit("{connection}"))
	if err != nil {
		return nil, err
	}
	idle, err := meter.Int64ObservableGauge(pgxPoolIdleMetric, metric.WithDescription(pgxPoolIdleDescription), metric.WithUnit("{connection}"))
	if err != nil {
		return nil, err
	}
	maximum, err := meter.Int64ObservableGauge(pgxPoolMaximumMetric, metric.WithDescription(pgxPoolMaximumDescription), metric.WithUnit("{connection}"))
	if err != nil {
		return nil, err
	}
	waits, err := meter.Int64ObservableCounter(pgxPoolAcquireWaitMetric, metric.WithDescription(pgxPoolAcquireWaitDescription), metric.WithUnit("{wait}"))
	if err != nil {
		return nil, err
	}
	waitDuration, err := meter.Float64ObservableCounter(pgxPoolAcquireTimeMetric, metric.WithDescription(pgxPoolAcquireTimeDescription), metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	observe := metric.WithAttributeSet(attribute.NewSet(attribute.String(databasePoolKey, poolName.value)))
	registration, err := meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		stats := pool.Stat()
		if stats == nil {
			return nil
		}
		observer.ObserveInt64(acquired, int64(stats.AcquiredConns()), observe)
		observer.ObserveInt64(idle, int64(stats.IdleConns()), observe)
		observer.ObserveInt64(maximum, int64(stats.MaxConns()), observe)
		observer.ObserveInt64(waits, stats.EmptyAcquireCount(), observe)
		observer.ObserveFloat64(waitDuration, stats.EmptyAcquireWaitTime().Seconds(), observe)
		return nil
	}, acquired, idle, maximum, waits, waitDuration)
	return guardMetricRegistration(registration, err)
}

func RegisterSQLDBStats(providers Providers, database *sql.DB, poolName DatabasePoolName) (*MetricRegistration, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	if database == nil {
		return nil, ErrInvalidDatabase
	}
	if !validDatabasePoolName(poolName.value) {
		return nil, ErrInvalidDatabasePoolName
	}
	registration, err := otelsql.RegisterDBStatsMetrics(
		database,
		otelsql.WithTracerProvider(providers.Tracer),
		otelsql.WithMeterProvider(&sqlStatsMeterProvider{MeterProvider: providers.Meter}),
		otelsql.WithAttributes(attribute.String(databasePoolKey, poolName.value)),
	)
	return guardMetricRegistration(registration, err)
}

type sqlStatsMeterProvider struct {
	metric.MeterProvider
}

func (provider *sqlStatsMeterProvider) Meter(name string, options ...metric.MeterOption) metric.Meter {
	meter := provider.MeterProvider.Meter(name, options...)
	if name != sqlScopeName || nilInterface(meter) {
		return meter
	}
	return &sqlStatsMeter{Meter: meter}
}

type sqlStatsMeter struct {
	metric.Meter
}

func (meter *sqlStatsMeter) RegisterCallback(callback metric.Callback, instruments ...metric.Observable) (registration metric.Registration, err error) {
	gate := newMetricCallbackGate(callback)
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		cleanupErr := gate.cleanup(registration)
		registration = nil
		err = errors.Join(ErrInvalidMetricRegistration, cleanupErr)
	}()
	registration, err = meter.Meter.RegisterCallback(gate.call, instruments...)
	completed = true
	if err != nil || nilInterface(registration) {
		cleanupErr := gate.cleanup(registration)
		return nil, errors.Join(ErrInvalidMetricRegistration, cleanupErr)
	}
	return &guardedMetricRegistration{Registration: registration, gate: gate}, nil
}

type metricCallbackGate struct {
	mu       sync.Mutex
	cond     *sync.Cond
	active   bool
	inFlight int
	callback metric.Callback
}

func newMetricCallbackGate(callback metric.Callback) *metricCallbackGate {
	gate := &metricCallbackGate{active: true, callback: callback}
	gate.cond = sync.NewCond(&gate.mu)
	return gate
}

func (gate *metricCallbackGate) call(ctx context.Context, observer metric.Observer) (err error) {
	gate.mu.Lock()
	if !gate.active || gate.callback == nil {
		gate.mu.Unlock()
		return nil
	}
	callback := gate.callback
	gate.inFlight++
	gate.mu.Unlock()
	defer func() {
		gate.mu.Lock()
		gate.inFlight--
		if gate.inFlight == 0 {
			gate.cond.Broadcast()
		}
		gate.mu.Unlock()
		if recover() != nil {
			err = ErrMetricCallback
		}
	}()
	return callback(ctx, observer)
}

func (gate *metricCallbackGate) cleanup(registration metric.Registration) error {
	gate.mu.Lock()
	gate.active = false
	gate.mu.Unlock()
	err := safeMetricUnregister(registration)
	gate.mu.Lock()
	for gate.inFlight > 0 {
		gate.cond.Wait()
	}
	gate.callback = nil
	gate.mu.Unlock()
	return err
}

type guardedMetricRegistration struct {
	metric.Registration
	gate *metricCallbackGate
	once sync.Once
	err  error
}

func (registration *guardedMetricRegistration) Unregister() error {
	if registration == nil {
		return nil
	}
	registration.once.Do(func() {
		registration.err = registration.gate.cleanup(registration.Registration)
		registration.Registration = nil
		registration.gate = nil
	})
	return registration.err
}

func safeMetricUnregister(registration metric.Registration) (err error) {
	if nilInterface(registration) {
		return nil
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		err = ErrMetricRegistrationCleanup
	}()
	if registration.Unregister() != nil {
		err = ErrMetricRegistrationCleanup
	}
	completed = true
	return err
}

func guardMetricRegistration(registration metric.Registration, err error) (*MetricRegistration, error) {
	if err != nil {
		if !nilInterface(registration) {
			err = errors.Join(err, safeMetricUnregister(registration))
		}
		return nil, err
	}
	if nilInterface(registration) {
		return nil, ErrInvalidMetricRegistration
	}
	return &MetricRegistration{Registration: registration}, nil
}
