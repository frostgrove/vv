package cache

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
)

type KeyCodec[K any] interface {
	Version() KeyVersion
	Encode(K, KeyLimit) ([]byte, error)
}

type KeyLimit struct {
	MaxBytes int
}

type keyCodec[K any] struct {
	version KeyVersion
	encode  func(K, KeyLimit) ([]byte, error)
}

func KeyFunc[K any](version KeyVersion, encode func(K, KeyLimit) ([]byte, error)) (KeyCodec[K], error) {
	if version == 0 || encode == nil {
		return nil, failure("build key codec", fmt.Errorf("%w: version and encoder are required", ErrInvalid))
	}
	return keyCodec[K]{version: version, encode: encode}, nil
}

func MustKeyFunc[K any](version KeyVersion, encode func(K, KeyLimit) ([]byte, error)) KeyCodec[K] {
	codec, err := KeyFunc(version, encode)
	if err != nil {
		panic(err)
	}
	return codec
}

func (this keyCodec[K]) Version() KeyVersion { return this.version }

func (this keyCodec[K]) Encode(key K, limit KeyLimit) ([]byte, error) {
	if limit.MaxBytes <= 0 || limit.MaxBytes > MaxEncodedKeyBytes {
		return nil, fmt.Errorf("%w: key limit is invalid", ErrInvalid)
	}
	encoded, err := this.encode(key, limit)
	if err != nil {
		return nil, err
	}
	if len(encoded) == 0 || len(encoded) > limit.MaxBytes {
		return nil, fmt.Errorf("%w: encoded key has %d bytes", ErrTooLarge, len(encoded))
	}
	return append([]byte(nil), encoded...), nil
}

type hmacKeyCodec[K any] struct {
	inner  KeyCodec[K]
	secret []byte
}

func HMACKey[K any](inner KeyCodec[K], secret []byte) (KeyCodec[K], error) {
	if nilInterface(inner) || len(secret) < 32 {
		return nil, failure("build HMAC key codec", fmt.Errorf("%w: codec and a secret of at least 32 bytes are required", ErrInvalid))
	}
	return hmacKeyCodec[K]{inner: inner, secret: append([]byte(nil), secret...)}, nil
}

func (this hmacKeyCodec[K]) Version() KeyVersion { return this.inner.Version() }

func (this hmacKeyCodec[K]) Encode(key K, limit KeyLimit) ([]byte, error) {
	if limit.MaxBytes < sha256.Size {
		return nil, ErrTooLarge
	}
	raw, err := this.inner.Encode(key, limit)
	if err != nil {
		return nil, err
	}
	h := hmac.New(sha256.New, this.secret)
	_, _ = h.Write(raw)
	return h.Sum(nil), nil
}

type Partitioner[K any] func(K, KeyLimit) ([]byte, error)

type Scope[K any] struct {
	namespace Namespace
	partition Partitioner[K]
	global    bool
}

func Global[K any](namespace Namespace) Scope[K] {
	return Scope[K]{namespace: namespace, global: true}
}

func Partitioned[K any](namespace Namespace, partition Partitioner[K]) Scope[K] {
	return Scope[K]{namespace: namespace, partition: partition}
}

func (this Scope[K]) valid() bool {
	return this.namespace.valid() && (this.global || this.partition != nil)
}

func (this Scope[K]) partitionOf(key K, limit KeyLimit) (digest [32]byte, size int, err error) {
	defer func() {
		if recover() != nil {
			digest = [32]byte{}
			size = 0
			err = fmt.Errorf("partitioner panicked")
		}
	}()
	if this.global {
		return [32]byte{}, 0, nil
	}
	if limit.MaxBytes <= 0 || limit.MaxBytes > MaxEncodedKeyBytes {
		return [32]byte{}, 0, fmt.Errorf("%w: partition limit is invalid", ErrTooLarge)
	}
	raw, err := this.partition(key, limit)
	if err != nil {
		return [32]byte{}, 0, sanitizedError(err, ErrInvalid)
	}
	if len(raw) == 0 || len(raw) > limit.MaxBytes {
		return [32]byte{}, 0, fmt.Errorf("%w: partition has %d encoded bytes", ErrTooLarge, len(raw))
	}
	return sha256.Sum256(raw), len(raw), nil
}
