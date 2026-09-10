package auditserving

import (
	"context"
	"errors"
	"reflect"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/health"
)

var ErrUnready = errors.New("audit serving runtime is not ready")

type BackendCheck interface {
	Check(context.Context) error
}

type Inputs struct {
	Recorder *audit.Recorder
	History  *audit.History
	Attempts *audit.Attempts
	Store    audit.StoreInfo
	Backend  BackendCheck
	Active   audit.CatalogRef
}

type probe struct {
	store    audit.StoreInfo
	active   audit.CatalogRef
	attempts *audit.Attempts
	recorder *audit.Recorder
	history  *audit.History
	backend  BackendCheck
}

func NewRecorder(inputs Inputs) *audit.Recorder {
	return inputs.Recorder
}

func NewHistory(inputs Inputs) *audit.History {
	return inputs.History
}

func NewAttempts(inputs Inputs) *audit.Attempts {
	return inputs.Attempts
}

func NewHealth(inputs Inputs) health.Contribution {
	return health.Contribution{
		Name:       "audit",
		Code:       "audit_unready",
		Importance: health.Required,
		Probe: probe{
			store: inputs.Store, active: inputs.Active, attempts: inputs.Attempts,
			recorder: inputs.Recorder, history: inputs.History, backend: inputs.Backend,
		},
	}
}

func (p probe) Check(ctx context.Context) error {
	if ctx == nil {
		return ErrUnready
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if absent(p.store) || absent(p.backend) || p.recorder == nil || p.history == nil || p.attempts == nil {
		return ErrUnready
	}
	if err := p.backend.Check(ctx); err != nil {
		return errors.Join(ErrUnready, err)
	}
	checked, ok := p.backend.(audit.StoreInfo)
	if !ok || p.store.BackingID() == (audit.BackingID{}) || p.store.LogID() == (audit.LogID{}) ||
		checked.BackingID() != p.store.BackingID() || checked.LogID() != p.store.LogID() ||
		!audit.SameBacking(checked.Backing(), p.store.Backing()) || p.history.Profile() != audit.PublicOnePageDevelopmentAlpha {
		return ErrUnready
	}
	state := p.store.Catalogs()
	checkedState := checked.Catalogs()
	if !state.HasActive() || !checkedState.HasActive() || state.Active() != p.active || checkedState.Active() != p.active || state.SetDigest() != checkedState.SetDigest() {
		return ErrUnready
	}
	return nil
}

func absent(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
