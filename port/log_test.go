package port_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/shardit-io/vv/port"
)

// The library's own lines go where the application says, and somewhere sensible
// when it says nothing.
//
// They used to go to log.Printf — the standard library's package logger, which
// belongs to the whole process. A consumer could not give these lines the
// request's trace id, could not route them to their own handler, and could not
// silence them without silencing every other library in the binary that reached
// for the same default.
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

	// The control. Every assertion above would hold for a Logger that ignored
	// the context and wrote to this handler because it was also the default, so
	// a context without one has to answer something else — and never nil, which
	// is what every call site would otherwise have to check.
	other := port.Logger(context.Background())
	if other == nil {
		t.Fatal("a context with no logger answered nil")
	}
	if other == mine {
		t.Fatal("a context with no logger answered the one that was never put in it")
	}

	// And a nil logger is not storable, or "no logger" would have two spellings
	// and only one of them would be checked.
	if got := port.Logger(port.WithLogger(context.Background(), nil)); got == nil {
		t.Fatal("WithLogger(nil) stored a nil logger")
	}
}
