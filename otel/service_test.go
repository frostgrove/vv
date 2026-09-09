package vvotel_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
	"go.opentelemetry.io/otel/codes"
)

type dummyModel struct {
	ID string
}

type fakeBusinessPanic struct {
	operation string
}

type fakeEffectError struct {
	operation string
}

func (e *fakeEffectError) Error() string { return e.operation + " failed" }

const (
	fakeCountResult       int64 = 101
	fakeGetResultID             = "get-result"
	fakeCreateResultID          = "create-result"
	fakeUpdateResultID          = "update-result"
	fakeReplaceResultID         = "replace-result"
	fakeDeleteResult      int64 = 107
	fakeDeleteManyResult  int64 = 109
	fakeRestoreResult     int64 = 113
	fakeRestoreManyResult int64 = 127
)

func fakeListResult() crud.PaginatedResponse[dummyModel] {
	return crud.PaginatedResponse[dummyModel]{
		Items:      []dummyModel{{ID: "list-result-a"}, {ID: "list-result-b"}},
		Page:       3,
		Limit:      2,
		Total:      17,
		TotalPages: 9,
		HasNext:    true,
		HasPrev:    true,
		NextCursor: "list-next",
		PrevCursor: "list-prev",
	}
}

type fakePortService struct {
	lastCtx            context.Context
	calls              int
	operationCalls     map[string]int
	panicValue         any
	goexit             bool
	panicNil           bool
	err                error
	meta               *crud.Meta
	paths              errs.Resolver
	metaCalls          int
	pathsCalls         int
	listCommand        port.ListCommand
	countCommand       port.CountCommand
	getCommand         port.GetCommand[string]
	createCommand      port.CreateCommand[dummyModel]
	updateCommand      port.UpdateCommand[string, dummyModel]
	replaceCommand     port.ReplaceCommand[string, dummyModel]
	deleteCommand      port.DeleteCommand[string]
	deleteManyCommand  port.BulkDeleteCommand[string]
	restoreCommand     port.RestoreCommand[string]
	restoreManyCommand port.BulkRestoreCommand[string]
}

type dummyResolver struct{}

func (dummyResolver) Resolve(path errs.Path) (errs.Path, bool) { return path, true }

func (f *fakePortService) Meta() *crud.Meta {
	f.metaCalls++
	if f.meta == nil {
		f.meta = &crud.Meta{}
	}
	return f.meta
}

func (f *fakePortService) Paths() errs.Resolver {
	f.pathsCalls++
	if f.paths == nil {
		f.paths = &dummyResolver{}
	}
	return f.paths
}

func (f *fakePortService) call(ctx context.Context, operation string) {
	f.lastCtx = ctx
	f.calls++
	if f.operationCalls == nil {
		f.operationCalls = make(map[string]int)
	}
	f.operationCalls[operation]++
	if f.panicValue != nil {
		panic(f.panicValue)
	}
	if f.goexit {
		runtime.Goexit()
	}
	if f.panicNil {
		panic(nil)
	}
}

func (f *fakePortService) List(ctx context.Context, cmd port.ListCommand) (crud.PaginatedResponse[dummyModel], error) {
	f.listCommand = cmd
	f.call(ctx, vvotel.OpCommandList)
	return fakeListResult(), f.err
}
func (f *fakePortService) Count(ctx context.Context, cmd port.CountCommand) (int64, error) {
	f.countCommand = cmd
	f.call(ctx, vvotel.OpCommandCount)
	return fakeCountResult, f.err
}
func (f *fakePortService) Get(ctx context.Context, cmd port.GetCommand[string]) (dummyModel, error) {
	f.getCommand = cmd
	f.call(ctx, vvotel.OpCommandGet)
	return dummyModel{ID: fakeGetResultID}, f.err
}

