package jobs

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
)

type Declaration interface {
	Describe() Descriptor
	declarationName() Name
	declarationMarker()
}

type Catalog struct {
	declarations []Declaration
	descriptors  []Descriptor
	byName       map[Name]int
	fingerprint  string
}

func NewCatalog(declarations ...Declaration) (Catalog, error) {
	if len(declarations) > MaxDefinitions {
		return Catalog{}, fmt.Errorf("%w: catalog has too many definitions", ErrTooLarge)
	}
	entries := append([]Declaration(nil), declarations...)
	for index, declaration := range entries {
		if nilInterface(declaration) {
			return Catalog{}, fmt.Errorf("%w: declaration %d is nil or unresolved", ErrInvalid, index)
		}
		entries[index] = canonicalDeclarationOf(declaration)
		if nilInterface(entries[index]) || !entries[index].declarationName().valid() {
			return Catalog{}, fmt.Errorf("%w: declaration %d is nil or unresolved", ErrInvalid, index)
		}
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].declarationName().String() < entries[right].declarationName().String()
	})
	descriptors := make([]Descriptor, len(entries))
	byName := make(map[Name]int, len(entries))
	for index, declaration := range entries {
		name := declaration.declarationName()
		if index > 0 && name == entries[index-1].declarationName() {
			return Catalog{}, fmt.Errorf("%w: duplicate definition %q", ErrConflict, name)
		}
		descriptor := declaration.Describe()
		if descriptor.Automatic {
			if _, ok := declaration.(automaticQueueBinding); !ok {
				return Catalog{}, fmt.Errorf("%w: automatic definition %q lost its declaration binding", ErrInvalid, name)
			}
		}
		if !descriptor.Resolved || descriptor.Name != name || !descriptor.Partition.Valid() || descriptor.PayloadIdentity.Available && (descriptor.PayloadIdentity.ID.IsZero() || descriptor.PayloadIdentity.Version.IsZero()) || !descriptor.PayloadIdentity.Available && (!descriptor.PayloadIdentity.ID.IsZero() || !descriptor.PayloadIdentity.Version.IsZero() || descriptor.PayloadIdentity.Automatic) {
			return Catalog{}, fmt.Errorf("%w: declaration descriptor is unresolved", ErrInvalid)
		}
		descriptors[index] = cloneDescriptor(descriptor)
		byName[name] = index
	}
	return Catalog{declarations: entries, descriptors: descriptors, byName: byName, fingerprint: fingerprintDescriptors(descriptors)}, nil
}

func canonicalDeclarationOf(declaration Declaration) Declaration {
	if canonical, ok := declaration.(interface{ canonicalDeclaration() Declaration }); ok {
		return canonical.canonicalDeclaration()
	}
	return declaration
}

func MustCatalog(declarations ...Declaration) Catalog {
	catalog, err := NewCatalog(declarations...)
	if err != nil {
		panic(err)
	}
	return catalog
}

func (this Catalog) Fingerprint() string { return this.fingerprint }

func (this Catalog) Len() int { return len(this.declarations) }

func (this Catalog) String() string {
	return fmt.Sprintf("[job catalog definitions=%d]", len(this.declarations))
}

func (this Catalog) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

func (this Catalog) Lookup(name Name) (Declaration, bool) {
	index, ok := this.byName[name]
	if !ok {
		return nil, false
	}
	return this.declarations[index], true
}

func (this Catalog) Definitions() []Declaration {
	return append([]Declaration(nil), this.declarations...)
}

func (this Catalog) AutomaticConsumers() []Consumer {
	consumers := make([]Consumer, 0, len(this.declarations))
	for _, declaration := range this.declarations {
		consumer, ok := declaration.(Consumer)
		if !ok {
			continue
		}
		binding := consumer.consumerBinding()
		if binding.err == nil && binding.valid {
			consumers = append(consumers, consumer)
		}
	}
	return consumers
}

func (this Catalog) RequiresTenantPartition() bool {
	for _, descriptor := range this.descriptors {
		if descriptor.Partition == PartitionTenantRequired {
			return true
		}
	}
	return false
}

