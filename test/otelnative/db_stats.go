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
	registration metric.Registration
	gate         *metricCallbackGate
	once         sync.Once
	err          error
}

type providerMetricRegistration struct {
	metric.Registration
	owner *MetricRegistration
}

func (r *providerMetricRegistration) Unregister() error {
	if r == nil || r.owner == nil {
		return nil
	}
	return r.owner.Unregister()
}

func (r *MetricRegistration) Unregister() error {
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		r.err = ErrMetricRegistrationCleanup
		registration := r.registration
		gate := r.gate
		r.registration = nil
		r.gate = nil
		if gate != nil {
			r.err = gate.cleanup(registration)
		} else {
			r.err = safeMetricUnregister(registration)
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
	return runNativeAssembly(func() (*MetricRegistration, error) {
		return registerPGXPoolStats(providers, pool, poolName)
	})
}

func registerPGXPoolStats(providers Providers, pool PGXPoolStatsSource, poolName DatabasePoolName) (*MetricRegistration, error) {
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
	return registerMetricCallback(meter, func(ctx context.Context, observer metric.Observer) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		stats := pool.Stat()
		if stats == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		observer.ObserveInt64(acquired, int64(stats.AcquiredConns()), observe)
		if err := ctx.Err(); err != nil {
			return err
		}
		observer.ObserveInt64(idle, int64(stats.IdleConns()), observe)
		if err := ctx.Err(); err != nil {
			return err
		}
		observer.ObserveInt64(maximum, int64(stats.MaxConns()), observe)
		if err := ctx.Err(); err != nil {
			return err
		}
		observer.ObserveInt64(waits, stats.EmptyAcquireCount(), observe)
		if err := ctx.Err(); err != nil {
			return err
		}
		observer.ObserveFloat64(waitDuration, stats.EmptyAcquireWaitTime().Seconds(), observe)
		return nil
	}, acquired, idle, maximum, waits, waitDuration)
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
	return runNativeAssembly(func() (*MetricRegistration, error) {
		registration, err := otelsql.RegisterDBStatsMetrics(
			database,
			otelsql.WithTracerProvider(providers.Tracer),
			otelsql.WithMeterProvider(&sqlStatsMeterProvider{MeterProvider: providers.Meter}),
			otelsql.WithAttributes(attribute.String(databasePoolKey, poolName.value)),
		)
		return guardMetricRegistration(registration, err, nil)
	})
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
	owner, err := registerMetricCallback(meter.Meter, callback, instruments...)
	if err != nil {
		return nil, err
	}
	return &providerMetricRegistration{Registration: owner.registration, owner: owner}, nil
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
	completed := false
	defer func() {
		gate.mu.Lock()
		gate.inFlight--
		if gate.inFlight == 0 {
			gate.cond.Broadcast()
		}
		gate.mu.Unlock()
		if !completed {
			_ = recover()
			err = ErrMetricCallback
		}
	}()
	err = callback(ctx, observer)
	completed = true
	return err
}

func (gate *metricCallbackGate) cleanup(registration metric.Registration) (err error) {
	gate.mu.Lock()
	gate.active = false
	gate.mu.Unlock()
	err = ErrMetricRegistrationCleanup
	completed := false
	defer func() {
		if !completed {
			_ = recover()
		}
		gate.mu.Lock()
		for gate.inFlight > 0 {
			gate.cond.Wait()
		}
		gate.callback = nil
		gate.mu.Unlock()
	}()
	err = safeMetricUnregister(registration)
	completed = true
	return err
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

func registerMetricCallback(meter metric.Meter, callback metric.Callback, instruments ...metric.Observable) (result *MetricRegistration, err error) {
	gate := newMetricCallbackGate(callback)
	var registration metric.Registration
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		cleanupErr := gate.cleanup(registration)
		result = nil
		err = errors.Join(ErrInvalidMetricRegistration, cleanupErr)
	}()
	registration, err = meter.RegisterCallback(gate.call, instruments...)
	completed = true
	return guardMetricRegistration(registration, err, gate)
}

func guardMetricRegistration(registration metric.Registration, err error, gate *metricCallbackGate) (*MetricRegistration, error) {
	if err != nil {
		var cleanupErr error
		if gate != nil {
			cleanupErr = gate.cleanup(registration)
		} else if !nilInterface(registration) {
			cleanupErr = safeMetricUnregister(registration)
		}
		return nil, errors.Join(ErrInvalidMetricRegistration, cleanupErr)
	}
	if nilInterface(registration) {
		if gate != nil {
			_ = gate.cleanup(nil)
		}
		return nil, ErrInvalidMetricRegistration
	}
	return &MetricRegistration{registration: registration, gate: gate}, nil
}