func TestService_GoexitPreservesGoroutineTermination(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	raw := &fakePortService{goexit: true}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)
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
	if raw.calls != 1 || raw.operationCalls[vvotel.OpCommandGet] != 1 || len(raw.operationCalls) != 1 {
		t.Fatalf("business calls=%d by_operation=%v, want Get once", raw.calls, raw.operationCalls)
	}
	if len(tp.spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(tp.spans))
	}
	span := tp.spans[0]
	if !span.ended {
		t.Fatal("Goexit span was not ended")
	}
	if span.status != codes.Error {
		t.Fatalf("got status %v, want Error", span.status)
	}
	if got := span.attributes[vvotel.AttrOperationOutcome].AsString(); got != vvotel.OutcomeGoroutineExit {
		t.Fatalf("got outcome %q, want %q", got, vvotel.OutcomeGoroutineExit)
	}
	if _, ok := span.attributes[vvotel.AttrErrorType]; ok {
		t.Fatal("Goexit span must not contain error.type")
	}
	if mp.metricCount() != 0 {
		t.Fatalf("Goexit recorded %d metrics, want 0", mp.metricCount())
	}
	if tp.endCalls != 1 {
		t.Fatalf("End called %d times, want 1", tp.endCalls)
	}
}

func TestService_PanicNilIsNotSuppressed(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	raw := &fakePortService{panicNil: true}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)
	returned := false
	func() {
		defer func() { _ = recover() }()
		_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
		returned = true
	}()
	if returned {
		t.Fatal("panic(nil) was suppressed")
	}
	if raw.calls != 1 || raw.operationCalls[vvotel.OpCommandGet] != 1 || len(raw.operationCalls) != 1 {
		t.Fatalf("business calls=%d by_operation=%v, want Get once", raw.calls, raw.operationCalls)
	}
	if len(tp.spans) != 1 || tp.endCalls != 1 || !tp.spans[0].ended {
		t.Fatal("panic(nil) span was not ended")
	}
	if tp.spans[0].status != codes.Error {
		t.Fatalf("panic(nil) span status=%v, want Error", tp.spans[0].status)
	}
	if got := tp.spans[0].attributes[vvotel.AttrErrorType].AsString(); got != vvotel.ErrorTypePanic {
		t.Fatalf("got error.type %q, want %q", got, vvotel.ErrorTypePanic)
	}
	if mp.metricCount() != 1 || mp.histogramRecordCalls != 1 {
		t.Fatalf("panic(nil) metrics=%d record_calls=%d, want one each", mp.metricCount(), mp.histogramRecordCalls)
	}
}

type panickingClassificationError struct{}

func (*panickingClassificationError) Error() string {
	return "classified error"
}

func (*panickingClassificationError) Is(error) bool {
	panic("hostile Is")
}

func TestService_ClassificationPanicDoesNotReplaceBusinessError(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	want := &panickingClassificationError{}
	raw := &fakePortService{err: want}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)
	_, got := svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	if got != want {
		t.Fatalf("business error identity changed: got %v, want same value", got)
	}
	if raw.calls != 1 {
		t.Fatalf("business operation called %d times, want 1", raw.calls)
	}
	if len(tp.spans) != 1 || !tp.spans[0].ended {
		t.Fatal("classification panic suppressed span completion")
	}
	if gotType := tp.spans[0].attributes[vvotel.AttrErrorType].AsString(); gotType != vvotel.ErrorTypeInternal {
		t.Fatalf("got error.type %q, want %q", gotType, vvotel.ErrorTypeInternal)
	}
	if mp.metricCount() != 1 {
		t.Fatalf("classification panic recorded %d metrics, want 1", mp.metricCount())
	}
}
func (f *fakePortService) Create(ctx context.Context, cmd port.CreateCommand[dummyModel]) (dummyModel, error) {
	f.createCommand = cmd
	f.call(ctx, vvotel.OpCommandCreate)
	return dummyModel{ID: fakeCreateResultID}, f.err
}
func (f *fakePortService) Update(ctx context.Context, cmd port.UpdateCommand[string, dummyModel]) (dummyModel, error) {
	f.updateCommand = cmd
	f.call(ctx, vvotel.OpCommandUpdate)
	return dummyModel{ID: fakeUpdateResultID}, f.err
}
func (f *fakePortService) Replace(ctx context.Context, cmd port.ReplaceCommand[string, dummyModel]) (dummyModel, error) {
	f.replaceCommand = cmd
	f.call(ctx, vvotel.OpCommandReplace)
	return dummyModel{ID: fakeReplaceResultID}, f.err
}
func (f *fakePortService) Delete(ctx context.Context, cmd port.DeleteCommand[string]) (int64, error) {
	f.deleteCommand = cmd
	f.call(ctx, vvotel.OpCommandDelete)
	return fakeDeleteResult, f.err
}
func (f *fakePortService) DeleteMany(ctx context.Context, cmd port.BulkDeleteCommand[string]) (int64, error) {
	f.deleteManyCommand = cmd
	f.call(ctx, vvotel.OpCommandDeleteMany)
	return fakeDeleteManyResult, f.err
}

