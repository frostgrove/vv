package jobs

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"time"
)

type SchemaVersion uint32

func (this SchemaVersion) IsZero() bool { return this == 0 }

type PayloadLimit struct {
	MaxBytes        int
	MaxDecodedBytes int
	MaxDepth        int
}

func DefaultPayloadLimit() PayloadLimit {
	return PayloadLimit{
		MaxBytes:        DefaultPayloadBytes,
		MaxDecodedBytes: DefaultDecodedBytes,
		MaxDepth:        MaxPayloadDepth,
	}
}

type Codec[P any] interface {
	ID() CodecID
	Version() SchemaVersion
	Encode(P, PayloadLimit) ([]byte, error)
	Decode([]byte, PayloadLimit) (P, error)
}

type EncodedPayload struct {
	codec   CodecID
	version SchemaVersion
	data    []byte
}

func NewEncodedPayload(codec CodecID, version SchemaVersion, data []byte) (EncodedPayload, error) {
	if codec.IsZero() || version.IsZero() {
		return EncodedPayload{}, fmt.Errorf("%w: payload codec and version are required", ErrInvalid)
	}
	if len(data) > MaxPayloadBytes {
		return EncodedPayload{}, ErrTooLarge
	}
	return EncodedPayload{codec: codec, version: version, data: bytes.Clone(data)}, nil
}

func (this EncodedPayload) Codec() CodecID             { return this.codec }
func (this EncodedPayload) Version() SchemaVersion     { return this.version }
func (this EncodedPayload) Bytes() []byte              { return bytes.Clone(this.data) }
func (this EncodedPayload) IsZero() bool               { return this.codec.IsZero() || this.version.IsZero() }
func (this EncodedPayload) encodedBytes() []byte       { return this.data }
func (this EncodedPayload) encodedLength() int         { return len(this.data) }
func (this EncodedPayload) matches(codec CodecID) bool { return this.codec == codec }
func (this EncodedPayload) String() string {
	return fmt.Sprintf("[job payload codec=%s version=%d bytes=%d]", this.codec, this.version, len(this.data))
}
func (this EncodedPayload) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

type codecDescription struct {
	id      CodecID
	version SchemaVersion
}

type stringCodec struct{ version SchemaVersion }

func String(version SchemaVersion) Codec[string] {
	return stringCodec{version: version}
}

func (stringCodec) ID() CodecID                 { return builtinCodecID("string") }
func (this stringCodec) Version() SchemaVersion { return this.version }
func (stringCodec) codecMode() CodecMode        { return SafeCodecMode }

func (stringCodec) Encode(value string, limit PayloadLimit) ([]byte, error) {
	if err := validatePayloadLimit(limit); err != nil {
		return nil, err
	}
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return []byte(strings.Clone(value)), nil
}

func (stringCodec) Decode(encoded []byte, limit PayloadLimit) (string, error) {
	if err := validatePayloadLimit(limit); err != nil {
		return "", err
	}
	if len(encoded) > limit.MaxBytes || len(encoded) > limit.MaxDecodedBytes {
		return "", ErrTooLarge
	}
	return strings.Clone(string(encoded)), nil
}

type bytesCodec struct{ version SchemaVersion }

func Bytes(version SchemaVersion) Codec[[]byte] {
	return bytesCodec{version: version}
}

func (bytesCodec) ID() CodecID                 { return builtinCodecID("bytes") }
func (this bytesCodec) Version() SchemaVersion { return this.version }
func (bytesCodec) codecMode() CodecMode        { return SafeCodecMode }

func (bytesCodec) Encode(value []byte, limit PayloadLimit) ([]byte, error) {
	if err := validatePayloadLimit(limit); err != nil {
		return nil, err
	}
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return bytes.Clone(value), nil
}

func (bytesCodec) Decode(encoded []byte, limit PayloadLimit) ([]byte, error) {
	if err := validatePayloadLimit(limit); err != nil {
		return nil, err
	}
	if len(encoded) > limit.MaxBytes || len(encoded) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return bytes.Clone(encoded), nil
}

type rfc3339UTCCodec struct{ version SchemaVersion }

func RFC3339UTC(version SchemaVersion) Codec[time.Time] {
	return rfc3339UTCCodec{version: version}
}

