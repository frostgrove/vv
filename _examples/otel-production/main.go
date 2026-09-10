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
	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 10*time.Second)
	flushErr := telemetry.ForceFlush(flushCtx)
	cancelFlush()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Second)
	shutdownErr := telemetry.Shutdown(shutdownCtx)
	cancelShutdown()
	if err := errors.Join(flushErr, shutdownErr); err != nil {
		log.Print(err)
	}
}
