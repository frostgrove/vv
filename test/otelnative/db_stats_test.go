package otelnative

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type callbackMeterProvider struct {
	metric.MeterProvider
	meter metric.Meter
}

func (provider *callbackMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return provider.meter
}

type callbackMeter struct {
	metric.Meter
	callback     metric.Callback
	registration metric.Registration
	err          error
	panic        bool
}

func (meter *callbackMeter) RegisterCallback(callback metric.Callback, _ ...metric.Observable) (metric.Registration, error) {
	meter.callback = callback
	if meter.panic {
		panic("registration-secret-93821")
	}
	return meter.registration, meter.err
}

type registrationProbe struct {
	metric.Registration
	calls  atomic.Int64
	err    error
	panic  bool
	goexit bool
}

func (registration *registrationProbe) Unregister() error {
	registration.calls.Add(1)
	if registration.panic {
		panic("unregister-secret-29173")
	}
	if registration.goexit {
		runtime.Goexit()
	}
	return registration.err
}

type pgxStatsProbe struct {
	calls   atomic.Int64
	once    sync.Once
	entered chan struct{}
	release chan struct{}
	panic   bool
	result  *pgxpool.Stat
}

func (source *pgxStatsProbe) Stat() *pgxpool.Stat {
	source.calls.Add(1)
	if source.entered != nil {
		source.once.Do(func() { close(source.entered) })
	}
	if source.release != nil {
		<-source.release
	}
	if source.panic {
		panic("pgx stats")
	}
	return source.result
}

type countingObserver struct {
	metric.Observer
	count atomic.Int64
}

func (observer *countingObserver) ObserveFloat64(metric.Float64Observable, float64, ...metric.ObserveOption) {
	observer.count.Add(1)
}

func (observer *countingObserver) ObserveInt64(metric.Int64Observable, int64, ...metric.ObserveOption) {
	observer.count.Add(1)
}

type blockingObserver struct {
	metric.Observer
	once    sync.Once
	entered chan struct{}
	release chan struct{}
	count   atomic.Int64
}

func (observer *blockingObserver) observe() {
	observer.count.Add(1)
	observer.once.Do(func() { close(observer.entered) })
	<-observer.release
}

func (observer *blockingObserver) ObserveFloat64(metric.Float64Observable, float64, ...metric.ObserveOption) {
	observer.observe()
}

func (observer *blockingObserver) ObserveInt64(metric.Int64Observable, int64, ...metric.ObserveOption) {
	observer.observe()
}

