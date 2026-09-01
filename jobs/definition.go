package jobs

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
)

type PayloadIdentity[P any] interface {
	ID() CodecID
	Version() SchemaVersion
	Digest(P, PayloadLimit) ([32]byte, error)
}

const AutomaticPayloadIdentityVersion SchemaVersion = 1

type DefinitionSpec[P any] struct {
	Name      Name
	Codec     Codec[P]
	Identity  PayloadIdentity[P]
	Upcasters []Upcaster
	Policy    Policy
	Partition PartitionMode
}

func (DefinitionSpec[P]) String() string { return "[job definition spec]" }
func (this DefinitionSpec[P]) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

type Definition[P any] struct {
	name            Name
	codec           Codec[P]
	codecInfo       codecDescription
	identity        PayloadIdentity[P]
	identityInfo    PayloadIdentityDescription
	encodedIdentity bool
	upcasters       []upcasterDescription
	policy          Policy
	partition       PartitionMode
	descriptor      Descriptor
}

func Define[P any](spec DefinitionSpec[P]) (*Definition[P], error) {
	if !spec.Name.valid() || !spec.Partition.Valid() {
		return nil, fmt.Errorf("%w: definition name is invalid", ErrInvalid)
	}
	if err := validatePolicy(spec.Policy); err != nil {
		return nil, err
	}
	codecInfo, err := describeCodec(spec.Codec)
	if err != nil {
		return nil, err
	}
	if err := validateCodecPayloadLimit(spec.Codec, spec.Policy.Payload); err != nil {
		return nil, err
	}
	upcasters, revisions, descriptions, err := normalizeUpcasters(spec.Upcasters, codecInfo)
	if err != nil {
		return nil, err
	}
	for _, upcaster := range upcasters {
		if err := invokeUpcasterLimitValidation(upcaster.upcaster, spec.Policy.Payload); err != nil {
			return nil, err
		}
	}
	mode := CustomCodecMode
	if described, ok := any(spec.Codec).(interface{ codecMode() CodecMode }); ok {
		mode = described.codecMode()
	}
	identity := spec.Identity
	if nilInterface(identity) {
		identity = nil
		if supported, ok := any(spec.Codec).(PayloadIdentity[P]); ok {
			identity = supported
		}
	}
	identityInfo := PayloadIdentityDescription{}
	encodedIdentity := false
	if identity != nil {
		identityInfo, err = describePayloadIdentity(identity)
		if err != nil {
			return nil, err
		}
	} else if mode == SafeCodecMode {
		identityInfo = PayloadIdentityDescription{ID: codecInfo.id, Version: AutomaticPayloadIdentityVersion, Available: true, Automatic: true}
		encodedIdentity = true
	}
	descriptor := Descriptor{
		Name: spec.Name,
		Codec: CodecDescription{
			ID:                 codecInfo.id,
			CurrentVersion:     codecInfo.version,
			SupportedRevisions: revisions,
			Mode:               mode,
			Upcasts:            descriptions,
		},
		PayloadIdentity: identityInfo,
		Policy:          describePolicy(spec.Policy),
		Partition:       spec.Partition,
		Resolved:        true,
	}
	return &Definition[P]{name: spec.Name, codec: spec.Codec, codecInfo: codecInfo, identity: identity, identityInfo: identityInfo, encodedIdentity: encodedIdentity, upcasters: upcasters, policy: spec.Policy, partition: spec.Partition, descriptor: descriptor}, nil
}

func MustDefine[P any](spec DefinitionSpec[P]) *Definition[P] {
	definition, err := Define(spec)
	if err != nil {
		panic(err)
	}
	return definition
}

func (this *Definition[P]) Name() Name {
	if this == nil {
		return Name{}
	}
	return this.name
}

func (this *Definition[P]) Policy() Policy {
	if this == nil {
		return Policy{}
	}
	return this.policy
}

func (this *Definition[P]) Partition() PartitionMode {
	if this == nil {
		return PartitionGlobal
	}
	return this.partition
}

func (this *Definition[P]) PayloadIdentity() PayloadIdentityDescription {
	if this == nil {
		return PayloadIdentityDescription{}
	}
	return this.identityInfo
}

