package event

import "fmt"

// A chain's position is the revision: From is revision 1 and each Then the
// next, so there is no revision number to type, duplicate or leave a gap in,
// and a chain always starts at 1. Neither call returns an error — a chain is
// written as one expression at package level — so a defect is carried here and
// surfaced by the declaration that reads it.
type Chain[V any] struct {
	codec Codec[V]
	links []link[V]
	err   error
}

// One retained revision, erased to the current type. decode is that revision's
// own codec followed by every declared upcaster up to V; the three self readers
// stay over the revision's own type and pass values as any, because the aliasing
// half of a round trip has to compare two encodings of one value made by the
// codec that wrote the bytes — an upcaster stands between the aliased buffer and
// the current type, and it copies, so a comparison made after one proves nothing
// about any revision but the last.
type link[V any] struct {
	typeName   string
	accepts    func(any) bool
	decode     func([]byte) (V, error)
	selfEncode func(any) ([]byte, error)
	selfDecode func([]byte) (any, error)
	selfZero   func() ([]byte, error)
}

func From[V any](codec Codec[V]) Chain[V] {
	if nilByAnyRoute(codec) {
		return Chain[V]{err: fmt.Errorf("%w: revision 1 declares no codec", ErrDeclaration)}
	}
	if err := canEncodeWith(codec, 1); err != nil {
		return Chain[V]{err: err}
	}
	return Chain[V]{codec: codec, links: []link[V]{linkOf(codec)}}
}

func Then[A, B any](prev Chain[A], codec Codec[B], up func(A) (B, error)) Chain[B] {
	revision := len(prev.links) + 1
	switch {
	case prev.err != nil:
		return Chain[B]{err: prev.err}
	case len(prev.links) == 0:
		return Chain[B]{err: fmt.Errorf("%w: a chain begins at From and this one begins at Then", ErrDeclaration)}
	case nilByAnyRoute(codec):
		return Chain[B]{err: fmt.Errorf("%w: revision %d declares no codec", ErrDeclaration, revision)}
	case up == nil:
		return Chain[B]{err: fmt.Errorf("%w: revision %d declares no upcaster", ErrDeclaration, revision)}
	}
	if err := canEncodeWith(codec, revision); err != nil {
		return Chain[B]{err: err}
	}
	links := make([]link[B], 0, revision)
	for _, previous := range prev.links {
		links = append(links, carry(previous, up))
	}
	return Chain[B]{codec: codec, links: append(links, linkOf(codec))}
}

func linkOf[V any](codec Codec[V]) link[V] {
	return link[V]{
		typeName:   typeNameOf[V](),
		accepts:    func(sample any) bool { _, is := sample.(V); return is },
		decode:     func(payload []byte) (V, error) { return decodeWith(codec, payload) },
		selfEncode: func(sample any) ([]byte, error) { typed, _ := sample.(V); return encodeWith(codec, typed) },
		selfDecode: func(payload []byte) (any, error) { return decodeWith(codec, payload) },
		selfZero:   func() ([]byte, error) { var zero V; return encodeWith(codec, zero) },
	}
}

func carry[A, B any](previous link[A], up func(A) (B, error)) link[B] {
	return link[B]{
		typeName:   previous.typeName,
		accepts:    previous.accepts,
		selfEncode: previous.selfEncode,
		selfDecode: previous.selfDecode,
		selfZero:   previous.selfZero,
		decode: func(payload []byte) (B, error) {
			value, err := previous.decode(payload)
			if err != nil {
				var none B
				return none, err
			}
			return upcastTo(up, value)
		},
	}
}

func upcastTo[A, B any](up func(A) (B, error), value A) (result B, err error) {
	defer func() {
		if recover() != nil {
			var none B
			result, err = none, upcastRefusal(nil)
		}
	}()
	result, err = up(value)
	if err != nil {
		var none B
		return none, upcastRefusal(err)
	}
	return result, nil
}