type fakeRestorablePortService struct {
	fakePortService
}

func (f *fakeRestorablePortService) Restore(ctx context.Context, cmd port.RestoreCommand[string]) (int64, error) {
	f.restoreCommand = cmd
	f.call(ctx, vvotel.OpCommandRestore)
	return fakeRestoreResult, f.err
}
func (f *fakeRestorablePortService) RestoreMany(ctx context.Context, cmd port.BulkRestoreCommand[string]) (int64, error) {
	f.restoreManyCommand = cmd
	f.call(ctx, vvotel.OpCommandRestoreMany)
	return fakeRestoreManyResult, f.err
}

func TestWrapServiceInfersTypesAndAppliesTheTypedMiddlewareOnce(t *testing.T) {
	raw := &fakeRestorablePortService{}
	wrapped := vvotel.WrapService(nil, raw)

	command := port.GetCommand[string]{ID: "inferred-id"}
	model, err := wrapped.Get(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != fakeGetResultID || !reflect.DeepEqual(raw.getCommand, command) {
		t.Fatalf("Get result=%+v command=%+v, want result %q and exact command %+v", model, raw.getCommand, fakeGetResultID, command)
	}
	if raw.calls != 1 || raw.operationCalls[vvotel.OpCommandGet] != 1 {
		t.Fatalf("Get calls=%d by_operation=%v, want one", raw.calls, raw.operationCalls)
	}

	restorable, ok := port.RestorableOf[string](wrapped)
	if !ok {
		t.Fatal("inference-friendly wrapper lost RestorableService")
	}
	restore := port.RestoreCommand[string]{ID: "restore-id"}
	result, err := restorable.Restore(t.Context(), restore)
	if err != nil {
		t.Fatal(err)
	}
	if result != fakeRestoreResult || raw.restoreCommand != restore {
		t.Fatalf("Restore result=%d command=%+v, want result %d and exact command %+v", result, raw.restoreCommand, fakeRestoreResult, restore)
	}
	if raw.calls != 2 || raw.operationCalls[vvotel.OpCommandRestore] != 1 {
		t.Fatalf("total calls=%d by_operation=%v, want Get and Restore once each", raw.calls, raw.operationCalls)
	}
}

type serviceCommandFixture struct {
	list               port.ListCommand
	count              port.CountCommand
	get                port.GetCommand[string]
	create             port.CreateCommand[dummyModel]
	update             port.UpdateCommand[string, dummyModel]
	replace            port.ReplaceCommand[string, dummyModel]
	delete             port.DeleteCommand[string]
	deleteMany         port.BulkDeleteCommand[string]
	restore            port.RestoreCommand[string]
	restoreMany        port.BulkRestoreCommand[string]
	createBeforeError  error
	updateBeforeError  error
	replaceBeforeError error
}

func newServiceCommandFixture() serviceCommandFixture {
	createBeforeError := &fakeEffectError{operation: "create before"}
	updateBeforeError := &fakeEffectError{operation: "update before"}
	replaceBeforeError := &fakeEffectError{operation: "replace before"}
	return serviceCommandFixture{
		list: port.ListCommand{
			Query:   &query.Request{Page: 11, Limit: 13, Search: "list-query"},
			Options: []crud.Option{crud.Page(17), crud.Limit(19), crud.After("list-after")},
		},
		count: port.CountCommand{
			Query:   &query.Request{Offset: 23, Search: "count-query", Distinct: true},
			Options: []crud.Option{crud.Offset(29), crud.Distinct()},
		},
		get: port.GetCommand[string]{
			ID:      "get-input",
			Query:   &query.Request{Select: query.Strings{"id", "name"}, Search: "get-query"},
			Options: []crud.Option{crud.Select("id", "name"), crud.ForUpdate()},
		},
		create: port.CreateCommand[dummyModel]{
			Model: dummyModel{ID: "create-input"},
			Before: func(model *dummyModel) error {
				model.ID = "create-before-effect"
				return createBeforeError
			},
		},
		update: port.UpdateCommand[string, dummyModel]{
			ID:    "update-input-id",
			Patch: dummyModel{ID: "update-input-patch"},
			Before: func(model *dummyModel) error {
				model.ID = "update-before-effect"
				return updateBeforeError
			},
		},
		replace: port.ReplaceCommand[string, dummyModel]{
			ID:    "replace-input-id",
			Model: dummyModel{ID: "replace-input-model"},
			Before: func(model *dummyModel) error {
				model.ID = "replace-before-effect"
				return replaceBeforeError
			},
		},
		delete:             port.DeleteCommand[string]{ID: "delete-input"},
		deleteMany:         port.BulkDeleteCommand[string]{IDs: []string{"delete-many-a", "delete-many-b", "delete-many-c"}},
		restore:            port.RestoreCommand[string]{ID: "restore-input"},
		restoreMany:        port.BulkRestoreCommand[string]{IDs: []string{"restore-many-a", "restore-many-b", "restore-many-c", "restore-many-d"}},
		createBeforeError:  createBeforeError,
		updateBeforeError:  updateBeforeError,
		replaceBeforeError: replaceBeforeError,
	}
}

func (f serviceCommandFixture) operations() []string {
	return []string{
		vvotel.OpCommandList,
		vvotel.OpCommandCount,
		vvotel.OpCommandGet,
		vvotel.OpCommandCreate,
		vvotel.OpCommandUpdate,
		vvotel.OpCommandReplace,
		vvotel.OpCommandDelete,
		vvotel.OpCommandDeleteMany,
		vvotel.OpCommandRestore,
		vvotel.OpCommandRestoreMany,
	}
}

func (f serviceCommandFixture) execute(ctx context.Context, operation string, svc port.Service[dummyModel, string, dummyModel]) (any, error) {
	switch operation {
	case vvotel.OpCommandList:
		return svc.List(ctx, f.list)
	case vvotel.OpCommandCount:
		return svc.Count(ctx, f.count)
	case vvotel.OpCommandGet:
		return svc.Get(ctx, f.get)
	case vvotel.OpCommandCreate:
		return svc.Create(ctx, f.create)
	case vvotel.OpCommandUpdate:
		return svc.Update(ctx, f.update)
	case vvotel.OpCommandReplace:
		return svc.Replace(ctx, f.replace)
	case vvotel.OpCommandDelete:
		return svc.Delete(ctx, f.delete)
	case vvotel.OpCommandDeleteMany:
		return svc.DeleteMany(ctx, f.deleteMany)
	case vvotel.OpCommandRestore:
		restorable, ok := port.RestorableOf[string](svc)
		if !ok {
			return nil, errors.New("Restore capability was erased")
		}
		return restorable.Restore(ctx, f.restore)
	case vvotel.OpCommandRestoreMany:
		restorable, ok := port.RestorableOf[string](svc)
		if !ok {
			return nil, errors.New("RestoreMany capability was erased")
		}
		return restorable.RestoreMany(ctx, f.restoreMany)
	default:
		return nil, fmt.Errorf("unknown command operation %q", operation)
	}
}

func serviceExpectedResult(operation string) any {
	switch operation {
	case vvotel.OpCommandList:
		return fakeListResult()
	case vvotel.OpCommandCount:
		return fakeCountResult
	case vvotel.OpCommandGet:
		return dummyModel{ID: fakeGetResultID}
	case vvotel.OpCommandCreate:
		return dummyModel{ID: fakeCreateResultID}
	case vvotel.OpCommandUpdate:
		return dummyModel{ID: fakeUpdateResultID}
	case vvotel.OpCommandReplace:
		return dummyModel{ID: fakeReplaceResultID}
	case vvotel.OpCommandDelete:
		return fakeDeleteResult
	case vvotel.OpCommandDeleteMany:
		return fakeDeleteManyResult
	case vvotel.OpCommandRestore:
		return fakeRestoreResult
	case vvotel.OpCommandRestoreMany:
		return fakeRestoreManyResult
	default:
		panic("unknown command operation: " + operation)
	}
}

func assertServiceOptionsPreserved(t *testing.T, got, want []crud.Option) {
	t.Helper()
	if len(got) != len(want) || (len(want) > 0 && &got[0] != &want[0]) || !reflect.DeepEqual(crud.Build(got...), crud.Build(want...)) {
		t.Fatal("service command options changed identity, order or effect")
	}
}

func assertServiceBeforePreserved(t *testing.T, before func(*dummyModel) error, wantError error, wantID string) {
	t.Helper()
	if before == nil {
		t.Fatal("service command Before hook was erased")
	}
	probe := dummyModel{ID: "before-probe"}
	if got := before(&probe); got != wantError || probe.ID != wantID {
		t.Fatalf("service command Before hook changed: result=%v model=%+v", got, probe)
	}
}

func assertStringSlicePreserved(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) || (len(want) > 0 && &got[0] != &want[0]) {
		t.Fatalf("service command IDs changed identity or value: got=%v want=%v", got, want)
	}
}