func (this *Definition[P]) Describe() Descriptor {
	if this == nil {
		return Descriptor{}
	}
	return cloneDescriptor(this.descriptor)
}

func (this Definition[P]) String() string {
	if this.name.IsZero() {
		return "[job definition]"
	}
	return fmt.Sprintf("[job definition name=%s]", this.name)
}

func (this Definition[P]) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

func (this *Definition[P]) Encode(value P) (EncodedPayload, error) {
	if this == nil {
		return EncodedPayload{}, fmt.Errorf("%w: definition is nil", ErrInvalid)
	}
	if err := this.validateCurrentCodec(); err != nil {
		return EncodedPayload{}, err
	}
	encoded, err := invokeCodecEncode(this.codec, value, this.policy.Payload)
	if err != nil {
		return EncodedPayload{}, err
	}
	if len(encoded) > this.policy.Payload.MaxBytes {
		return EncodedPayload{}, ErrTooLarge
	}
	return NewEncodedPayload(this.codecInfo.id, this.codecInfo.version, encoded)
}

func (this *Definition[P]) Digest(value P) (PayloadDigest, error) {
	_, digest, err := this.preparePayload(value, true)
	return digest, err
}

func (this *Definition[P]) preparePayload(value P, requireIdentity bool) (EncodedPayload, PayloadDigest, error) {
	if this == nil {
		return EncodedPayload{}, PayloadDigest{}, fmt.Errorf("%w: definition is nil", ErrInvalid)
	}
	if requireIdentity && !this.identityInfo.Available {
		return EncodedPayload{}, PayloadDigest{}, ErrUnsupported
	}
	var semantic PayloadDigest
	if requireIdentity && !this.encodedIdentity {
		if err := validatePayloadIdentitySnapshot(this.identity, this.identityInfo); err != nil {
			return EncodedPayload{}, PayloadDigest{}, err
		}
		valueDigest, err := invokePayloadIdentity(this.identity, value, this.policy.Payload)
		if err != nil {
			return EncodedPayload{}, PayloadDigest{}, err
		}
		semantic, err = NewPayloadDigest(this.identityInfo.ID, this.identityInfo.Version, valueDigest)
		if err != nil {
			return EncodedPayload{}, PayloadDigest{}, err
		}
	}
	encoded, err := this.Encode(value)
	if err != nil {
		return EncodedPayload{}, PayloadDigest{}, err
	}
	if !requireIdentity {
		return encoded, PayloadDigest{}, nil
	}
	if this.encodedIdentity {
		return encoded, digestEncodedPayload(this.identityInfo.ID, this.identityInfo.Version, encoded.encodedBytes()), nil
	}
	return encoded, semantic, nil
}

func (this *Definition[P]) Decode(payload EncodedPayload) (P, error) {
	return this.decodePayload(payload, false)
}

func (this *Definition[P]) decodeOwned(payload EncodedPayload) (P, error) {
	return this.decodePayload(payload, true)
}

func (this *Definition[P]) decodePayload(payload EncodedPayload, owned bool) (P, error) {
	var zero P
	if this == nil || payload.IsZero() {
		return zero, fmt.Errorf("%w: definition or payload is invalid", ErrInvalid)
	}
	if payload.encodedLength() > this.policy.Payload.MaxBytes {
		return zero, ErrTooLarge
	}
	if err := this.validateCurrentCodec(); err != nil {
		return zero, err
	}
	encoded := payload.encodedBytes()
	if payload.Version() == this.codecInfo.version {
		if !payload.matches(this.codecInfo.id) {
			return zero, fmt.Errorf("%w: current payload codec does not match", ErrCorrupt)
		}
		if owned {
			return invokeCodecDecodeOwned(this.codec, encoded, this.policy.Payload)
		}
		return invokeCodecDecode(this.codec, encoded, this.policy.Payload)
	}
	index := sort.Search(len(this.upcasters), func(index int) bool {
		return this.upcasters[index].from >= payload.Version()
	})
	if index == len(this.upcasters) || this.upcasters[index].from != payload.Version() {
		return zero, fmt.Errorf("%w: payload revision is outside the supported window", ErrUnsupported)
	}
	if payload.Codec() != this.upcasters[index].sourceCodec {
		return zero, fmt.Errorf("%w: historic payload codec does not match", ErrCorrupt)
	}
	for ; index < len(this.upcasters); index++ {
		upcaster := this.upcasters[index]
		if err := validateUpcasterSnapshot(upcaster); err != nil {
			return zero, err
		}
		var next []byte
		var err error
		if owned {
			next, err = invokeUpcasterOwned(upcaster.upcaster, encoded, this.policy.Payload)
		} else {
			next, err = invokeUpcaster(upcaster.upcaster, encoded, this.policy.Payload)
		}
		if err != nil {
			return zero, err
		}
		encoded = next
	}
	if owned {
		return invokeCodecDecodeOwned(this.codec, encoded, this.policy.Payload)
	}
	return invokeCodecDecode(this.codec, encoded, this.policy.Payload)
}

