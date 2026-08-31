package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const MaxCodecIDBytes = 64

type ValueLimit struct {
	MaxBytes        int
	MaxDecodedBytes int
	MaxDepth        int
}

type Codec[V any] interface {
	ID() string
	Schema() ValueSchema
	Encode(V, ValueLimit) ([]byte, error)
	Decode([]byte, ValueLimit) (V, error)
}

type codecDescriptor struct {
	id     string
	schema ValueSchema
}

type stringCodec struct{ schema ValueSchema }

func String(schema ValueSchema) Codec[string] {
	return stringCodec{schema: schema}
}

func (this stringCodec) ID() string          { return "string" }
func (this stringCodec) Schema() ValueSchema { return this.schema }

func (this stringCodec) Encode(value string, limit ValueLimit) ([]byte, error) {
	if err := validateValueLimit(limit); err != nil {
		return nil, err
	}
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return []byte(strings.Clone(value)), nil
}

func (this stringCodec) Decode(encoded []byte, limit ValueLimit) (string, error) {
	if err := validateValueLimit(limit); err != nil {
		return "", err
	}
	if len(encoded) > limit.MaxBytes || len(encoded) > limit.MaxDecodedBytes {
		return "", ErrTooLarge
	}
	return strings.Clone(string(encoded)), nil
}

type bytesCodec struct{ schema ValueSchema }

func Bytes(schema ValueSchema) Codec[[]byte] {
	return bytesCodec{schema: schema}
}

func (this bytesCodec) ID() string          { return "bytes" }
func (this bytesCodec) Schema() ValueSchema { return this.schema }

func (this bytesCodec) Encode(value []byte, limit ValueLimit) ([]byte, error) {
	if err := validateValueLimit(limit); err != nil {
		return nil, err
	}
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return bytes.Clone(value), nil
}

func (this bytesCodec) Decode(encoded []byte, limit ValueLimit) ([]byte, error) {
	if err := validateValueLimit(limit); err != nil {
		return nil, err
	}
	if len(encoded) > limit.MaxBytes || len(encoded) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return bytes.Clone(encoded), nil
}

type jsonCodec[V any] struct{ schema ValueSchema }

func JSON[V any](schema ValueSchema) Codec[V] {
	return jsonCodec[V]{schema: schema}
}

func (this jsonCodec[V]) ID() string          { return "json" }
func (this jsonCodec[V]) Schema() ValueSchema { return this.schema }

func (this jsonCodec[V]) Encode(value V, limit ValueLimit) ([]byte, error) {
	if err := validateValueLimit(limit); err != nil {
		return nil, err
	}
	buffer := &limitedBuffer{remaining: limit.MaxBytes}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		if buffer.exceeded {
			return nil, ErrTooLarge
		}
		return nil, err
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	if err := validateJSONDepth(encoded, limit.MaxDepth); err != nil {
		return nil, err
	}
	return bytes.Clone(encoded), nil
}

func (this jsonCodec[V]) Decode(encoded []byte, limit ValueLimit) (V, error) {
	var value V
	if err := validateValueLimit(limit); err != nil {
		return value, err
	}
	if len(encoded) > limit.MaxBytes || len(encoded) > limit.MaxDecodedBytes {
		return value, ErrTooLarge
	}
	if err := validateJSONDepth(encoded, limit.MaxDepth); err != nil {
		return value, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return value, fmt.Errorf("multiple JSON values")
		}
		return value, err
	}
	return value, nil
}

func validCodec[V any](codec Codec[V]) error {
	_, err := describeCodec(codec)
	return err
}

func describeCodec[V any](codec Codec[V]) (descriptor codecDescriptor, err error) {
	defer func() {
		if recover() != nil {
			descriptor = codecDescriptor{}
			err = fmt.Errorf("%w: codec descriptor panicked", ErrInvalid)
		}
	}()
	if nilInterface(codec) {
		return codecDescriptor{}, fmt.Errorf("%w: codec or value schema is invalid", ErrInvalid)
	}
	schema := codec.Schema()
	if schema == 0 {
		return codecDescriptor{}, fmt.Errorf("%w: codec or value schema is invalid", ErrInvalid)
	}
	id := codec.ID()
	if id == "" || len(id) > MaxCodecIDBytes || strings.TrimSpace(id) != id {
		return codecDescriptor{}, fmt.Errorf("%w: codec id is invalid", ErrInvalid)
	}
	for _, r := range id {
		if r < 0x21 || r > 0x7e {
			return codecDescriptor{}, fmt.Errorf("%w: codec id contains a control or non-ASCII character", ErrInvalid)
		}
	}
	return codecDescriptor{id: strings.Clone(id), schema: schema}, nil
}

func validateValueLimit(limit ValueLimit) error {
	if limit.MaxBytes <= 0 || limit.MaxDecodedBytes <= 0 || limit.MaxDepth <= 0 {
		return fmt.Errorf("%w: value limits must be positive", ErrInvalid)
	}
	return nil
}

type limitedBuffer struct {
	bytes.Buffer
	remaining int
	exceeded  bool
}

func (this *limitedBuffer) Write(data []byte) (int, error) {
	if len(data) > this.remaining {
		this.exceeded = true
		return 0, ErrTooLarge
	}
	n, err := this.Buffer.Write(data)
	this.remaining -= n
	return n, err
}

func validateJSONDepth(encoded []byte, maximum int) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			continue
		}
		switch delimiter {
		case '{', '[':
			depth++
			if depth > maximum {
				return ErrTooLarge
			}
		case '}', ']':
			depth--
			if depth < 0 {
				return fmt.Errorf("unbalanced JSON structure")
			}
		}
	}
}