func (f serviceCommandFixture) assertCaptured(t *testing.T, raw *fakeRestorablePortService, operation string) {
	t.Helper()
	switch operation {
	case vvotel.OpCommandList:
		if raw.listCommand.Query != f.list.Query {
			t.Fatal("List query identity changed")
		}
		assertServiceOptionsPreserved(t, raw.listCommand.Options, f.list.Options)
	case vvotel.OpCommandCount:
		if raw.countCommand.Query != f.count.Query {
			t.Fatal("Count query identity changed")
		}
		assertServiceOptionsPreserved(t, raw.countCommand.Options, f.count.Options)
	case vvotel.OpCommandGet:
		if raw.getCommand.ID != f.get.ID || raw.getCommand.Query != f.get.Query {
			t.Fatalf("Get command changed: %+v", raw.getCommand)
		}
		assertServiceOptionsPreserved(t, raw.getCommand.Options, f.get.Options)
	case vvotel.OpCommandCreate:
		if raw.createCommand.Model != f.create.Model {
			t.Fatalf("Create command model changed: %+v", raw.createCommand.Model)
		}
		assertServiceBeforePreserved(t, raw.createCommand.Before, f.createBeforeError, "create-before-effect")
	case vvotel.OpCommandUpdate:
		if raw.updateCommand.ID != f.update.ID || raw.updateCommand.Patch != f.update.Patch {
			t.Fatalf("Update command changed: %+v", raw.updateCommand)
		}
		assertServiceBeforePreserved(t, raw.updateCommand.Before, f.updateBeforeError, "update-before-effect")
	case vvotel.OpCommandReplace:
		if raw.replaceCommand.ID != f.replace.ID || raw.replaceCommand.Model != f.replace.Model {
			t.Fatalf("Replace command changed: %+v", raw.replaceCommand)
		}
		assertServiceBeforePreserved(t, raw.replaceCommand.Before, f.replaceBeforeError, "replace-before-effect")
	case vvotel.OpCommandDelete:
		if raw.deleteCommand != f.delete {
			t.Fatalf("Delete command changed: %+v", raw.deleteCommand)
		}
	case vvotel.OpCommandDeleteMany:
		assertStringSlicePreserved(t, raw.deleteManyCommand.IDs, f.deleteMany.IDs)
	case vvotel.OpCommandRestore:
		if raw.restoreCommand != f.restore {
			t.Fatalf("Restore command changed: %+v", raw.restoreCommand)
		}
	case vvotel.OpCommandRestoreMany:
		assertStringSlicePreserved(t, raw.restoreManyCommand.IDs, f.restoreMany.IDs)
	default:
		t.Fatalf("unknown command operation %q", operation)
	}
}

