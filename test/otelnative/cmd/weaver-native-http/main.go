package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	endpoint := flag.String("endpoint", "127.0.0.1:14317", "OTLP/gRPC Weaver live-check endpoint")
	flag.Parse()
	if err := run(*endpoint); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return err
	}
	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(endpoint), otlpmetricgrpc.WithInsecure())
	if err != nil {
		return errors.Join(err, traceExporter.Shutdown(ctx))
	}
	telemetryResource := resource.NewSchemaless(
		attribute.String("service.name", "frostgrove-weaver-native-http-fixture"),
		attribute.String("service.version", "v1"),
	)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(telemetryResource),
		sdktrace.WithBatcher(traceExporter),
	)
	meterProvider := metric.NewMeterProvider(
		metric.WithResource(telemetryResource),
		metric.WithReader(metric.NewPeriodicReader(metricExporter, metric.WithInterval(time.Hour))),
	)
	requestErr := exerciseHTTP(tracerProvider, meterProvider)
	flushErr := errors.Join(tracerProvider.ForceFlush(ctx), meterProvider.ForceFlush(ctx))
	shutdownErr := errors.Join(tracerProvider.Shutdown(ctx), meterProvider.Shutdown(ctx))
	return errors.Join(requestErr, flushErr, shutdownErr)
}

func exerciseHTTP(tracerProvider *sdktrace.TracerProvider, meterProvider *metric.MeterProvider) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /weaver", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := otelhttp.NewHandler(mux, "weaver-native-http",
		otelhttp.WithTracerProvider(tracerProvider),
		otelhttp.WithMeterProvider(meterProvider),
	)
	server := httptest.NewServer(handler)
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/weaver", nil)
	if err != nil {
		return err
	}
	response, err := server.Client().Do(request)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.Join(fmt.Errorf("HTTP fixture status %d", response.StatusCode), copyErr, closeErr)
	}
	return errors.Join(copyErr, closeErr)
}