func (this *Definition[P]) validateCurrentCodec() error {
	current, err := describeCodec(this.codec)
	if err != nil {
		return err
	}
	if current != this.codecInfo {
		return fmt.Errorf("%w: codec identity changed after definition", ErrInvalid)
	}
	return nil
}

func (this *Definition[P]) declarationName() Name { return this.name }
func (this *Definition[P]) declarationMarker()    {}

func describePayloadIdentity[P any](identity PayloadIdentity[P]) (description PayloadIdentityDescription, err error) {
	defer func() {
		if recover() != nil {
			description = PayloadIdentityDescription{}
			err = fmt.Errorf("%w: payload identity descriptor panicked", ErrInvalid)
		}
	}()
	if nilInterface(identity) {
		return PayloadIdentityDescription{}, fmt.Errorf("%w: payload identity is required", ErrInvalid)
	}
	id := identity.ID()
	version := identity.Version()
	if id.IsZero() || version.IsZero() {
		return PayloadIdentityDescription{}, fmt.Errorf("%w: payload identity id and version are required", ErrInvalid)
	}
	return PayloadIdentityDescription{ID: id, Version: version, Available: true}, nil
}

func validatePayloadIdentitySnapshot[P any](identity PayloadIdentity[P], expected PayloadIdentityDescription) error {
	current, err := describePayloadIdentity(identity)
	if err != nil {
		return err
	}
	if current.ID != expected.ID || current.Version != expected.Version {
		return fmt.Errorf("%w: payload identity changed after definition", ErrInvalid)
	}
	return nil
}

func invokePayloadIdentity[P any](identity PayloadIdentity[P], value P, limit PayloadLimit) (digest [32]byte, err error) {
	defer func() {
		if recover() != nil {
			digest = [32]byte{}
			err = fmt.Errorf("%w: payload identity panicked", ErrInvalid)
		}
	}()
	digest, err = identity.Digest(value, limit)
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			return [32]byte{}, ErrTooLarge
		}
		return [32]byte{}, ErrInvalid
	}
	if digest == [32]byte{} {
		return [32]byte{}, ErrInvalid
	}
	return digest, nil
}

func normalizeUpcasters(values []Upcaster, current codecDescription) ([]upcasterDescription, []SchemaVersion, []UpcastDescription, error) {
	if len(values) > MaxUpcastHops {
		return nil, nil, nil, fmt.Errorf("%w: too many upcaster hops", ErrTooLarge)
	}
	result := make([]upcasterDescription, len(values))
	for index, value := range values {
		descriptor, err := describeUpcaster(value)
		if err != nil {
			return nil, nil, nil, err
		}
		result[index] = descriptor
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].from != result[right].from {
			return result[left].from < result[right].from
		}
		if result[left].to != result[right].to {
			return result[left].to < result[right].to
		}
		if result[left].sourceCodec != result[right].sourceCodec {
			return result[left].sourceCodec.String() < result[right].sourceCodec.String()
		}
		return result[left].targetCodec.String() < result[right].targetCodec.String()
	})
	for index := range result {
		if index > 0 && result[index].from == result[index-1].from {
			return nil, nil, nil, fmt.Errorf("%w: duplicate upcaster source revision", ErrConflict)
		}
		if index > 0 && (result[index-1].to != result[index].from || result[index-1].targetCodec != result[index].sourceCodec) {
			return nil, nil, nil, fmt.Errorf("%w: upcaster chain is not contiguous", ErrInvalid)
		}
	}
	if len(result) > 0 {
		last := result[len(result)-1]
		if last.to != current.version || last.targetCodec != current.id {
			return nil, nil, nil, fmt.Errorf("%w: upcaster chain does not terminate at the current codec", ErrInvalid)
		}
	}
	revisions := make([]SchemaVersion, 0, len(result)+1)
	descriptions := make([]UpcastDescription, len(result))
	for index, upcaster := range result {
		revisions = append(revisions, upcaster.from)
		descriptions[index] = UpcastDescription{From: upcaster.from, To: upcaster.to, SourceCodec: upcaster.sourceCodec, TargetCodec: upcaster.targetCodec}
	}
	revisions = append(revisions, current.version)
	if len(revisions) > MaxSupportedRevisions {
		return nil, nil, nil, fmt.Errorf("%w: supported revision window is too large", ErrTooLarge)
	}
	return result, revisions, descriptions, nil
}