func TestService_AllTenCommandsPreserveOneCallAcrossOutcomes(t *testing.T) {
	fixture := newServiceCommandFixture()
	businessError := errors.New("business error")
	businessPanic := &fakeBusinessPanic{operation: "command"}
	terminals := []struct {
		name          string
		err           error
		panicValue    any
		wantOutcome   string
		wantErrorType string
	}{
		{name: "success", wantOutcome: vvotel.OutcomeOk},
		{name: "error", err: businessError, wantOutcome: vvotel.OutcomeError, wantErrorType: vvotel.ErrorTypeInternal},
		{name: "canceled", err: context.Canceled, wantOutcome: vvotel.OutcomeCanceled, wantErrorType: vvotel.ErrorTypeCanceled},
		{name: "deadline", err: context.DeadlineExceeded, wantOutcome: vvotel.OutcomeTimeout, wantErrorType: vvotel.ErrorTypeTimeout},
		{name: "panic", panicValue: businessPanic, wantOutcome: vvotel.OutcomeError, wantErrorType: vvotel.ErrorTypePanic},
	}

	for _, operation := range fixture.operations() {
		for _, terminal := range terminals {
			t.Run(operation+"/"+terminal.name, func(t *testing.T) {
				tp := newTestTracerProvider()
				mp := newTestMeterProvider()
				tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp, ResourceName: "item"})
				if err != nil {
					t.Fatal(err)
				}

				raw := &fakeRestorablePortService{}
				raw.err = terminal.err
				raw.panicValue = terminal.panicValue
				svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)
				var result any
				var callErr error
				var panicValue any
				returned := false
				func() {
					defer func() { panicValue = recover() }()
					result, callErr = fixture.execute(context.Background(), operation, svc)
					returned = true
				}()

				if terminal.panicValue != nil {
					if returned || panicValue != terminal.panicValue {
						t.Fatalf("business panic changed or was suppressed: returned=%t panic=%v", returned, panicValue)
					}
				} else if !returned {
					t.Fatalf("unexpected panic: %v", panicValue)
				} else if callErr != terminal.err {
					t.Fatalf("business error identity changed: got %v, want %v", callErr, terminal.err)
				}
				if terminal.panicValue == nil && !reflect.DeepEqual(result, serviceExpectedResult(operation)) {
					t.Fatalf("business result changed: got=%#v want=%#v", result, serviceExpectedResult(operation))
				}
				if raw.calls != 1 || raw.operationCalls[operation] != 1 || len(raw.operationCalls) != 1 {
					t.Fatalf("business calls=%d by_operation=%v, want only %s once", raw.calls, raw.operationCalls, operation)
				}
				fixture.assertCaptured(t, raw, operation)
				if raw.lastCtx.Value(spanKey{}) == nil {
					t.Fatal("derived context not passed to the business operation")
				}
				if len(tp.spans) != 1 || tp.endCalls != 1 {
					t.Fatalf("spans=%d end_calls=%d, want one each", len(tp.spans), tp.endCalls)
				}
				span := tp.spans[0]
				if !span.ended || span.name != vvotel.CommandSpanName(operation) {
					t.Fatalf("command span differs: name=%q ended=%t", span.name, span.ended)
				}
				wantStatus := codes.Unset
				if terminal.wantErrorType != "" {
					wantStatus = codes.Error
				}
				if span.status != wantStatus || span.attributes[vvotel.AttrOperationOutcome].AsString() != terminal.wantOutcome {
					t.Fatalf("span terminal differs: status=%v attributes=%v", span.status, span.attributes)
				}
				if got := span.attributes[vvotel.AttrErrorType].AsString(); got != terminal.wantErrorType {
					t.Fatalf("span error.type=%q, want %q", got, terminal.wantErrorType)
				}
				if span.attributes[vvotel.AttrResourceName].AsString() != "item" {
					t.Fatal("approved resource name was not preserved on the span")
				}
				if len(mp.metrics) != 1 || mp.histogramRecordCalls != 1 {
					t.Fatalf("metrics=%d record_calls=%d, want one each", len(mp.metrics), mp.histogramRecordCalls)
				}
				metric := mp.metrics[0]
				if metric.name != vvotel.MetricCommandDuration || metric.attributes[vvotel.AttrOperationName].AsString() != operation || metric.attributes[vvotel.AttrOperationOutcome].AsString() != terminal.wantOutcome {
					t.Fatalf("command metric differs: %+v", metric)
				}
				if got := metric.attributes[vvotel.AttrErrorType].AsString(); got != terminal.wantErrorType {
					t.Fatalf("metric error.type=%q, want %q", got, terminal.wantErrorType)
				}
				if metric.context != raw.lastCtx {
					t.Fatal("histogram did not receive the business operation context")
				}
				assertCurrentSpanConforms(t, "command_span", span)
				assertCurrentMetricConforms(t, "command_duration", metric)
			})
		}
	}
}

