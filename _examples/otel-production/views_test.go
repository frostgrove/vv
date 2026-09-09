package main

import (
	"fmt"
	"reflect"
	"testing"

	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func TestFrostgroveViewsCoverEveryImplementedMetricExactlyOnce(t *testing.T) {
	views := frostgroveViews()
	implemented := 0
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.Kind != "metric" || descriptor.Availability != "implemented" {
			continue
		}
		implemented++
		instrument := sdkmetric.Instrument{
			Name: descriptor.Name,
			Scope: instrumentation.Scope{
				Name:    vvotel.ScopeName,
				Version: vvotel.ScopeVersion,
			},
		}
		var matches []sdkmetric.Stream
		for _, view := range views {
			if stream, ok := view(instrument); ok {
				matches = append(matches, stream)
			}
		}
		if len(matches) != 1 {
			t.Fatalf("views matching %s = %d", descriptor.Name, len(matches))
		}
		stream := matches[0]
		allowedKeys := make(map[attribute.Key]struct{})
		for _, variant := range descriptor.Variants {
			for _, spec := range variant.Attributes {
				allowedKeys[spec.Key] = struct{}{}
			}
		}
		for key := range allowedKeys {
			if !stream.AttributeFilter(attribute.String(string(key), "value")) {
				t.Fatalf("view %s rejects declared key %s", descriptor.Name, key)
			}
		}
		if stream.AttributeFilter(attribute.String("secret.key", "secret")) {
			t.Fatalf("view %s admits an undeclared key", descriptor.Name)
		}
		if descriptor.Instrument == "histogram" {
			aggregation, ok := stream.Aggregation.(sdkmetric.AggregationExplicitBucketHistogram)
			if !ok || !reflect.DeepEqual(aggregation.Boundaries, descriptor.Boundaries) {
				t.Fatalf("view %s buckets = %#v", descriptor.Name, stream.Aggregation)
			}
		} else if stream.Aggregation != nil {
			t.Fatalf("view %s changes %s aggregation", descriptor.Name, descriptor.Instrument)
		}
	}
	if len(views) != implemented {
		t.Fatalf("views = %d, implemented metrics = %d", len(views), implemented)
	}
}

func TestTelemetryConfigBoundsCombinedUniqueFrameworkResources(t *testing.T) {
	config := validTelemetryConfig()
	config.FrameworkResources = make([]vvotel.ApprovedName, 0, vvotel.MaxResourceNameValues)
	for index := 0; index < vvotel.MaxResourceNameValues; index++ {
		config.FrameworkResources = append(config.FrameworkResources, vvotel.MustApproveName(fmt.Sprintf("resource-%d", index)))
	}
	if _, err := normalizeTelemetryConfig(config); err == nil {
		t.Fatal("primary plus maximum distinct additional resources was accepted")
	}
	config.FrameworkResources[len(config.FrameworkResources)-1] = config.FrameworkResource
	if _, err := normalizeTelemetryConfig(config); err != nil {
		t.Fatalf("maximum unique resource domain: %v", err)
	}
}
