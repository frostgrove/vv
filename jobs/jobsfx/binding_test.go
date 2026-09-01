package jobsfx

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/jobs"
)

type typedHandler struct {
	payload string
}

func (handler *typedHandler) Handle(_ context.Context, payload string) error {
	handler.payload = payload
	return nil
}

type typedAdapter struct {
	payload string
	called  bool
}

type selfHandler struct{}

var selfBinding = AutoFor[*selfHandler, string]()

func (*selfHandler) Handle(ctx context.Context, payload string) error {
	return selfBinding.Go(ctx, payload)
}

type selfAdapter struct{}

var selfAdapterBinding = AutoAdapterFor[*selfAdapter, string]()

func (*selfAdapter) Handle(
	ctx context.Context,
	payload string,
	_ jobs.DeliveryMeta,
	_ jobs.AttemptController,
) error {
	return selfAdapterBinding.Go(ctx, payload)
}

func (adapter *typedAdapter) Handle(
	_ context.Context,
	payload string,
	meta jobs.DeliveryMeta,
	_ jobs.AttemptController,
) error {
	adapter.payload = payload
	adapter.called = true
	return nil
}

func TestAutoForRoutesToTypedHandle(t *testing.T) {
	binding := AutoFor[*typedHandler, string](jobs.Heavy)
	handler := &typedHandler{}
	if err := binding.handler(handler, context.Background(), "payload"); err != nil {
		t.Fatal(err)
	}
	if handler.payload != "payload" || binding.adapterHandler != nil {
		t.Fatalf("payload=%q adapter=%v", handler.payload, binding.adapterHandler != nil)
	}
}

func TestAutoAdapterForRoutesToTypedHandle(t *testing.T) {
	binding := AutoAdapterFor[*typedAdapter, string](jobs.Heavy)
	adapter := &typedAdapter{}
	if err := binding.adapterHandler(adapter, context.Background(), "payload", jobs.DeliveryMeta{}, nil); err != nil {
		t.Fatal(err)
	}
	if adapter.payload != "payload" || !adapter.called || binding.handler != nil {
		t.Fatalf("payload=%q called=%t handler=%v", adapter.payload, adapter.called, binding.handler != nil)
	}
}

func TestTypedBindingsDoNotCreateSelfHandlerInitializationCycles(t *testing.T) {
	if selfBinding == nil || selfAdapterBinding == nil {
		t.Fatal("typed binding was not initialized")
	}
}
