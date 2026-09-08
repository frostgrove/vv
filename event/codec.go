package event

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// The second extension point, and the one that decides what a payload may be:
// whether a type can be encoded is answered here and never by a rule the kernel
// holds. Three obligations an implementation cannot be checked against and is
// therefore told:
//
//	The framework calls all three methods from every request goroutine
//	concurrently, and each must be safe for that.
//
//	Decode may return a value that aliases the payload it was given and may
//	return freshly allocated memory. It must not return one that aliases memory
//	the codec itself will write or reuse — Fact.RoundTrip is the runnable proxy
//	and refuses a codec that does.
//
//	CanEncode answers for the reader type alone and is asked at declaration, so
//	it must be constant and cheap; the shipped codec charges its answer when the
//	codec value is built.
//
// A panic out of any of the three is recovered into the refusal that method's
// own error produces — ErrEncode, ErrPayload and ErrCodecType. That is the whole
// rule for every extension point: the kernel recovers a panic from an extension
// that has a stated refusal channel for the same failure and maps it to the
// sentinel that channel already produces, and it recovers nothing else. So an
// upcaster's panic is recovered into ErrUpcast, and a payload type's own Equal —
// which Fact.RoundTrip asks of a struct whose every field is unexported, and
// there alone — into ErrSample, because a sample the comparison could not be
// made for is what ErrSample already says. A fold's, an identity mapper's and a
// store's are not recovered: the first two have no error channel to be a second
// spelling of, and a store has both an error channel and an Outcome vocabulary,
// so recovering it would be the kernel classifying a failure the store did not
// classify.
type Codec[V any] interface {
	Encode(V) ([]byte, error)
	Decode([]byte) (V, error)
	CanEncode() error
}

func JSON[V any]() Codec[V] {
	return jsonCodec[V]{charged: chargeJSON(reflect.TypeFor[V]())}
}

type jsonCodec[V any] struct{ charged error }

func (this jsonCodec[V]) CanEncode() error { return this.charged }

// The pointer is not incidental. It makes the value addressable, which is what
// lets encoding/json reach a marshaller declared on a pointer receiver — the
// ordinary Go idiom. Without it such a type is written field by field, as {} for
// most of them, and Decode, which has always passed a pointer, reads a zero
// value back with no error at any door.
func (jsonCodec[V]) Encode(value V) ([]byte, error) { return json.Marshal(&value) }

func (jsonCodec[V]) Decode(payload []byte) (V, error) {
	var value V
	err := json.Unmarshal(payload, &value)
	return value, err
}

func typeNameOf[V any]() string { return reflect.TypeFor[V]().String() }

func encodeWith[V any](codec Codec[V], value V) (encoded []byte, err error) {
	defer func() {
		if recover() != nil {
			encoded, err = nil, newRefusal(ErrEncode, nil, nil)
		}
	}()
	encoded, err = codec.Encode(value)
	if err != nil {
		return nil, newRefusal(ErrEncode, nil, err)
	}
	return encoded, nil
}

func decodeWith[V any](codec Codec[V], payload []byte) (value V, err error) {
	defer func() {
		if recover() != nil {
			var none V
			value, err = none, newRefusal(ErrPayload, nil, nil)
		}
	}()
	value, err = codec.Decode(payload)
	if err != nil {
		var none V
		return none, newRefusal(ErrPayload, nil, err)
	}
	return value, nil
}

func canEncodeWith[V any](codec Codec[V], revision int) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("%w: revision %d's codec panicked when it was asked", ErrCodecType, revision)
		}
	}()
	if answer := codec.CanEncode(); answer != nil {
		return fmt.Errorf("%w: revision %d: %s", ErrCodecType, revision, answer)
	}
	return nil
}
