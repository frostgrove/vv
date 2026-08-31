package jobs

import (
	"bytes"
	"fmt"
)

type Upcaster interface {
	From() SchemaVersion
	To() SchemaVersion
	SourceCodec() CodecID
	TargetCodec() CodecID
	upcast([]byte, PayloadLimit) ([]byte, error)
	validateUpcasterLimit(PayloadLimit) error
	upcasterMarker()
}

type typedUpcaster[A, B any] struct {
	from Codec[A]
	to   Codec[B]
	fn   func(A) (B, error)
}

func Upcast[A, B any](from Codec[A], to Codec[B], fn func(A) (B, error)) Upcaster {
	return typedUpcaster[A, B]{from: from, to: to, fn: fn}
}

func (this typedUpcaster[A, B]) From() SchemaVersion {
	descriptor, _ := describeCodec(this.from)
	return descriptor.version
}

func (this typedUpcaster[A, B]) To() SchemaVersion {
	descriptor, _ := describeCodec(this.to)
	return descriptor.version
}

func (this typedUpcaster[A, B]) SourceCodec() CodecID {
	descriptor, _ := describeCodec(this.from)
	return descriptor.id
}

func (this typedUpcaster[A, B]) TargetCodec() CodecID {
	descriptor, _ := describeCodec(this.to)
	return descriptor.id
}

func (typedUpcaster[A, B]) String() string { return "[job upcaster]" }
func (this typedUpcaster[A, B]) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

func (typedUpcaster[A, B]) upcasterMarker() {}

func (this typedUpcaster[A, B]) validateUpcasterLimit(limit PayloadLimit) error {
	if err := validateCodecPayloadLimit(this.from, limit); err != nil {
		return err
	}
	return validateCodecPayloadLimit(this.to, limit)
}

func (this typedUpcaster[A, B]) upcast(encoded []byte, limit PayloadLimit) (result []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("%w: upcaster panicked", ErrCorrupt)
		}
	}()
	if this.fn == nil {
		return nil, fmt.Errorf("%w: upcaster function is required", ErrInvalid)
	}
	if len(encoded) > limit.MaxBytes {
		return nil, ErrTooLarge
	}
	value, err := invokeCodecDecode(this.from, encoded, limit)
	if err != nil {
		return nil, normalizeUpcastRuntimeError(err)
	}
	next, err := this.fn(value)
	if err != nil {
		return nil, ErrCorrupt
	}
	result, err = invokeCodecEncode(this.to, next, limit)
	if err != nil {
		return nil, normalizeUpcastRuntimeError(err)
	}
	if len(result) > limit.MaxBytes {
		return nil, ErrTooLarge
	}
	return bytes.Clone(result), nil
}

type upcasterDescription struct {
	from        SchemaVersion
	to          SchemaVersion
	sourceCodec CodecID
	targetCodec CodecID
	upcaster    Upcaster
}

func describeUpcaster(upcaster Upcaster) (descriptor upcasterDescription, err error) {
	defer func() {
		if recover() != nil {
			descriptor = upcasterDescription{}
			err = fmt.Errorf("%w: upcaster descriptor panicked", ErrInvalid)
		}
	}()
	if nilInterface(upcaster) {
		return upcasterDescription{}, fmt.Errorf("%w: upcaster is required", ErrInvalid)
	}
	descriptor = upcasterDescription{
		from:        upcaster.From(),
		to:          upcaster.To(),
		sourceCodec: upcaster.SourceCodec(),
		targetCodec: upcaster.TargetCodec(),
		upcaster:    upcaster,
	}
	if descriptor.from.IsZero() || descriptor.to.IsZero() || descriptor.sourceCodec.IsZero() || descriptor.targetCodec.IsZero() || descriptor.from == ^SchemaVersion(0) || descriptor.to != descriptor.from+1 {
		return upcasterDescription{}, fmt.Errorf("%w: upcaster revisions must be adjacent", ErrInvalid)
	}
	if typed, ok := upcaster.(interface{ validateUpcaster() error }); ok {
		if err := typed.validateUpcaster(); err != nil {
			return upcasterDescription{}, normalizeDefinitionError(err)
		}
	}
	return descriptor, nil
}

func invokeUpcaster(upcaster Upcaster, encoded []byte, limit PayloadLimit) (result []byte, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = ErrCorrupt
		}
	}()
	result, err = upcaster.upcast(bytes.Clone(encoded), limit)
	if err != nil {
		return nil, normalizeUpcastRuntimeError(err)
	}
	if len(result) > limit.MaxBytes {
		return nil, ErrTooLarge
	}
	return bytes.Clone(result), nil
}

func invokeUpcasterLimitValidation(upcaster Upcaster, limit PayloadLimit) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrInvalid
		}
	}()
	return normalizeDefinitionError(upcaster.validateUpcasterLimit(limit))
}

func normalizeUpcastRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	if normalized := normalizeCodecDecodeError(err); normalized == ErrTooLarge || normalized == ErrUnsupported {
		return normalized
	}
	return ErrCorrupt
}

func (this typedUpcaster[A, B]) validateUpcaster() error {
	if this.fn == nil {
		return fmt.Errorf("%w: upcaster function is required", ErrInvalid)
	}
	from, err := describeCodec(this.from)
	if err != nil {
		return err
	}
	to, err := describeCodec(this.to)
	if err != nil {
		return err
	}
	if from.version == ^SchemaVersion(0) || to.version != from.version+1 {
		return fmt.Errorf("%w: upcaster revisions must be adjacent", ErrInvalid)
	}
	return nil
}