func TestService_RestorableTotality(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})

	fixture := newServiceCommandFixture()
	raw := &fakeRestorablePortService{}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)

	restorable, ok := port.RestorableOf[string](svc)
	if !ok {
		t.Fatal("expected RestorableOf to succeed")
	}

	n, err := restorable.Restore(context.Background(), fixture.restore)
	if err != nil || n != fakeRestoreResult {
		t.Fatalf("Restore failed: n=%d err=%v", n, err)
	}
	fixture.assertCaptured(t, raw, vvotel.OpCommandRestore)
	if raw.calls != 1 || raw.operationCalls[vvotel.OpCommandRestore] != 1 || len(raw.operationCalls) != 1 {
		t.Fatalf("Restore calls=%d by_operation=%v, want Restore once", raw.calls, raw.operationCalls)
	}
	if len(tp.spans) != 1 || tp.spans[0].name != "vv.command restore" {
		t.Errorf("expected span vv.command restore, got %v", tp.spans[0].name)
	}

	nm, err := restorable.RestoreMany(context.Background(), fixture.restoreMany)
	if err != nil || nm != fakeRestoreManyResult {
		t.Fatalf("RestoreMany failed: nm=%d err=%v", nm, err)
	}
	fixture.assertCaptured(t, raw, vvotel.OpCommandRestoreMany)
	if raw.calls != 2 || raw.operationCalls[vvotel.OpCommandRestore] != 1 || raw.operationCalls[vvotel.OpCommandRestoreMany] != 1 || len(raw.operationCalls) != 2 {
		t.Fatalf("restorable calls=%d by_operation=%v, want each operation once", raw.calls, raw.operationCalls)
	}
	if len(tp.spans) != 2 || tp.spans[1].name != "vv.command restore_many" {
		t.Errorf("expected span vv.command restore_many, got %v", tp.spans[1].name)
	}
	if tp.endCalls != 2 || mp.histogramRecordCalls != 2 || mp.metricCount() != 2 {
		t.Fatalf("restorable telemetry end=%d record=%d metrics=%d, want two each", tp.endCalls, mp.histogramRecordCalls, mp.metricCount())
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

	wantMeta := &crud.Meta{}
	wantPaths := &dummyResolver{}
	raw := &fakePortService{meta: wantMeta, paths: wantPaths}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)

	if meta := svc.Meta(); meta != wantMeta || raw.metaCalls != 1 {
		t.Fatalf("Meta identity/calls changed: got=%p want=%p calls=%d", meta, wantMeta, raw.metaCalls)
	}
	if paths := svc.Paths(); paths != wantPaths || raw.pathsCalls != 1 {
		t.Fatalf("Paths identity/calls changed: got=%T/%p want=%p calls=%d", paths, paths, wantPaths, raw.pathsCalls)
	}
	if len(tp.spans) != 0 || mp.metricCount() != 0 {
		t.Fatalf("Meta and Paths emitted spans=%d metrics=%d", len(tp.spans), mp.metricCount())
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
	if err != nil || res.ID != fakeGetResultID {
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
	if err != nil || res.ID != fakeGetResultID {
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
			assertCurrentSpanConforms(t, "command_span", s)
			assertCurrentMetricConforms(t, "command_duration", m)
		})
	}
}

func TestService_PanicsEndedSafelyAndRepanicked(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp, ResourceName: "panic_res"})

	want := &fakeBusinessPanic{operation: "service get"}
	raw := &fakePortService{panicValue: want}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)

	defer func() {
		r := recover()
		if r != want {
			t.Fatalf("business panic identity changed: got %v, want %p", r, want)
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
		assertCurrentSpanConforms(t, "command_span", s)
		assertCurrentMetricConforms(t, "command_duration", mp.metrics[0])
		m := mp.metrics[0]
		if m.attributes[vvotel.AttrErrorType].AsString() != vvotel.ErrorTypePanic {
			t.Errorf("metric error.type %q, want panic", m.attributes[vvotel.AttrErrorType].AsString())
		}
	}()

	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "1"})
}
