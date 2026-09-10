package main

import (
	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func frostgroveViews() []sdkmetric.View {
	descriptors := vvotel.SignalDescriptors()
	views := make([]sdkmetric.View, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if descriptor.Availability != "implemented" || descriptor.Kind != "metric" {
			continue
		}
		keys := make(map[attribute.Key]struct{})
		for _, variant := range descriptor.Variants {
			for _, spec := range variant.Attributes {
				keys[spec.Key] = struct{}{}
			}
		}
		stream := sdkmetric.Stream{AttributeFilter: attributeFilter(keys)}
		if descriptor.Instrument == "histogram" {
			stream.Aggregation = sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: append([]float64(nil), descriptor.Boundaries...),
			}
		}
		views = append(views, sdkmetric.NewView(
			sdkmetric.Instrument{
				Name: descriptor.Name,
				Scope: instrumentation.Scope{
					Name:    vvotel.ScopeName,
					Version: vvotel.ScopeVersion,
				},
			},
			stream,
		))
	}
	return views
}

func attributeFilter(keys map[attribute.Key]struct{}) attribute.Filter {
	return func(candidate attribute.KeyValue) bool {
		_, ok := keys[candidate.Key]
		return ok
	}
}
