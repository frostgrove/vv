package cache

import "fmt"

type ProviderKind string

const (
	MemoryProviderKind     ProviderKind = "memory"
	PostgreSQLProviderKind ProviderKind = "postgresql"
	RedisProviderKind      ProviderKind = "redis"
	NoProviderKind         ProviderKind = "none"
)

type ProviderID string
type ResourceID string

type Provider struct {
	ID        ProviderID
	Resource  ResourceID
	Kind      ProviderKind
	Backend   Backend
	ClockSkew SkewPolicy
	Fallback  bool
}

type ProfileDescription struct {
	Name     string
	Provider ProviderKind
	Policy   PolicyDescription
}

func (this Profile) ProviderKind() ProviderKind { return this.provider }

func (this Profile) Describe() ProfileDescription {
	return ProfileDescription{
		Name:     this.name,
		Provider: this.provider,
		Policy:   describePolicy(this.policy),
	}
}

func validProviderKind(kind ProviderKind) bool {
	return kind == MemoryProviderKind || kind == PostgreSQLProviderKind || kind == RedisProviderKind || kind == NoProviderKind
}

func validProvider(provider Provider) error {
	if provider.ID == "" || validNamespacePart(string(provider.ID)) != nil || !validProviderKind(provider.Kind) ||
		provider.Kind == NoProviderKind || nilInterface(provider.Backend) {
		return fmt.Errorf("%w: cache provider is invalid", ErrInvalid)
	}
	if provider.Resource != "" && validNamespacePart(string(provider.Resource)) != nil {
		return fmt.Errorf("%w: cache provider resource identity is invalid", ErrInvalid)
	}
	description, ok := BackendDescriptionOf(provider.Backend)
	if !ok {
		return fmt.Errorf("%w: cache provider backend description is invalid", ErrInvalid)
	}
	switch description.Topology {
	case ProcessBackend:
		if provider.ClockSkew.mode != 0 && provider.ClockSkew.mode != SingleProcessSkew {
			return fmt.Errorf("%w: process cache provider has shared clock skew", ErrInvalid)
		}
	case SharedBackend:
		if provider.ClockSkew.mode != BoundedSharedSkew {
			return fmt.Errorf("%w: shared cache provider has process clock skew", ErrInvalid)
		}
	}
	return nil
}

func (this Provider) resourceIdentity() ResourceID {
	if this.Resource != "" {
		return this.Resource
	}
	return ResourceID(this.ID)
}
