package jobs

import (
	"fmt"
	"sync"
	"sync/atomic"
)

type GeneratedDefinitionSpec[P any] struct {
	Name      Name
	Codec     Codec[P]
	Identity  PayloadIdentity[P]
	Upcasters []Upcaster
	Partition PartitionMode
}

func (GeneratedDefinitionSpec[P]) String() string { return "[generated job definition spec]" }
func (this GeneratedDefinitionSpec[P]) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

type Automatic[P any] struct {
	handler      Handler[P]
	profile      Profile
	policy       Policy
	definition   atomic.Pointer[Definition[P]]
	activation   atomic.Pointer[QueueActivation]
	activationMu sync.Mutex
}

func Auto[P any](handler Handler[P], profiles ...Profile) *Automatic[P] {
	if handler == nil {
		panic(fmt.Errorf("%w: automatic job handler is required", ErrInvalid))
	}
	if len(profiles) > 1 {
		panic(fmt.Errorf("%w: Auto accepts at most one profile", ErrInvalid))
	}
	automatic := Declare[P](profiles...)
	automatic.handler = handler
	return automatic
}

func Declare[P any](profiles ...Profile) *Automatic[P] {
	if len(profiles) > 1 {
		panic(fmt.Errorf("%w: Declare accepts at most one profile", ErrInvalid))
	}
	profile := Default
	if len(profiles) == 1 {
		profile = profiles[0]
	}
	policy, err := profile.Build()
	if err != nil {
		panic(err)
	}
	return &Automatic[P]{profile: profile, policy: policy}
}

func Materialize[P any](automatic *Automatic[P], spec GeneratedDefinitionSpec[P]) (*Automatic[P], error) {
	if automatic == nil {
		return nil, fmt.Errorf("%w: automatic declaration is invalid", ErrInvalid)
	}
	if automatic.definition.Load() != nil {
		return nil, fmt.Errorf("%w: automatic declaration is already materialized", ErrConflict)
	}
	definition, err := Define(DefinitionSpec[P]{Name: spec.Name, Codec: spec.Codec, Identity: spec.Identity, Upcasters: spec.Upcasters, Policy: automatic.policy, Partition: spec.Partition})
	if err != nil {
		return nil, err
	}
	definition.descriptor.Automatic = true
	if !automatic.definition.CompareAndSwap(nil, definition) {
		return nil, fmt.Errorf("%w: automatic declaration is already materialized", ErrConflict)
	}
	return automatic, nil
}

func MustMaterialize[P any](automatic *Automatic[P], spec GeneratedDefinitionSpec[P]) *Automatic[P] {
	materialized, err := Materialize(automatic, spec)
	if err != nil {
		panic(err)
	}
	return materialized
}

func (this *Automatic[P]) Name() Name {
	if definition := this.resolved(); definition != nil {
		return definition.Name()
	}
	return Name{}
}

func (this *Automatic[P]) Policy() Policy {
	if this == nil {
		return Policy{}
	}
	return this.policy
}

func (this *Automatic[P]) Partition() PartitionMode {
	if definition := this.resolved(); definition != nil {
		return definition.Partition()
	}
	return PartitionGlobal
}

func (this *Automatic[P]) PayloadIdentity() PayloadIdentityDescription {
	if definition := this.resolved(); definition != nil {
		return definition.PayloadIdentity()
	}
	return PayloadIdentityDescription{}
}

func (this *Automatic[P]) Describe() Descriptor {
	if this == nil {
		return Descriptor{}
	}
	if definition := this.definition.Load(); definition != nil {
		return definition.Describe()
	}
	return Descriptor{Policy: describePolicy(this.policy), Automatic: true}
}

func (*Automatic[P]) String() string { return "[automatic job declaration]" }
func (this *Automatic[P]) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

func (this *Automatic[P]) Encode(value P) (EncodedPayload, error) {
	definition := this.resolved()
	if definition == nil {
		return EncodedPayload{}, ErrNotActivated
	}
	return definition.Encode(value)
}

func (this *Automatic[P]) Digest(value P) (PayloadDigest, error) {
	definition := this.resolved()
	if definition == nil {
		return PayloadDigest{}, ErrNotActivated
	}
	return definition.Digest(value)
}

func (this *Automatic[P]) preparePayload(value P, requireIdentity bool) (EncodedPayload, PayloadDigest, error) {
	definition := this.resolved()
	if definition == nil {
		return EncodedPayload{}, PayloadDigest{}, ErrNotActivated
	}
	return definition.preparePayload(value, requireIdentity)
}

func (this *Automatic[P]) Decode(payload EncodedPayload) (P, error) {
	definition := this.resolved()
	if definition == nil {
		var zero P
		return zero, ErrNotActivated
	}
	return definition.Decode(payload)
}

func (this *Automatic[P]) decodeOwned(payload EncodedPayload) (P, error) {
	definition := this.resolved()
	if definition == nil {
		var zero P
		return zero, ErrNotActivated
	}
	return definition.decodeOwned(payload)
}

func (this *Automatic[P]) Handler() Handler[P] {
	if this == nil {
		return nil
	}
	return this.handler
}

func (this *Automatic[P]) Definition() (*Definition[P], bool) {
	definition := this.resolved()
	return definition, definition != nil
}

func (this *Automatic[P]) resolved() *Definition[P] {
	if this == nil {
		return nil
	}
	return this.definition.Load()
}

func (this *Automatic[P]) declarationName() Name {
	if definition := this.resolved(); definition != nil {
		return definition.Name()
	}
	return Name{}
}

func (this *Automatic[P]) declarationMarker() {}