func (rfc3339UTCCodec) ID() CodecID                 { return builtinCodecID("time-rfc3339-utc") }
func (this rfc3339UTCCodec) Version() SchemaVersion { return this.version }
func (rfc3339UTCCodec) codecMode() CodecMode        { return SafeCodecMode }

func (rfc3339UTCCodec) Encode(value time.Time, limit PayloadLimit) ([]byte, error) {
	if err := validatePayloadLimit(limit); err != nil {
		return nil, err
	}
	if int(reflect.TypeFor[time.Time]().Size()) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	value = value.UTC()
	if value.Year() < 0 || value.Year() > 9999 {
		return nil, ErrInvalid
	}
	encoded := value.AppendFormat(make([]byte, 0, 30), time.RFC3339Nano)
	if len(encoded) > limit.MaxBytes {
		return nil, ErrTooLarge
	}
	return encoded, nil
}

func (rfc3339UTCCodec) Decode(encoded []byte, limit PayloadLimit) (time.Time, error) {
	if err := validatePayloadLimit(limit); err != nil {
		return time.Time{}, err
	}
	if len(encoded) > limit.MaxBytes || int(reflect.TypeFor[time.Time]().Size()) > limit.MaxDecodedBytes {
		return time.Time{}, ErrTooLarge
	}
	if !validRFC3339UTC(encoded) {
		return time.Time{}, ErrCorrupt
	}
	text := string(encoded)
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || value.UTC().Format(time.RFC3339Nano) != text {
		return time.Time{}, ErrCorrupt
	}
	return value.UTC(), nil
}

func validRFC3339UTC(encoded []byte) bool {
	length := len(encoded)
	if length != 20 && (length < 22 || length > 30) {
		return false
	}
	if encoded[4] != '-' || encoded[7] != '-' || encoded[10] != 'T' || encoded[13] != ':' || encoded[16] != ':' || encoded[length-1] != 'Z' {
		return false
	}
	if length > 20 && encoded[19] != '.' {
		return false
	}
	for index, value := range encoded[:length-1] {
		if index == 4 || index == 7 || index == 10 || index == 13 || index == 16 || index == 19 && length > 20 {
			continue
		}
		if value < '0' || value > '9' {
			return false
		}
	}
	return true
}

func describeCodec[P any](codec Codec[P]) (descriptor codecDescription, err error) {
	defer func() {
		if recover() != nil {
			descriptor = codecDescription{}
			err = fmt.Errorf("%w: codec descriptor panicked", ErrInvalid)
		}
	}()
	if nilInterface(codec) {
		return codecDescription{}, fmt.Errorf("%w: codec is required", ErrInvalid)
	}
	if validator, ok := any(codec).(interface{ validateCodec() error }); ok {
		if err := validator.validateCodec(); err != nil {
			return codecDescription{}, normalizeDefinitionError(err)
		}
	}
	id := codec.ID()
	version := codec.Version()
	if id.IsZero() || version.IsZero() {
		return codecDescription{}, fmt.Errorf("%w: codec id and version are required", ErrInvalid)
	}
	return codecDescription{id: id, version: version}, nil
}

func validateCodecPayloadLimit[P any](codec Codec[P], limit PayloadLimit) error {
	if err := validatePayloadLimit(limit); err != nil {
		return err
	}
	if validator, ok := any(codec).(interface{ validateCodecLimit(PayloadLimit) error }); ok {
		return normalizeDefinitionError(validator.validateCodecLimit(limit))
	}
	return nil
}

func validatePayloadLimit(limit PayloadLimit) error {
	if limit.MaxBytes <= 0 || limit.MaxBytes > MaxPayloadBytes || limit.MaxDecodedBytes <= 0 || limit.MaxDecodedBytes > MaxDecodedBytes || limit.MaxDepth <= 0 || limit.MaxDepth > MaxPayloadDepth {
		return fmt.Errorf("%w: payload limits are outside supported bounds", ErrInvalid)
	}
	return nil
}

func builtinCodecID(value string) CodecID {
	id, err := ParseCodecID(value)
	if err != nil {
		panic(err)
	}
	return id
}

func nilInterface(value any) bool {
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