func (this Catalog) Describe() CatalogDescriptor {
	descriptors := make([]Descriptor, len(this.descriptors))
	for index, descriptor := range this.descriptors {
		descriptors[index] = cloneDescriptor(descriptor)
	}
	return CatalogDescriptor{Definitions: descriptors, Fingerprint: this.fingerprint}
}

func fingerprintDescriptors(descriptors []Descriptor) string {
	digest := sha256.New()
	writeFingerprintString(digest, "frostgrove.jobs.catalog.v4")
	writeFingerprintUint(digest, uint64(len(descriptors)))
	for _, descriptor := range descriptors {
		writeFingerprintString(digest, descriptor.Name.String())
		writeFingerprintString(digest, descriptor.Codec.ID.String())
		writeFingerprintUint(digest, uint64(descriptor.Codec.CurrentVersion))
		writeFingerprintString(digest, string(descriptor.Codec.Mode))
		writeFingerprintUint(digest, uint64(descriptor.Partition))
		if descriptor.PayloadIdentity.Available {
			writeFingerprintUint(digest, 1)
			writeFingerprintString(digest, descriptor.PayloadIdentity.ID.String())
			writeFingerprintUint(digest, uint64(descriptor.PayloadIdentity.Version))
			if descriptor.PayloadIdentity.Automatic {
				writeFingerprintUint(digest, 1)
			} else {
				writeFingerprintUint(digest, 0)
			}
		} else {
			writeFingerprintUint(digest, 0)
		}
		writeFingerprintUint(digest, uint64(len(descriptor.Codec.SupportedRevisions)))
		for _, revision := range descriptor.Codec.SupportedRevisions {
			writeFingerprintUint(digest, uint64(revision))
		}
		writeFingerprintUint(digest, uint64(len(descriptor.Codec.Upcasts)))
		for _, upcast := range descriptor.Codec.Upcasts {
			writeFingerprintUint(digest, uint64(upcast.From))
			writeFingerprintUint(digest, uint64(upcast.To))
			writeFingerprintString(digest, upcast.SourceCodec.String())
			writeFingerprintString(digest, upcast.TargetCodec.String())
		}
		policy := descriptor.Policy
		writeFingerprintString(digest, policy.Queue.String())
		writeFingerprintInt(digest, int64(policy.Priority))
		writeFingerprintInt(digest, int64(policy.AttemptTimeout))
		writeFingerprintInt(digest, int64(policy.ProgressTimeout))
		writeFingerprintInt(digest, int64(policy.MaxElapsed))
		writeFingerprintInt(digest, int64(policy.MaxRetries))
		writeFingerprintInt(digest, int64(policy.MaxHandlerDeferrals))
		writeFingerprintInt(digest, int64(policy.MaxDeliveryDeferrals))
		writeFingerprintInt(digest, int64(policy.Backoff.Initial))
		writeFingerprintInt(digest, int64(policy.Backoff.Maximum))
		writeFingerprintUint(digest, uint64(policy.Backoff.Jitter))
		writeFingerprintInt(digest, int64(policy.Retention))
		writeFingerprintInt(digest, int64(policy.IntentRetention))
		writeFingerprintInt(digest, int64(policy.MaxPayloadBytes))
		writeFingerprintInt(digest, int64(policy.MaxDecodedBytes))
		writeFingerprintInt(digest, int64(policy.MaxPayloadDepth))
		ackModes := policy.Durability.AcceptedAckModes().Values()
		writeFingerprintUint(digest, uint64(len(ackModes)))
		for _, mode := range ackModes {
			writeFingerprintUint(digest, uint64(mode))
		}
		failures := policy.Durability.ProtectedFailures().Values()
		writeFingerprintUint(digest, uint64(len(failures)))
		for _, failure := range failures {
			writeFingerprintUint(digest, uint64(failure))
		}
		traceKeys := policy.Trace.Keys()
		writeFingerprintUint(digest, uint64(len(traceKeys)))
		for _, key := range traceKeys {
			writeFingerprintString(digest, key.Value())
		}
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func writeFingerprintString(digest hash.Hash, value string) {
	writeFingerprintUint(digest, uint64(len(value)))
	_, _ = digest.Write([]byte(value))
}

func writeFingerprintInt(digest hash.Hash, value int64) {
	writeFingerprintUint(digest, uint64(value))
}

func writeFingerprintUint(digest hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = digest.Write(encoded[:])
}
