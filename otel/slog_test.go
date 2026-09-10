package vvotel_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/trace"
)

type slogHandlerCapture struct {
	enabled        bool
	enabledContext context.Context
	enabledLevel   slog.Level
	handledContext context.Context
	handledRecord  slog.Record
	handleError    error
	attrs          []slog.Attr
	groups         []string
}

type capturingSlogHandler struct {
	capture *slogHandlerCapture
	attrs   []slog.Attr
	groups  []string
}

type countingSlogLogValuer struct {
	calls *int
}

func (v countingSlogLogValuer) LogValue() slog.Value {
	*v.calls = *v.calls + 1
	return slog.GroupValue(slog.String(vvotel.LogTraceIDKey, "caller"))
}

func (h capturingSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	h.capture.enabledContext = ctx
	h.capture.enabledLevel = level
	return h.capture.enabled
}

func (h capturingSlogHandler) Handle(ctx context.Context, record slog.Record) error {
	h.capture.handledContext = ctx
	h.capture.handledRecord = record.Clone()
	h.capture.attrs = append([]slog.Attr(nil), h.attrs...)
	h.capture.groups = append([]string(nil), h.groups...)
	return h.capture.handleError
}

func (h capturingSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return h
}

func (h capturingSlogHandler) WithGroup(name string) slog.Handler {
	h.groups = append(append([]string(nil), h.groups...), name)
	return h
}

func TestTraceHandlerCorrelatesWithoutOverwritingCallerFields(t *testing.T) {
	t.Run("sampled and unsampled spans", func(t *testing.T) {
		for _, flags := range []trace.TraceFlags{trace.FlagsSampled, 0} {
			capture := &slogHandlerCapture{enabled: true}
			handler := vvotel.TraceHandler(capturingSlogHandler{capture: capture})
			spanContext, ctx := slogTraceContext(flags)
			record := slog.NewRecord(time.Time{}, slog.LevelError, "failed", 0)
			record.AddAttrs(
				slog.String("kept", "exact"),
				slog.Group("nested", slog.Int("count", 2)),
			)
			original := slogRecordAttrs(record)

			if err := handler.Handle(ctx, record); err != nil {
				t.Fatalf("the borrowed handler rejected a valid record: %v", err)
			}

			assertSlogAttrs(t, slogRecordAttrs(record), original)
			want := append(append([]slog.Attr(nil), original...),
				slog.String(vvotel.LogTraceIDKey, spanContext.TraceID().String()),
				slog.String(vvotel.LogSpanIDKey, spanContext.SpanID().String()),
				slog.String(vvotel.LogTraceFlagsKey, flags.String()),
			)
			assertSlogAttrs(t, slogRecordAttrs(capture.handledRecord), want)
		}
	})

	t.Run("invalid span", func(t *testing.T) {
		capture := &slogHandlerCapture{enabled: true}
		handler := vvotel.TraceHandler(capturingSlogHandler{capture: capture})
		record := slog.NewRecord(time.Time{}, slog.LevelInfo, "plain", 0)
		record.AddAttrs(slog.String("kept", "exact"))

		if err := handler.Handle(context.Background(), record); err != nil {
			t.Fatalf("the borrowed handler rejected a record without a span: %v", err)
		}

		assertSlogAttrs(t, slogRecordAttrs(capture.handledRecord), slogRecordAttrs(record))
	})

	t.Run("unresolved LogValuer", func(t *testing.T) {
		capture := &slogHandlerCapture{enabled: true}
		handler := vvotel.TraceHandler(capturingSlogHandler{capture: capture})
		calls := 0
		record := slog.NewRecord(time.Time{}, slog.LevelInfo, "dynamic", 0)
		record.AddAttrs(slog.Any("dynamic", countingSlogLogValuer{calls: &calls}))
		_, ctx := slogTraceContext(trace.FlagsSampled)

		if err := handler.Handle(ctx, record); err != nil {
			t.Fatalf("the borrowed handler rejected a LogValuer record: %v", err)
		}

		if calls != 0 {
			t.Fatalf("TraceHandler resolved caller LogValuer %d times", calls)
		}
		assertSlogAttrs(t, slogRecordAttrs(capture.handledRecord), slogRecordAttrs(record))
	})

	t.Run("reserved keys suppress the whole triplet", func(t *testing.T) {
		boundCollision := slog.Group("request", slog.String(vvotel.LogTraceFlagsKey, "caller"))
		cases := []struct {
			name       string
			attrs      []slog.Attr
			boundAttrs []slog.Attr
			groups     []string
			derive     func(slog.Handler) slog.Handler
		}{
			{
				name:  "record",
				attrs: []slog.Attr{slog.String(vvotel.LogTraceIDKey, "caller")},
			},
			{
				name: "nested record group",
				attrs: []slog.Attr{slog.Group("request",
					slog.String(vvotel.LogSpanIDKey, "caller"),
				)},
			},
			{
				name: "prior WithAttrs nested group",
				boundAttrs: []slog.Attr{
					boundCollision,
				},
				derive: func(handler slog.Handler) slog.Handler {
					return handler.WithAttrs([]slog.Attr{boundCollision})
				},
			},
			{
				name:   "current WithGroup name",
				groups: []string{vvotel.LogTraceIDKey},
				derive: func(handler slog.Handler) slog.Handler {
					return handler.WithGroup(vvotel.LogTraceIDKey)
				},
			},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				capture := &slogHandlerCapture{enabled: true}
				root := vvotel.TraceHandler(capturingSlogHandler{capture: capture})
				handler := root
				if test.derive != nil {
					handler = test.derive(handler)
				}
				record := slog.NewRecord(time.Time{}, slog.LevelWarn, "collision", 0)
				record.AddAttrs(test.attrs...)
				record.AddAttrs(slog.String("kept", "exact"))
				_, ctx := slogTraceContext(trace.FlagsSampled)

				if err := handler.Handle(ctx, record); err != nil {
					t.Fatalf("the borrowed handler rejected a colliding record: %v", err)
				}

				assertSlogAttrs(t, slogRecordAttrs(capture.handledRecord), slogRecordAttrs(record))
				assertSlogAttrs(t, capture.attrs, test.boundAttrs)
				assertSlogGroups(t, capture.groups, test.groups)

				clean := slog.NewRecord(time.Time{}, slog.LevelWarn, "clean", 0)
				if err := root.Handle(ctx, clean); err != nil {
					t.Fatalf("the root handler rejected a clean record: %v", err)
				}
				if !slogRecordOwnsCorrelation(capture.handledRecord) {
					t.Fatal("a collision in a derived handler leaked into its parent")
				}
			})
		}
	})

	t.Run("standard handler composition", func(t *testing.T) {
		borrowedError := errors.New("borrowed handler failure")
		capture := &slogHandlerCapture{enabled: false, handleError: borrowedError}
		bound := []slog.Attr{slog.String("bound", "exact")}
		handler := vvotel.TraceHandler(capturingSlogHandler{capture: capture}).
			WithAttrs(bound).
			WithGroup("request")
		_, ctx := slogTraceContext(0)

		if handler.Enabled(ctx, slog.LevelWarn) {
			t.Fatal("TraceHandler changed the borrowed handler's Enabled result")
		}
		if capture.enabledContext != ctx || capture.enabledLevel != slog.LevelWarn {
			t.Fatal("TraceHandler did not pass Enabled its original context and level")
		}

		record := slog.NewRecord(time.Time{}, slog.LevelError, "failed", 0)
		record.AddAttrs(slog.Group("payload", slog.String("kept", "exact")))
		if err := handler.Handle(ctx, record); err != borrowedError {
			t.Fatalf("TraceHandler returned %v rather than the borrowed handler error", err)
		}
		if capture.handledContext != ctx {
			t.Fatal("TraceHandler did not pass Handle its original context")
		}
		assertSlogAttrs(t, capture.attrs, bound)
		if len(capture.groups) != 1 || capture.groups[0] != "request" {
			t.Fatalf("TraceHandler changed the borrowed handler groups: %v", capture.groups)
		}
	})

	t.Run("existing JSON rendering is unchanged around the appended fields", func(t *testing.T) {
		_, ctx := slogTraceContext(0)
		withoutTrace := renderTraceJSON(t, context.Background())
		withTrace := renderTraceJSON(t, ctx)
		fragment := `,"` + vvotel.LogTraceIDKey + `":"0102030405060708090a0b0c0d0e0f10","` +
			vvotel.LogSpanIDKey + `":"1112131415161718","` +
			vvotel.LogTraceFlagsKey + `":"00"`
		if !strings.Contains(withTrace, fragment) {
			t.Fatalf("the rendered record does not contain the appended triplet: %s", withTrace)
		}
		if got := strings.Replace(withTrace, fragment, "", 1); got != withoutTrace {
			t.Fatalf("existing JSON changed around correlation fields\nwithout: %s\nwith:    %s", withoutTrace, withTrace)
		}
	})
}

