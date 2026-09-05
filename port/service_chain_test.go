package port_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type dummyModel struct {
	ID string
}

type fakeService struct {
	log *[]string
}

func (f *fakeService) Meta() *crud.Meta     { return nil }
func (f *fakeService) Paths() errs.Resolver { return nil }
func (f *fakeService) List(ctx context.Context, cmd port.ListCommand) (crud.PaginatedResponse[dummyModel], error) {
	*f.log = append(*f.log, "base.List")
	return crud.PaginatedResponse[dummyModel]{}, nil
}
func (f *fakeService) Count(ctx context.Context, cmd port.CountCommand) (int64, error) { return 0, nil }
func (f *fakeService) Get(ctx context.Context, cmd port.GetCommand[string]) (dummyModel, error) {
	return dummyModel{}, nil
}
func (f *fakeService) Create(ctx context.Context, cmd port.CreateCommand[dummyModel]) (dummyModel, error) {
	return dummyModel{}, nil
}
func (f *fakeService) Update(ctx context.Context, cmd port.UpdateCommand[string, dummyModel]) (dummyModel, error) {
	return dummyModel{}, nil
}
func (f *fakeService) Replace(ctx context.Context, cmd port.ReplaceCommand[string, dummyModel]) (dummyModel, error) {
	return dummyModel{}, nil
}
func (f *fakeService) Delete(ctx context.Context, cmd port.DeleteCommand[string]) (int64, error) {
	return 0, nil
}
func (f *fakeService) DeleteMany(ctx context.Context, cmd port.BulkDeleteCommand[string]) (int64, error) {
	return 0, nil
}

type fakeRestorableService struct {
	fakeService
}

func (f *fakeRestorableService) Restore(ctx context.Context, cmd port.RestoreCommand[string]) (int64, error) {
	*f.log = append(*f.log, "base.Restore")
	return 1, nil
}
func (f *fakeRestorableService) RestoreMany(ctx context.Context, cmd port.BulkRestoreCommand[string]) (int64, error) {
	*f.log = append(*f.log, "base.RestoreMany")
	return 1, nil
}

type wrappingService struct {
	port.Service[dummyModel, string, dummyModel]
	name string
	log  *[]string
}

func (w *wrappingService) List(ctx context.Context, cmd port.ListCommand) (crud.PaginatedResponse[dummyModel], error) {
	*w.log = append(*w.log, w.name+".enter")
	res, err := w.Service.List(ctx, cmd)
	*w.log = append(*w.log, w.name+".exit")
	return res, err
}

func (w *wrappingService) Restorable() (port.RestorableService[string], bool) {
	return port.RestorableOf[string](w.Service)
}

func TestChainService_ExecutionOrderAndNilSkipping(t *testing.T) {
	var log []string
	base := &fakeService{log: &log}

	mw1 := func(next port.Service[dummyModel, string, dummyModel]) port.Service[dummyModel, string, dummyModel] {
		return &wrappingService{Service: next, name: "mw1", log: &log}
	}
	mw2 := func(next port.Service[dummyModel, string, dummyModel]) port.Service[dummyModel, string, dummyModel] {
		return &wrappingService{Service: next, name: "mw2", log: &log}
	}

	chained := port.ChainService(base, mw1, nil, mw2)
	_, _ = chained.List(context.Background(), port.ListCommand{})

	expected := []string{"mw1.enter", "mw2.enter", "base.List", "mw2.exit", "mw1.exit"}
	if len(log) != len(expected) {
		t.Fatalf("unexpected call count: got %v, want %v", log, expected)
	}
	for i, v := range expected {
		if log[i] != v {
			t.Errorf("at step %d: got %s, want %s", i, log[i], v)
		}
	}
}

func TestChainService_RestorablePreservation(t *testing.T) {
	var log []string
	base := &fakeRestorableService{fakeService: fakeService{log: &log}}

	mw := func(next port.Service[dummyModel, string, dummyModel]) port.Service[dummyModel, string, dummyModel] {
		return &wrappingService{Service: next, name: "mw", log: &log}
	}

	chained := port.ChainService[dummyModel, string, dummyModel](base, mw)
	restorable, ok := port.RestorableOf[string](chained)
	if !ok {
		t.Fatal("expected chained service to preserve restorable capability")
	}

	_, err := restorable.Restore(context.Background(), port.RestoreCommand[string]{ID: "123"})
	if err != nil {
		t.Fatalf("unexpected error on restore: %v", err)
	}
	if len(log) != 1 || log[0] != "base.Restore" {
		t.Fatalf("unexpected log: %v", log)
	}
}

func TestChainService_NilBase(t *testing.T) {
	chained := port.ChainService[dummyModel, string, dummyModel](nil)
	if chained != nil {
		t.Fatalf("expected nil when base is nil")
	}
	var typedNil *fakeService
	if chained := port.ChainService[dummyModel, string, dummyModel](typedNil); chained != nil {
		t.Fatal("expected nil for typed-nil base")
	}
	var nilMiddleware port.ServiceMiddleware[dummyModel, string, dummyModel]
	if chained := port.ChainService[dummyModel, string, dummyModel](&fakeService{}, nilMiddleware); chained == nil {
		t.Fatal("nil middleware function should be skipped")
	}
}

func TestChainService_NilMiddlewareResultDoesNotFailOpen(t *testing.T) {
	base := &fakeService{}
	nilResult := func(port.Service[dummyModel, string, dummyModel]) port.Service[dummyModel, string, dummyModel] {
		return nil
	}
	if chained := port.ChainService(base, nilResult); chained != nil {
		t.Fatal("expected nil when middleware returns nil")
	}

	var typedNil *wrappingService
	typedNilResult := func(port.Service[dummyModel, string, dummyModel]) port.Service[dummyModel, string, dummyModel] {
		return typedNil
	}
	if chained := port.ChainService(base, typedNilResult); chained != nil {
		t.Fatal("expected nil when middleware returns typed nil")
	}
}
