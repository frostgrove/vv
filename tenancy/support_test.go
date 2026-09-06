package tenancy

import "context"

type knownTenants struct{ byReference map[Reference]Resolution }

func (this knownTenants) Resolve(context.Context) (Resolution, error) {
	return Resolution{}, ErrNoScope
}

func (this knownTenants) Lookup(_ context.Context, reference Reference) (Resolution, error) {
	resolution, ok := this.byReference[reference]
	if !ok {
		return Resolution{}, ErrUnmapped
	}
	return resolution, nil
}