func validateUpcasterSnapshot(expected upcasterDescription) error {
	current, err := describeUpcaster(expected.upcaster)
	if err != nil {
		return err
	}
	if current.from != expected.from || current.to != expected.to || current.sourceCodec != expected.sourceCodec || current.targetCodec != expected.targetCodec {
		return fmt.Errorf("%w: upcaster identity changed after definition", ErrInvalid)
	}
	return nil
}

func invokeCodecEncode[P any](codec Codec[P], value P, limit PayloadLimit) (encoded []byte, err error) {
	defer func() {
		if recover() != nil {
			encoded = nil
			err = fmt.Errorf("%w: codec encode panicked", ErrInvalid)
		}
	}()
	encoded, err = codec.Encode(value, limit)
	if err != nil {
		encoded = nil
		err = normalizeCodecEncodeError(err)
	}
	return encoded, err
}

func invokeCodecEncodeOwned[P any](codec Codec[P], value P, limit PayloadLimit) ([]byte, error) {
	encoded, err := invokeCodecEncode(codec, value, limit)
	if err != nil {
		return nil, err
	}
	if len(encoded) > limit.MaxBytes || len(encoded) > MaxPayloadBytes {
		return nil, ErrTooLarge
	}
	if _, owned := any(codec).(interface{ ownsEncodedOutput() }); !owned {
		encoded = bytes.Clone(encoded)
	}
	return encoded[:len(encoded):len(encoded)], nil
}

func invokeCodecDecode[P any](codec Codec[P], encoded []byte, limit PayloadLimit) (value P, err error) {
	return invokeCodecDecodeOwned(codec, bytes.Clone(encoded), limit)
}

func invokeCodecDecodeOwned[P any](codec Codec[P], encoded []byte, limit PayloadLimit) (value P, err error) {
	encoded = encoded[:len(encoded):len(encoded)]
	defer func() {
		if recover() != nil {
			var zero P
			value = zero
			err = fmt.Errorf("%w: codec decode panicked", ErrCorrupt)
		}
	}()
	if decoder, ok := any(codec).(interface {
		decodeOwned([]byte, PayloadLimit) (P, error)
	}); ok {
		value, err = decoder.decodeOwned(encoded, limit)
	} else {
		value, err = codec.Decode(encoded, limit)
	}
	if err != nil {
		var zero P
		value = zero
		err = normalizeCodecDecodeError(err)
	}
	if raw, ok := any(value).([]byte); ok {
		raw = raw[:len(raw):len(raw)]
		value = any(raw).(P)
	}
	return value, err
}

func normalizeDefinitionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrTooLarge) {
		return ErrTooLarge
	}
	return ErrInvalid
}

func normalizeCodecEncodeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrTooLarge) {
		return ErrTooLarge
	}
	return ErrInvalid
}

func normalizeCodecDecodeError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrTooLarge):
		return ErrTooLarge
	case errors.Is(err, ErrUnsupported):
		return ErrUnsupported
	case errors.Is(err, ErrCorrupt):
		return ErrCorrupt
	default:
		return ErrCorrupt
	}
}
