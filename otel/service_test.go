package vvotel_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
	"go.opentelemetry.io/otel/codes"
)

type dummyModel struct {
	ID string
}

type fakePortService struct {
	lastCtx  context.Context
	panic    bool
	goexit   bool
	panicNil bool
	err      error
}

type dummyResolver struct{}

func (dummyResolver) Resolve(path errs.Path) (errs.Path, bool) { return path, true }

func (f *fakePortService) Meta() *crud.Meta     { return &crud.Meta{} }
func (f *fakePortService) Paths() errs.Resolver { return dummyResolver{} }
func (f *fakePortService) List(ctx context.Context, cmd port.ListCommand) (crud.PaginatedResponse[dummyModel], error) {
	f.lastCtx = ctx
	if f.err != nil {
		return crud.PaginatedResponse[dummyModel]{}, f.err
	}
	return crud.PaginatedResponse[dummyModel]{Items: []dummyModel{{ID: "1"}}, Total: 1}, nil
}
func (f *fakePortService) Count(ctx context.Context, cmd port.CountCommand) (int64, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return 0, f.err
	}
	return 42, nil
}
func (f *fakePortService) Get(ctx context.Context, cmd port.GetCommand[string]) (dummyModel, error) {
	f.lastCtx = ctx
	if f.panic {
		panic("database crashed")
	}
	if f.goexit {
		runtime.Goexit()
	}
	if f.panicNil {
		panic(nil)
	}
	if f.err != nil {
		return dummyModel{}, f.err
	}
	return dummyModel{ID: cmd.ID}, nil
}

func TestService_GoexitPreservesGoroutineTermination(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{goexit: true})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime.Goexit did not terminate the goroutine")
	}
}

func TestService_PanicNilIsNotSuppressed(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{panicNil: true})
	defer func() {
		if recover() == nil {
			t.Fatal("panic(nil) was suppressed")
		}
	}()
	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
}
func (f *fakePortService) Create(ctx context.Context, cmd port.CreateCommand[dummyModel]) (dummyModel, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return dummyModel{}, f.err
	}
	return cmd.Model, nil
}
func (f *fakePortService) Update(ctx context.Context, cmd port.UpdateCommand[string, dummyModel]) (dummyModel, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return dummyModel{}, f.err
	}
	return dummyModel{ID: cmd.ID}, nil
}
func (f *fakePortService) Replace(ctx context.Context, cmd port.ReplaceCommand[string, dummyModel]) (dummyModel, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return dummyModel{}, f.err
	}
	return dummyModel{ID: cmd.ID}, nil
}
func (f *fakePortService) Delete(ctx context.Context, cmd port.DeleteCommand[string]) (int64, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return 0, f.err
	}
	return 1, nil
}
func (f *fakePortService) DeleteMany(ctx context.Context, cmd port.BulkDeleteCommand[string]) (int64, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return 0, f.err
	}
	return int64(len(cmd.IDs)), nil
}

type fakeRestorablePortService struct {
	fakePortService
}

func (f *fakeRestorablePortService) Restore(ctx context.Context, cmd port.RestoreCommand[string]) (int64, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return 0, f.err
	}
	return 1, nil
}
func (f *fakeRestorablePortService) RestoreMany(ctx context.Context, cmd port.BulkRestoreCommand[string]) (int64, error) {
	f.lastCtx = ctx
	if f.err != nil {
		return 0, f.err
	}
	return int64(len(cmd.IDs)), nil
}

