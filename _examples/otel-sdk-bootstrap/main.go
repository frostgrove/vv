package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/storage"
	"github.com/frostgrove/vv/storage/storagefs"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Product struct {
	ID   string
	Name string
}

type dummyService struct{}

func (d *dummyService) Meta() *crud.Meta     { return nil }
func (d *dummyService) Paths() errs.Resolver { return nil }
func (d *dummyService) List(ctx context.Context, cmd port.ListCommand) (crud.PaginatedResponse[Product], error) {
	return crud.PaginatedResponse[Product]{}, nil
}
func (d *dummyService) Count(ctx context.Context, cmd port.CountCommand) (int64, error) {
	return 0, nil
}
func (d *dummyService) Get(ctx context.Context, cmd port.GetCommand[string]) (Product, error) {
	return Product{ID: cmd.ID, Name: "Demo Product"}, nil
}
func (d *dummyService) Create(ctx context.Context, cmd port.CreateCommand[Product]) (Product, error) {
	return cmd.Model, nil
}
func (d *dummyService) Update(ctx context.Context, cmd port.UpdateCommand[string, Product]) (Product, error) {
	return Product{}, nil
}
func (d *dummyService) Replace(ctx context.Context, cmd port.ReplaceCommand[string, Product]) (Product, error) {
	return Product{}, nil
}
func (d *dummyService) Delete(ctx context.Context, cmd port.DeleteCommand[string]) (int64, error) {
	return 1, nil
}
func (d *dummyService) DeleteMany(ctx context.Context, cmd port.BulkDeleteCommand[string]) (int64, error) {
	return 1, nil
}

func main() {
	ctx := context.Background()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("demo-service"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		log.Fatalf("resource.New failed: %v", err)
	}

	traceExporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatalf("stdouttrace.New failed: %v", err)
	}
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(traceExporter)),
	)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracerProvider.Shutdown(shutdownCtx)
	}()

	metricExporter, err := stdoutmetric.New(stdoutmetric.WithPrettyPrint())
	if err != nil {
		log.Fatalf("stdoutmetric.New failed: %v", err)
	}
	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(metricExporter)),
	)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = meterProvider.Shutdown(shutdownCtx)
	}()

	telemetry, err := vvotel.New(vvotel.Config{
		TracerProvider: tracerProvider,
		MeterProvider:  meterProvider,
		ResourceName:   "products",
	})
	if err != nil {
		log.Fatalf("vvotel.New failed: %v", err)
	}

	service := port.ChainService[Product, string, Product](
		&dummyService{},
		vvotel.Service[Product, string, Product](telemetry),
	)

	tmpDir, err := os.MkdirTemp("", "frostgrove-otel-*")
	if err != nil {
		log.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	backend, err := storagefs.New(&storagefs.Config{Root: tmpDir})
	if err != nil {
		log.Fatalf("storagefs.New failed: %v", err)
	}
	baseStore, err := storage.New(&storage.Config{Namespace: "demo", Backend: backend})
	if err != nil {
		log.Fatalf("storage.New failed: %v", err)
	}
	store := storage.Chain(
		baseStore,
		vvotel.Store(telemetry),
	)

	obs := cache.MustObservers(
		vvotel.Cache(telemetry, vvotel.WithCacheSpanEvents(true)),
	)

	prod, err := service.Get(ctx, port.GetCommand[string]{ID: "prod-1"})
	if err != nil {
		log.Fatalf("service.Get failed: %v", err)
	}
	fmt.Printf("Fetched product: %+v\n", prod)

	k, _ := storage.ParseKey("sample.txt")
	info, err := store.Put(ctx, k, strings.NewReader("hello"), storage.PutOptions{Mode: storage.Replace})
	if err != nil {
		log.Fatalf("store.Put failed: %v", err)
	}
	fmt.Printf("Stored file, size: %d bytes\n", info.Size)

	obs.Observe(ctx, cache.Event{
		Operation: cache.LookupOperation,
		Outcome:   cache.HitOutcome,
	})
	fmt.Println("Observed cache event")

	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tracerProvider.ForceFlush(flushCtx); err != nil {
		log.Printf("trace flush failed: %v", err)
	}
	if err := meterProvider.ForceFlush(flushCtx); err != nil {
		log.Printf("metric flush failed: %v", err)
	}
}
