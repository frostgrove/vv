package main

import (
	"context"
	"errors"
	"log"
	"time"

	vvotel "github.com/frostgrove/vv/otel"
)

func main() {
	telemetry, err := NewTelemetry(context.Background(), Config{
		ServiceName:       "orders-api",
		ServiceVersion:    "1.0.0",
		ServiceNamespace:  "commerce",
		FrameworkResource: vvotel.MustApproveName("orders"),
	})
	if err != nil {
		log.Fatal(err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := telemetry.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Print(err)
	}
}