func TestService_AllEightOperationsTotality(t *testing.T) {
	ops := []struct {
		name     string
		execute  func(ctx context.Context, svc port.Service[dummyModel, string, dummyModel]) error
		wantSpan string
	}{
		{
			name: "List",
			execute: func(ctx context.Context, svc port.Service[dummyModel, string, dummyModel]) error {
				_, err := svc.List(ctx, port.ListCommand{})
				return err
			},
			wantSpan: "vv.command list",
		},
		{
			name: "Count",
			execute: func(ctx context.Context, svc port.Service[dummyModel, string, dummyModel]) error {
				_, err := svc.Count(ctx, port.CountCommand{})
				return err
			},
			wantSpan: "vv.command count",
		},
		{
			name: "Get",
			execute: func(ctx context.Context, svc port.Service[dummyModel, string, dummyModel]) error {
				_, err := svc.Get(ctx, port.GetCommand[string]{ID: "1"})
				return err
			},
			wantSpan: "vv.command get",
		},
		{
			name: "Create",
			execute: func(ctx context.Context, svc port.Service[dummyModel, string, dummyModel]) error {
				_, err := svc.Create(ctx, port.CreateCommand[dummyModel]{Model: dummyModel{ID: "1"}})
				return err
			},
			wantSpan: "vv.command create",
		},
		{
			name: "Update",
			execute: func(ctx context.Context, svc port.Service[dummyModel, string, dummyModel]) error {
				_, err := svc.Update(ctx, port.UpdateCommand[string, dummyModel]{ID: "1"})
				return err
			},
			wantSpan: "vv.command update",
		},
		{
			name: "Replace",
			execute: func(ctx context.Context, svc port.Service[dummyModel, string, dummyModel]) error {
				_, err := svc.Replace(ctx, port.ReplaceCommand[string, dummyModel]{ID: "1"})
				return err
			},
			wantSpan: "vv.command replace",
		},
		{
			name: "Delete",
			execute: func(ctx context.Context, svc port.Service[dummyModel, string, dummyModel]) error {
				_, err := svc.Delete(ctx, port.DeleteCommand[string]{ID: "1"})
				return err
			},
			wantSpan: "vv.command delete",
		},
		{
			name: "DeleteMany",
			execute: func(ctx context.Context, svc port.Service[dummyModel, string, dummyModel]) error {
				_, err := svc.DeleteMany(ctx, port.BulkDeleteCommand[string]{IDs: []string{"1", "2"}})
				return err
			},
			wantSpan: "vv.command delete_many",
		},
	}

	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			tel, err := vvotel.New(vvotel.Config{
				TracerProvider: tp,
				MeterProvider:  mp,
				ResourceName:   "item",
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			raw := &fakePortService{}
			svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)

			if err := op.execute(context.Background(), svc); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if raw.lastCtx.Value(spanKey{}) == nil {
				t.Fatal("derived context not passed down")
			}

			if len(tp.spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(tp.spans))
			}
			s := tp.spans[0]
			if s.name != op.wantSpan {
				t.Errorf("got span %q, want %q", s.name, op.wantSpan)
			}
			if !s.ended {
				t.Error("span must be ended")
			}
			if s.status != codes.Unset {
				t.Errorf("expected status Unset, got %v", s.status)
			}
			if outcome := s.attributes[vvotel.AttrOperationOutcome].AsString(); outcome != vvotel.OutcomeOk {
				t.Errorf("got outcome %q, want %q", outcome, vvotel.OutcomeOk)
			}
			if res := s.attributes[vvotel.AttrResourceName].AsString(); res != "item" {
				t.Errorf("got resource %q, want item", res)
			}

			if len(mp.metrics) != 1 {
				t.Fatalf("expected 1 metric recording, got %d", len(mp.metrics))
			}
			if mp.metrics[0].name != vvotel.MetricCommandDuration {
				t.Errorf("got metric %q, want %q", mp.metrics[0].name, vvotel.MetricCommandDuration)
			}
		})
	}
}

func TestService_RestorableTotality(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})

	raw := &fakeRestorablePortService{}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)

	restorable, ok := port.RestorableOf[string](svc)
	if !ok {
		t.Fatal("expected RestorableOf to succeed")
	}

	n, err := restorable.Restore(context.Background(), port.RestoreCommand[string]{ID: "1"})
	if err != nil || n != 1 {
		t.Fatalf("Restore failed: n=%d err=%v", n, err)
	}
	if len(tp.spans) != 1 || tp.spans[0].name != "vv.command restore" {
		t.Errorf("expected span vv.command restore, got %v", tp.spans[0].name)
	}

	nm, err := restorable.RestoreMany(context.Background(), port.BulkRestoreCommand[string]{IDs: []string{"1", "2"}})
	if err != nil || nm != 2 {
		t.Fatalf("RestoreMany failed: nm=%d err=%v", nm, err)
	}
	if len(tp.spans) != 2 || tp.spans[1].name != "vv.command restore_many" {
		t.Errorf("expected span vv.command restore_many, got %v", tp.spans[1].name)
	}

	nonRestorable := &fakePortService{}
	svcPlain := vvotel.Service[dummyModel, string, dummyModel](tel)(nonRestorable)
	if _, ok := port.RestorableOf[string](svcPlain); ok {
		t.Fatal("expected non-restorable service to return false from RestorableOf")
	}
}

