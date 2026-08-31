package port_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/frostgrove/vv/port"
)

func TestTheLibrarysOwnLinesGoWhereTheApplicationSays(t *testing.T) {
	var buf bytes.Buffer
	mine := slog.New(slog.NewTextHandler(&buf, nil)).With("request", "abc123")

	ctx := port.WithLogger(context.Background(), mine)
	port.Logger(ctx).Error("a thing happened", "detail", 7)

	out := buf.String()
	if !strings.Contains(out, "a thing happened") {
		t.Fatalf("the line did not reach the application's logger: %q", out)
	}
	if !strings.Contains(out, "request=abc123") {
		t.Fatalf("the line lost the fields the application's logger carries: %q", out)
	}

	other := port.Logger(context.Background())
	if other == nil {
		t.Fatal("a context with no logger answered nil")
	}
	if other == mine {
		t.Fatal("a context with no logger answered the one that was never put in it")
	}

	if got := port.Logger(port.WithLogger(context.Background(), nil)); got == nil {
		t.Fatal("WithLogger(nil) stored a nil logger")
	}
}
