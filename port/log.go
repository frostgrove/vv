package port

import (
	"context"
	"log/slog"
)

// Logger is the one place this library writes a line the caller did not ask for.
//
// There are only a handful of them and they are all the same shape: something
// happened that the client must not be told about and that nobody can be
// returned an error for — a handler panicked and the connection has to be
// closed, a response would not marshal, a status could not carry its details.
// The request is already over; the line is all that is left.
//
// Before this, those lines went to log.Printf — the standard library's package
// logger, which belongs to the whole process. A consumer could not route them to
// their own logger, could not give them the request's trace id, and could not
// silence them without silencing everything else in the binary that reached for
// the same default.
//
// The logger comes from the context when one was put there and from
// [slog.Default] otherwise, so the zero-configuration case still writes
// somewhere and an application that wires a request-scoped logger gets its
// fields on these lines too. It is never nil.
func Logger(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && l != nil {
			return l
		}
	}
	return slog.Default()
}

// WithLogger carries a logger for [Logger] to find — the seam an application
// uses to give this library's own lines the request's trace id, or to send them
// somewhere other than the process default.
//
// A nil logger is ignored rather than stored. "No logger" means slog.Default,
// and a context that answered nil would make every call site check.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerKey{}, l)
}

type loggerKey struct{}
