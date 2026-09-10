package main

import (
	"os"
	"strings"
	"testing"
)

func TestFixtureUsesOnlyNativeInstrumentationAndManagedProviders(t *testing.T) {
	content, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	for _, required := range []string{
		"otelhttp.NewHandler",
		"sdktrace.AlwaysSample()",
		"otlptracegrpc.New",
		"otlpmetricgrpc.New",
		"tracerProvider.ForceFlush",
		"meterProvider.ForceFlush",
		"tracerProvider.Shutdown",
		"meterProvider.Shutdown",
		`http.NewRequest(http.MethodGet`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("native Weaver fixture lacks %q", required)
		}
	}
	for _, forbidden := range []string{"github.com/frostgrove/vv/otel", "otel.SetTracerProvider", "otel.SetMeterProvider"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("native Weaver fixture contains %q", forbidden)
		}
	}
}
