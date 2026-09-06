package tenancyjobs

import (
	"context"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/tenancy"
)

// The queue table is durable, shared and writable by whatever else reaches that
// database, so the reference in a record is a claim and not an authority. What
// makes it this deployment's claim is the authority's sealer, and a deployment
// that configured no durable key is refused a producer here rather than given a
// worker that trusts what it reads.
func ContextProvider(authority *tenancy.Authority, provenance jobs.IdentityProvenance, epoch jobs.IdentityEpoch) (jobs.TrustedContextProvider, error) {
	sealer, err := authority.Sealer()
	if err != nil {
		return nil, err
	}
	if provenance.IsZero() || epoch.IsZero() {
		return nil, tenancy.ErrMalformed
	}
	return contextProvider{authority: authority, sealer: sealer, provenance: provenance, epoch: epoch}, nil
}

type contextProvider struct {
	authority  *tenancy.Authority
	sealer     tenancy.Sealer
	provenance jobs.IdentityProvenance
	epoch      jobs.IdentityEpoch
}

func (this contextProvider) Capture(ctx context.Context, request jobs.ContextCaptureRequest) (jobs.ContextCapture, error) {
	spec := jobs.ContextCaptureSpec{Provenance: this.provenance, Epoch: this.epoch}
	if request.Partition() == jobs.PartitionTenantRequired {
		scope, err := this.authority.Scope(ctx, tenancy.ClassDurable)
		if err != nil {
			return jobs.ContextCapture{}, err
		}
		partition, err := jobs.ParsePartition(scope.Reference().Value())
		if err != nil {
			return jobs.ContextCapture{}, tenancy.ErrMalformed
		}
		sealed, err := this.sealer.Seal(scope, record(request.Namespace(), request.Definition())...)
		if err != nil {
			return jobs.ContextCapture{}, err
		}
		token, err := jobs.NewProtectedIdentityToken(sealed)
		if err != nil {
			return jobs.ContextCapture{}, tenancy.ErrMalformed
		}
		spec.Tenant = partition
		spec.Token = token
	}
	return jobs.NewContextCapture(spec)
}

func IdentityRestorer(authority *tenancy.Authority) (jobs.TrustedIdentityRestorer, error) {
	sealer, err := authority.Sealer()
	if err != nil {
		return nil, err
	}
	return identityRestorer{authority: authority, sealer: sealer}, nil
}

type identityRestorer struct {
	authority *tenancy.Authority
	sealer    tenancy.Sealer
}

// The durable record is a reference and never an authority: the reference and the
// generation the producer wrote are read back only to ask the control plane what
// is true now. A tenant suspended, deleted or restored since the work was
// enqueued therefore refuses here, and a record whose generation has been
// overtaken refuses even though it verifies.
func (this identityRestorer) RestoreIdentity(ctx context.Context, request jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
	if request.Scope() != jobs.ContextTenant {
		return jobs.NewRestoredIdentity(ctx, jobs.ProducerPartition{}, jobs.ProducerActor{})
	}
	token, ok := request.Token()
	if !ok {
		return jobs.RestoredIdentity{}, tenancy.ErrUntrusted
	}
	reference, generation, err := this.sealer.Unseal(token.Bytes(), record(request.Namespace(), request.Definition())...)
	if err != nil {
		return jobs.RestoredIdentity{}, err
	}
	scope, err := this.authority.Lookup(ctx, reference, tenancy.ClassDurable)
	if err != nil {
		return jobs.RestoredIdentity{}, err
	}
	if scope.Epoch() != generation {
		return jobs.RestoredIdentity{}, tenancy.ErrStale
	}
	bound, err := this.authority.With(ctx, scope)
	if err != nil {
		return jobs.RestoredIdentity{}, err
	}
	return jobs.NewRestoredIdentity(bound, jobs.Partition(reference.Value()), jobs.ProducerActor{})
}

// The queue and the definition are sealed with the reference, so a record lifted
// out of one job and replayed into another verifies against neither.
func record(namespace jobs.Namespace, definition jobs.Name) [][]byte {
	digest := namespace.Digest()
	return [][]byte{digest[:], []byte(definition.Value())}
}