func TestPGXPoolStatsOwnsARegistrationAndBoundedIdentity(t *testing.T) {
	const secretPoolConfig = "secret-pool-config-4815"
	poolName, err := NewDatabasePoolName("primary")
	if err != nil {
		t.Fatal(err)
	}
	fixture := newDatabaseTelemetryFixture(t, []DatabasePoolName{poolName}, nil)
	config, err := pgxpool.ParseConfig("postgres://secret_user:secret_password@127.0.0.1:1/" + secretPoolConfig + "?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	registration, err := RegisterPGXPoolStats(fixture.providers, pool, poolName)
	if err != nil {
		t.Fatal(err)
	}
	fixture.flushMetrics(t)

	for _, name := range []string{
		pgxPoolAcquiredMetric,
		pgxPoolIdleMetric,
		pgxPoolMaximumMetric,
		pgxPoolAcquireWaitMetric,
		pgxPoolAcquireTimeMetric,
	} {
		if !hasMetric(fixture.metrics, pgxPoolScopeName, name) {
			t.Fatalf("missing pgx pool metric %q", name)
		}
	}
	parts := metricPrivacyParts(fixture.metrics.snapshot())
	if !slices.Contains(parts, poolName.String()) {
		t.Fatalf("pool identity missing from %v", parts)
	}
	assertNativePrivacy(t, nil, fixture.metrics.snapshot(), secretPoolConfig, "secret_user", "secret_password", secretResource)
	if err = registration.Unregister(); err != nil {
		t.Fatal(err)
	}
	if err = registration.Unregister(); err != nil {
		t.Fatal(err)
	}
}

func TestPGXPoolStatsDeactivatesDrainsAndClearsCallback(t *testing.T) {
	native := &registrationProbe{}
	meter := &callbackMeter{Meter: metricnoop.NewMeterProvider().Meter("base"), registration: native}
	providers := Providers{
		Tracer: tracenoop.NewTracerProvider(),
		Meter:  &callbackMeterProvider{MeterProvider: metricnoop.NewMeterProvider(), meter: meter},
	}
	pool := &pgxStatsProbe{entered: make(chan struct{}), release: make(chan struct{})}
	registration, err := RegisterPGXPoolStats(providers, pool, mustDatabasePoolName(t, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	observer := &countingObserver{}
	callbackDone := make(chan error, 1)
	go func() { callbackDone <- meter.callback(context.Background(), observer) }()
	select {
	case <-pool.entered:
	case <-time.After(time.Second):
		t.Fatal("callback did not enter Stat")
	}
	unregisterDone := make(chan error, 1)
	go func() { unregisterDone <- registration.Unregister() }()
	deadline := time.Now().Add(time.Second)
	for native.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if native.calls.Load() != 1 {
		t.Fatal("native registration was not unregistered")
	}
	select {
	case err := <-unregisterDone:
		t.Fatalf("Unregister returned before callback drain: %v", err)
	default:
	}
	close(pool.release)
	if err = <-callbackDone; err != nil {
		t.Fatalf("callback error=%v", err)
	}
	if err = <-unregisterDone; err != nil {
		t.Fatalf("Unregister error=%v", err)
	}
	beforeCalls := pool.calls.Load()
	beforeObservations := observer.count.Load()
	if err = meter.callback(context.Background(), observer); err != nil || pool.calls.Load() != beforeCalls || observer.count.Load() != beforeObservations {
		t.Fatalf("retained callback = %v calls:%d/%d observations:%d/%d", err, pool.calls.Load(), beforeCalls, observer.count.Load(), beforeObservations)
	}
	if err = registration.Unregister(); err != nil || native.calls.Load() != 1 {
		t.Fatalf("repeated Unregister=%v calls=%d", err, native.calls.Load())
	}
}

func TestPGXPoolStatsContainsCallbackAndCleanupFailures(t *testing.T) {
	newRegistration := func(t *testing.T, pool *pgxStatsProbe, native *registrationProbe) (*MetricRegistration, *callbackMeter) {
		t.Helper()
		meter := &callbackMeter{Meter: metricnoop.NewMeterProvider().Meter("base"), registration: native}
		providers := Providers{
			Tracer: tracenoop.NewTracerProvider(),
			Meter:  &callbackMeterProvider{MeterProvider: metricnoop.NewMeterProvider(), meter: meter},
		}
		registration, err := RegisterPGXPoolStats(providers, pool, mustDatabasePoolName(t, "primary"))
		if err != nil {
			t.Fatal(err)
		}
		return registration, meter
	}

	pool := &pgxStatsProbe{panic: true}
	registration, meter := newRegistration(t, pool, &registrationProbe{})
	if err := meter.callback(context.Background(), &countingObserver{}); !errors.Is(err, ErrMetricCallback) {
		t.Fatalf("Stat panic error=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	before := pool.calls.Load()
	if err := meter.callback(canceled, &countingObserver{}); !errors.Is(err, context.Canceled) || pool.calls.Load() != before {
		t.Fatalf("canceled callback=%v calls=%d/%d", err, pool.calls.Load(), before)
	}
	if err := registration.Unregister(); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			native := &registrationProbe{panic: mode == "panic", goexit: mode == "goexit"}
			pool := &pgxStatsProbe{}
			registration, meter := newRegistration(t, pool, native)
			if mode == "panic" {
				if err := registration.Unregister(); !errors.Is(err, ErrMetricRegistrationCleanup) {
					t.Fatalf("panic cleanup error=%v", err)
				}
			} else {
				exited := make(chan struct{})
				go func() {
					defer close(exited)
					_ = registration.Unregister()
				}()
				select {
				case <-exited:
				case <-time.After(time.Second):
					t.Fatal("Goexit cleanup did not unwind")
				}
			}
			if err := registration.Unregister(); !errors.Is(err, ErrMetricRegistrationCleanup) || native.calls.Load() != 1 {
				t.Fatalf("terminal cleanup=%v calls=%d", err, native.calls.Load())
			}
			before := pool.calls.Load()
			if err := meter.callback(context.Background(), &countingObserver{}); err != nil || pool.calls.Load() != before {
				t.Fatalf("post-cleanup callback=%v calls=%d/%d", err, pool.calls.Load(), before)
			}
		})
	}
}

func TestDatabaseMetricExporterDropsUnconfiguredPoolAtExport(t *testing.T) {
	const secretPool = "secret_pool_identity_4815"
	allowedPool, err := NewDatabasePoolName("primary")
	if err != nil {
		t.Fatal(err)
	}
	sink := &metricCapture{}
	exporter, err := NewDatabaseMetricExporter(sink, DatabaseProjectionPolicy{
		PoolNames: []DatabasePoolName{allowedPool},
		ResourceAttributes: []attribute.KeyValue{
			attribute.String("service.name", databaseServiceName),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	original := &metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(
			attribute.String("service.name", databaseServiceName),
			attribute.String("secret.resource", secretResource),
		),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope: instrumentation.Scope{
				Name:       sqlScopeName,
				Version:    "0.43.0",
				SchemaURL:  "secret-schema-4815",
				Attributes: attribute.NewSet(attribute.String("secret.scope", secretPool)),
			},
			Metrics: []metricdata.Metrics{{
				Name: "db.sql.connection.max_open",
				Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{
					Attributes: attribute.NewSet(
						attribute.String(databasePoolKey, secretPool),
						attribute.String("secret.attribute", secretPool),
					),
					Value: 1,
				}}},
			}},
		}},
	}
	if err = exporter.Export(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	exported := sink.snapshot()
	assertNativePrivacy(t, nil, exported, secretPool, secretResource, "secret-schema-4815")
	if len(exported.ScopeMetrics) != 0 {
		t.Fatalf("unknown scope tuple survived=%#v", exported.ScopeMetrics)
	}
}

func TestDatabasePoolNameIsClosedConfiguration(t *testing.T) {
	for _, value := range []string{"", "-primary", "primary pool", "primary/tenant"} {
		if _, err := NewDatabasePoolName(value); !errors.Is(err, ErrInvalidDatabasePoolName) {
			t.Fatalf("value=%q error=%v", value, err)
		}
	}
	pool, err := NewDatabasePoolName("primary.eu-1")
	if err != nil || pool.String() != "primary.eu-1" {
		t.Fatalf("pool=%q error=%v", pool.String(), err)
	}
}

func TestSQLDBStatsCleansPartialRegistrationAndRedactsFailure(t *testing.T) {
	const secret = "register-error-secret-58214"
	registration := &registrationProbe{}
	meter := &callbackMeter{
		Meter:        metricnoop.NewMeterProvider().Meter("base"),
		registration: registration,
		err:          errors.New(secret),
	}
	database := sql.OpenDB(outcomeConnector{})
	defer database.Close()
	pool := mustDatabasePoolName(t, "primary")
	providers := Providers{
		Tracer: tracenoop.NewTracerProvider(),
		Meter:  &callbackMeterProvider{MeterProvider: metricnoop.NewMeterProvider(), meter: meter},
	}

	got, err := RegisterSQLDBStats(providers, database, pool)
	if got != nil || !errors.Is(err, ErrInvalidMetricRegistration) || strings.Contains(err.Error(), secret) {
		t.Fatalf("registration/error = %#v/%v", got, err)
	}
	if registration.calls.Load() != 1 {
		t.Fatalf("partial registration cleanup calls = %d", registration.calls.Load())
	}
	observer := &countingObserver{}
	if err := meter.callback(context.Background(), observer); err != nil || observer.count.Load() != 0 {
		t.Fatalf("retained callback result/count = %v/%d", err, observer.count.Load())
	}
}

func TestSQLDBStatsDeactivatesUnregistersDrainsAndClearsCallback(t *testing.T) {
	registration := &registrationProbe{}
	meter := &callbackMeter{
		Meter:        metricnoop.NewMeterProvider().Meter("base"),
		registration: registration,
	}
	database := sql.OpenDB(outcomeConnector{})
	defer database.Close()
	providers := Providers{
		Tracer: tracenoop.NewTracerProvider(),
		Meter:  &callbackMeterProvider{MeterProvider: metricnoop.NewMeterProvider(), meter: meter},
	}
	guarded, err := RegisterSQLDBStats(providers, database, mustDatabasePoolName(t, "primary"))
	if err != nil {
		t.Fatal(err)
	}
	observer := &blockingObserver{entered: make(chan struct{}), release: make(chan struct{})}
	callbackDone := make(chan error, 1)
	go func() { callbackDone <- meter.callback(context.Background(), observer) }()
	select {
	case <-observer.entered:
	case <-time.After(time.Second):
		t.Fatal("callback did not enter")
	}
	unregisterDone := make(chan error, 1)
	go func() { unregisterDone <- guarded.Unregister() }()
	deadline := time.Now().Add(time.Second)
	for registration.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if registration.calls.Load() != 1 {
		t.Fatal("underlying registration was not unregistered before callback drain")
	}
	select {
	case err := <-unregisterDone:
		t.Fatalf("Unregister returned before in-flight callback drained: %v", err)
	default:
	}
	close(observer.release)
	if err := <-callbackDone; err != nil {
		t.Fatalf("callback error = %v", err)
	}
	if err := <-unregisterDone; err != nil {
		t.Fatalf("Unregister error = %v", err)
	}
	before := observer.count.Load()
	if err := meter.callback(context.Background(), observer); err != nil || observer.count.Load() != before {
		t.Fatalf("post-cleanup callback result/count = %v/%d, want %d", err, observer.count.Load(), before)
	}
	if err := guarded.Unregister(); err != nil || registration.calls.Load() != 1 {
		t.Fatalf("repeated Unregister = %v, underlying calls %d", err, registration.calls.Load())
	}
}

func TestSQLDBStatsContainsRegistrationAndCallbackPanics(t *testing.T) {
	database := sql.OpenDB(outcomeConnector{})
	defer database.Close()
	pool := mustDatabasePoolName(t, "primary")
	for _, test := range []struct {
		name         string
		registration metric.Registration
		panic        bool
	}{
		{name: "nil"},
		{name: "typed nil", registration: (*registrationProbe)(nil)},
		{name: "panic", panic: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			meter := &callbackMeter{
				Meter:        metricnoop.NewMeterProvider().Meter("base"),
				registration: test.registration,
				panic:        test.panic,
			}
			providers := Providers{
				Tracer: tracenoop.NewTracerProvider(),
				Meter:  &callbackMeterProvider{MeterProvider: metricnoop.NewMeterProvider(), meter: meter},
			}
			if got, err := RegisterSQLDBStats(providers, database, pool); got != nil || !errors.Is(err, ErrInvalidMetricRegistration) {
				t.Fatalf("registration/error = %#v/%v", got, err)
			}
		})
	}

	registration := &registrationProbe{}
	meter := &callbackMeter{Meter: metricnoop.NewMeterProvider().Meter("base"), registration: registration}
	providers := Providers{
		Tracer: tracenoop.NewTracerProvider(),
		Meter:  &callbackMeterProvider{MeterProvider: metricnoop.NewMeterProvider(), meter: meter},
	}
	guarded, err := RegisterSQLDBStats(providers, database, pool)
	if err != nil {
		t.Fatal(err)
	}
	observer := &blockingObserver{entered: make(chan struct{}), release: make(chan struct{})}
	close(observer.release)
	observer.once.Do(func() { close(observer.entered) })
	observer.Observer = panicObserver{}
	if err := meter.callback(context.Background(), panicObserver{}); !errors.Is(err, ErrMetricCallback) {
		t.Fatalf("callback panic error = %v", err)
	}
	if err := guarded.Unregister(); err != nil {
		t.Fatal(err)
	}
}

type panicObserver struct {
	metric.Observer
}

func (panicObserver) ObserveFloat64(metric.Float64Observable, float64, ...metric.ObserveOption) {
	panic("observer-secret-72914")
}

func (panicObserver) ObserveInt64(metric.Int64Observable, int64, ...metric.ObserveOption) {
	panic("observer-secret-72914")
}

func mustDatabasePoolName(t *testing.T, value string) DatabasePoolName {
	t.Helper()
	pool, err := NewDatabasePoolName(value)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
