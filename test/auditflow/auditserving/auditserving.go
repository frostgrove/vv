package auditserving

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/health"
)

var ErrUnready = errors.New("audit serving runtime is not ready")

type Inputs struct {
	Recorder *audit.Recorder
	History  *audit.History
	Store    audit.StoreInfo
	Active   audit.CatalogRef
}

type probe struct {
	store  audit.StoreInfo
	active audit.CatalogRef
}

func NewRecorder(inputs Inputs) *audit.Recorder {
	return inputs.Recorder
}

func NewHistory(inputs Inputs) *audit.History {
	return inputs.History
}

func NewHealth(inputs Inputs) health.Contribution {
	return health.Contribution{
		Name:       "audit",
		Code:       "audit_unready",
		Importance: health.Required,
		Probe:      probe{store: inputs.Store, active: inputs.Active},
	}
}

func (p probe) Check(ctx context.Context) error {
	if ctx == nil {
		return ErrUnready
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.store == nil || p.store.BackingID() == (audit.BackingID{}) || p.store.LogID() == (audit.LogID{}) {
		return ErrUnready
	}
	state := p.store.Catalogs()
	if !state.HasActive() || state.Active() != p.active {
		return ErrUnready
	}
	return nil
}
