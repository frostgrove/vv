package otelnative

import (
	"errors"
	"math"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
)

var (
	ErrInvalidProjectionPolicy = errors.New("otelnative: projection policy is invalid")
	ErrNativeAssembly          = errors.New("otelnative: native integration assembly failed")
)

const (
	maxNativeProjectionTables              = 64
	maxNativeProjectionResourceAttributes  = 32
	maxNativeProjectionAttributeKeyBytes   = 128
	maxNativeProjectionAttributeValueBytes = 512
	maxNativeProjectionAttributeSliceItems = 32
	maxNativeProjectionAttributeDepth      = 4
	maxNativeProjectionAttributeBytes      = 16 << 10
	maxNativeDatabasePools                 = 256
)

type nativeAssemblyResult[T any] struct {
	value T
	err   error
}

func runNativeAssembly[T any](assemble func() (T, error)) (T, error) {
	results := make(chan nativeAssemblyResult[T], 1)
	go func() {
		completed := false
		defer func() {
			if completed {
				return
			}
			_ = recover()
			var zero T
			results <- nativeAssemblyResult[T]{value: zero, err: ErrNativeAssembly}
		}()
		value, err := assemble()
		completed = true
		results <- nativeAssemblyResult[T]{value: value, err: err}
	}()
	result := <-results
	if result.err != nil {
		var zero T
		return zero, result.err
	}
	if nilInterface(result.value) {
		var zero T
		return zero, ErrNativeAssembly
	}
	return result.value, nil
}

func compileNativeResources(input []attribute.KeyValue) (map[string]attribute.Value, error) {
	if len(input) > maxNativeProjectionResourceAttributes {
		return nil, ErrInvalidProjectionPolicy
	}
	result := make(map[string]attribute.Value, len(input))
	remaining := maxNativeProjectionAttributeBytes
	for _, item := range input {
		key := string(item.Key)
		if !item.Valid() || len(key) == 0 || len(key) > maxNativeProjectionAttributeKeyBytes || !utf8.ValidString(key) || !consumeNativeProjectionBytes(&remaining, len(key)) || !validNativeProjectionValue(item.Value, 0, &remaining) {
			return nil, ErrInvalidProjectionPolicy
		}
		if _, duplicate := result[key]; duplicate {
			return nil, ErrInvalidProjectionPolicy
		}
		result[key] = cloneNativeAttributeValue(item.Value)
	}
	return result, nil
}

func validNativeProjectionValue(value attribute.Value, depth int, remaining *int) bool {
	switch value.Type() {
	case attribute.BOOL, attribute.INT64:
		return consumeNativeProjectionBytes(remaining, 8)
	case attribute.FLOAT64:
		number := value.AsFloat64()
		return !math.IsNaN(number) && !math.IsInf(number, 0) && consumeNativeProjectionBytes(remaining, 8)
	case attribute.STRING:
		candidate := value.AsString()
		return len(candidate) <= maxNativeProjectionAttributeValueBytes && utf8.ValidString(candidate) && consumeNativeProjectionBytes(remaining, len(candidate))
	case attribute.BOOLSLICE:
		candidate := value.AsBoolSlice()
		return len(candidate) <= maxNativeProjectionAttributeSliceItems && consumeNativeProjectionBytes(remaining, len(candidate))
	case attribute.INT64SLICE:
		candidate := value.AsInt64Slice()
		return len(candidate) <= maxNativeProjectionAttributeSliceItems && consumeNativeProjectionBytes(remaining, len(candidate)*8)
	case attribute.FLOAT64SLICE:
		candidate := value.AsFloat64Slice()
		if len(candidate) > maxNativeProjectionAttributeSliceItems || !consumeNativeProjectionBytes(remaining, len(candidate)*8) {
			return false
		}
		for _, number := range candidate {
			if math.IsNaN(number) || math.IsInf(number, 0) {
				return false
			}
		}
		return true
	case attribute.STRINGSLICE:
		candidate := value.AsStringSlice()
		if len(candidate) > maxNativeProjectionAttributeSliceItems {
			return false
		}
		for _, item := range candidate {
			if len(item) > maxNativeProjectionAttributeValueBytes || !utf8.ValidString(item) || !consumeNativeProjectionBytes(remaining, len(item)) {
				return false
			}
		}
		return true
	case attribute.BYTESLICE:
		candidate := value.AsByteSlice()
		return len(candidate) <= maxNativeProjectionAttributeValueBytes && consumeNativeProjectionBytes(remaining, len(candidate))
	case attribute.SLICE:
		candidate := value.AsSlice()
		if depth >= maxNativeProjectionAttributeDepth || len(candidate) > maxNativeProjectionAttributeSliceItems {
			return false
		}
		for _, item := range candidate {
			if !validNativeProjectionValue(item, depth+1, remaining) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func consumeNativeProjectionBytes(remaining *int, count int) bool {
	if remaining == nil || count < 0 || count > *remaining {
		return false
	}
	*remaining -= count
	return true
}

func cloneNativeAttributeValue(input attribute.Value) attribute.Value {
	switch input.Type() {
	case attribute.BOOLSLICE:
		return attribute.BoolSliceValue(input.AsBoolSlice())
	case attribute.INT64SLICE:
		return attribute.Int64SliceValue(input.AsInt64Slice())
	case attribute.FLOAT64SLICE:
		return attribute.Float64SliceValue(input.AsFloat64Slice())
	case attribute.STRINGSLICE:
		return attribute.StringSliceValue(input.AsStringSlice())
	case attribute.BYTESLICE:
		return attribute.ByteSliceValue(input.AsByteSlice())
	case attribute.SLICE:
		values := input.AsSlice()
		for index := range values {
			values[index] = cloneNativeAttributeValue(values[index])
		}
		return attribute.SliceValue(values...)
	default:
		return input
	}
}
