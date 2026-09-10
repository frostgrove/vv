package jobs

import (
	"bytes"
	"errors"
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
	result, err = this.upcastOwned(bytes.Clone(encoded), limit)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(result), nil
}

func (this typedUpcaster[A, B]) upcastOwned(encoded []byte, limit PayloadLimit) (result []byte, err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			result = nil
			err = fmt.Errorf("%w: upcaster panicked", ErrInvalid)
		}
	}()
	if this.fn == nil {
		completed = true
		return nil, fmt.Errorf("%w: upcaster function is required", ErrInvalid)
	}
	if len(encoded) > limit.MaxBytes {
		completed = true
		return nil, ErrTooLarge
	}
	value, err := invokeCodecDecodeOwned(this.from, encoded, limit)
	if err != nil {
		normalized := normalizeUpcastRuntimeError(err)
		completed = true
		return nil, normalized
	}
	next, err := this.fn(value)
	if err != nil {
		normalized := normalizeUpcastRuntimeError(err)
		completed = true
		return nil, normalized
	}
	result, err = invokeCodecEncodeOwned(this.to, next, limit)
	if err != nil {
		normalized := normalizeUpcastRuntimeError(err)
		completed = true
		return nil, normalized
	}
	if len(result) > limit.MaxBytes {
		completed = true
		return nil, ErrTooLarge
	}
	completed = true
	return result, nil
}

type upcasterDescription struct {
	from        SchemaVersion
	to          SchemaVersion
	sourceCodec CodecID
	targetCodec CodecID
	upcaster    Upcaster
}

func describeUpcaster(upcaster Upcaster) (descriptor upcasterDescription, err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			descriptor = upcasterDescription{}
			err = fmt.Errorf("%w: upcaster descriptor panicked", ErrInvalid)
		}
	}()
	if nilInterface(upcaster) {
		completed = true
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
		completed = true
		return upcasterDescription{}, fmt.Errorf("%w: upcaster revisions must be adjacent", ErrInvalid)
	}
	if typed, ok := upcaster.(interface{ validateUpcaster() error }); ok {
		if err := typed.validateUpcaster(); err != nil {
			normalized := normalizeDefinitionError(err)
			completed = true
			return upcasterDescription{}, normalized
		}
	}
	completed = true
	return descriptor, nil
}

func invokeUpcaster(upcaster Upcaster, encoded []byte, limit PayloadLimit) (result []byte, err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			result = nil
			err = ErrInvalid
		}
	}()
	result, err = upcaster.upcast(bytes.Clone(encoded), limit)
	if err != nil {
		normalized := normalizeUpcastRuntimeError(err)
		completed = true
		return nil, normalized
	}
	if len(result) > limit.MaxBytes {
		completed = true
		return nil, ErrTooLarge
	}
	completed = true
	return bytes.Clone(result), nil
}

func invokeUpcasterOwned(upcaster Upcaster, encoded []byte, limit PayloadLimit) (result []byte, err error) {
	encoded = encoded[:len(encoded):len(encoded)]
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			result = nil
			err = ErrInvalid
		}
	}()
	if owned, ok := upcaster.(interface {
		upcastOwned([]byte, PayloadLimit) ([]byte, error)
	}); ok {
		result, err = owned.upcastOwned(encoded, limit)
	} else {
		result, err = upcaster.upcast(encoded, limit)
	}
	if err != nil {
		normalized := normalizeUpcastRuntimeError(err)
		completed = true
		return nil, normalized
	}
	if len(result) > limit.MaxBytes {
		completed = true
		return nil, ErrTooLarge
	}
	result = result[:len(result):len(result)]
	completed = true
	return result, nil
}

func invokeUpcasterLimitValidation(upcaster Upcaster, limit PayloadLimit) (err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			err = ErrInvalid
		}
	}()
	err = normalizeDefinitionError(upcaster.validateUpcasterLimit(limit))
	completed = true
	return err
}

func normalizeUpcastRuntimeError(err error) error {
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
		return ErrInvalid
	}
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