func slogTraceContext(flags trace.TraceFlags) (trace.SpanContext, context.Context) {
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{17, 18, 19, 20, 21, 22, 23, 24},
		TraceFlags: flags,
	})
	return spanContext, trace.ContextWithSpanContext(context.Background(), spanContext)
}

func slogRecordAttrs(record slog.Record) []slog.Attr {
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	return attrs
}

func assertSlogAttrs(t *testing.T, got, want []slog.Attr) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("received %d attributes, want %d: %v", len(got), len(want), got)
	}
	for index := range want {
		if !got[index].Equal(want[index]) {
			t.Fatalf("attribute %d is %v, want %v", index, got[index], want[index])
		}
	}
}

func assertSlogGroups(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("received groups %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("group %d is %q, want %q", index, got[index], want[index])
		}
	}
}

func slogRecordOwnsCorrelation(record slog.Record) bool {
	owned := false
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == vvotel.LogTraceIDKey || attr.Key == vvotel.LogSpanIDKey || attr.Key == vvotel.LogTraceFlagsKey {
			owned = true
			return false
		}
		return true
	})
	return owned
}

func renderTraceJSON(t *testing.T, ctx context.Context) string {
	t.Helper()
	var output bytes.Buffer
	handler := vvotel.TraceHandler(slog.NewJSONHandler(&output, nil)).
		WithAttrs([]slog.Attr{slog.String("bound", "exact")}).
		WithGroup("request")
	record := slog.NewRecord(time.Time{}, slog.LevelError, "failed", 0)
	record.AddAttrs(
		slog.Group("nested", slog.String("kept", "exact")),
		slog.Int("count", 2),
	)
	if err := handler.Handle(ctx, record); err != nil {
		t.Fatalf("the JSON handler rejected a record: %v", err)
	}
	return output.String()
}