func TestService_MetaAndPathsDoNotEmitSpans(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})

	raw := &fakePortService{}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)

	if meta := svc.Meta(); meta == nil {
		t.Fatal("expected Meta() to be forwarded")
	}
	if paths := svc.Paths(); paths == nil {
		t.Fatal("expected Paths() to be forwarded")
	}
	if len(tp.spans) != 0 {
		t.Fatalf("Meta and Paths must not emit spans, got %d", len(tp.spans))
	}
}

func TestService_WithServiceResourceOption(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})

	raw := &fakePortService{}
	mw := vvotel.Service[dummyModel, string, dummyModel](tel, vvotel.WithServiceResource("override_res"))
	svc := mw(raw)

	_, _ = svc.Count(context.Background(), port.CountCommand{})

	if len(tp.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tp.spans))
	}
	if res := tp.spans[0].attributes[vvotel.AttrResourceName].AsString(); res != "override_res" {
		t.Errorf("got resource %q, want override_res", res)
	}
}

func TestService_NilAndDisabledPreservesBehavior(t *testing.T) {
	raw := &fakePortService{}
	mw := vvotel.Service[dummyModel, string, dummyModel](nil)
	if mw(nil) != nil {
		t.Fatal("expected nil wrapper when inner is nil")
	}

	svcNilTel := mw(raw)
	res, err := svcNilTel.Get(context.Background(), port.GetCommand[string]{ID: "abc"})
	if err != nil || res.ID != "abc" {
		t.Fatalf("unexpected result with nil telemetry: %v", err)
	}

	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	telDisabled, _ := vvotel.New(vvotel.Config{
		TracerProvider:         tp,
		MeterProvider:          mp,
		CommandTracesDisabled:  true,
		CommandMetricsDisabled: true,
	})
	svcDisabled := vvotel.Service[dummyModel, string, dummyModel](telDisabled)(raw)
	res, err = svcDisabled.Get(context.Background(), port.GetCommand[string]{ID: "xyz"})
	if err != nil || res.ID != "xyz" {
		t.Fatalf("unexpected result with disabled telemetry: %v", err)
	}
	if len(tp.spans) != 0 || len(mp.metrics) != 0 {
		t.Fatal("no spans or metrics should be recorded when disabled")
	}
}

func TestService_ErrorClassificationsAndErrorCode(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantOutcome string
		wantType    string
		wantCode    string
	}{
		{
			name:        "canceled",
			err:         context.Canceled,
			wantOutcome: vvotel.OutcomeCanceled,
			wantType:    vvotel.ErrorTypeCanceled,
		},
		{
			name:        "timeout",
			err:         context.DeadlineExceeded,
			wantOutcome: vvotel.OutcomeTimeout,
			wantType:    vvotel.ErrorTypeTimeout,
		},
		{
			name:        "stale_version",
			err:         &errs.Fault{Code: errs.CodeStaleVersion, Kind: errs.KindConflict},
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeStaleVersion,
			wantCode:    string(errs.CodeStaleVersion),
		},
		{
			name:        "raw_crud_stale_version",
			err:         crud.ErrStaleVersion,
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeStaleVersion,
		},
		{
			name:        "wrapped_crud_stale_version",
			err:         fmt.Errorf("write failed: %w", crud.ErrStaleVersion),
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeStaleVersion,
		},
		{
			name:        "not_found",
			err:         &errs.Fault{Code: errs.CodeNotFound, Kind: errs.KindNotFound},
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeNotFound,
			wantCode:    string(errs.CodeNotFound),
		},
		{
			name:        "forbidden",
			err:         &errs.Fault{Kind: errs.KindForbidden},
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeForbidden,
		},
		{
			name:        "unauthorized",
			err:         &errs.Fault{Kind: errs.KindUnauthorized},
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeForbidden,
		},
		{
			name:        "conflict",
			err:         &errs.Fault{Kind: errs.KindConflict},
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeConflict,
		},
		{
			name:        "validation",
			err:         &errs.Fault{Kind: errs.KindValidation},
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeInvalid,
		},
		{
			name:        "bad_request",
			err:         &errs.Fault{Kind: errs.KindBadRequest},
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeInvalid,
		},
		{
			name:        "too_large",
			err:         &errs.Fault{Kind: errs.KindTooLarge},
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeInvalid,
		},
		{
			name:        "method_not_allowed",
			err:         &errs.Fault{Kind: errs.KindMethodNotAllowed},
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeInvalid,
		},
		{
			name:        "internal",
			err:         errors.New("unexpected db error"),
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeInternal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})

			raw := &fakePortService{err: tc.err}
			svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)

			_, err := svc.Get(context.Background(), port.GetCommand[string]{ID: "1"})
			if !errors.Is(err, tc.err) {
				t.Fatalf("expected error identity preserved, got %v", err)
			}

			if len(tp.spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(tp.spans))
			}
			s := tp.spans[0]
			if outcome := s.attributes[vvotel.AttrOperationOutcome].AsString(); outcome != tc.wantOutcome {
				t.Errorf("got outcome %q, want %q", outcome, tc.wantOutcome)
			}
			if errType := s.attributes[vvotel.AttrErrorType].AsString(); errType != tc.wantType {
				t.Errorf("got error.type %q, want %q", errType, tc.wantType)
			}
			if tc.wantCode != "" {
				codeAttr, ok := s.attributes[vvotel.AttrErrorCode]
				if !ok {
					t.Fatalf("expected vv.error.code attribute on span")
				}
				if codeAttr.AsString() != tc.wantCode {
					t.Errorf("got vv.error.code %q, want %q", codeAttr.AsString(), tc.wantCode)
				}
			}

			if len(mp.metrics) != 1 {
				t.Fatalf("expected 1 metric, got %d", len(mp.metrics))
			}
			m := mp.metrics[0]
			if errType := m.attributes[vvotel.AttrErrorType].AsString(); errType != tc.wantType {
				t.Errorf("metric error.type %q, want %q", errType, tc.wantType)
			}
			if _, ok := m.attributes[vvotel.AttrErrorCode]; ok {
				t.Error("vv.error.code must NEVER be present on metrics to protect cardinality")
			}
		})
	}
}

func TestService_PanicsEndedSafelyAndRepanicked(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp, ResourceName: "panic_res"})

	raw := &fakePortService{panic: true}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)

	defer func() {
		r := recover()
		if r == nil || r != "database crashed" {
			t.Fatalf("expected re-panic of 'database crashed', got %v", r)
		}

		if len(tp.spans) != 1 {
			t.Fatalf("expected 1 span, got %d", len(tp.spans))
		}
		s := tp.spans[0]
		if !s.ended {
			t.Error("span must be ended on panic")
		}
		if s.status != codes.Error {
			t.Errorf("got status %v, want Error", s.status)
		}
		if errType := s.attributes[vvotel.AttrErrorType].AsString(); errType != vvotel.ErrorTypePanic {
			t.Errorf("got error.type %q, want panic", errType)
		}

		if len(mp.metrics) != 1 {
			t.Fatalf("expected 1 metric, got %d", len(mp.metrics))
		}
		m := mp.metrics[0]
		if m.attributes[vvotel.AttrErrorType].AsString() != vvotel.ErrorTypePanic {
			t.Errorf("metric error.type %q, want panic", m.attributes[vvotel.AttrErrorType].AsString())
		}
	}()

	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "1"})
}
