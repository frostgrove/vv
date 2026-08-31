package cache

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	MaxNamespacePartBytes = 128
	MaxEncodedKeyBytes    = 64 << 10
)

type Generation uint32
type KeyVersion uint32
type ValueSchema uint32

type Namespace struct {
	digest      [32]byte
	application string
	environment string
	purpose     string
	generation  Generation
}

func NamespaceOf(application, environment, purpose string, generation Generation) (Namespace, error) {
	parts := []string{application, environment, purpose}
	for _, part := range parts {
		if err := validNamespacePart(part); err != nil {
			return Namespace{}, failure("build namespace", err)
		}
	}
	if generation == 0 {
		return Namespace{}, failure("build namespace", fmt.Errorf("%w: generation is zero", ErrInvalid))
	}
	h := sha256.New()
	var size [4]byte
	for _, part := range parts {
		binary.BigEndian.PutUint32(size[:], uint32(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(part))
	}
	binary.BigEndian.PutUint32(size[:], uint32(generation))
	_, _ = h.Write(size[:])
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return Namespace{
		digest:      digest,
		application: strings.Clone(application),
		environment: strings.Clone(environment),
		purpose:     strings.Clone(purpose),
		generation:  generation,
	}, nil
}

func MustNamespace(application, environment, purpose string, generation Generation) Namespace {
	namespace, err := NamespaceOf(application, environment, purpose, generation)
	if err != nil {
		panic(err)
	}
	return namespace
}

func (this Namespace) String() string { return "[cache namespace]" }

func (this Namespace) valid() bool {
	return this.digest != [32]byte{} && this.generation != 0 &&
		validNamespacePart(this.application) == nil &&
		validNamespacePart(this.environment) == nil &&
		validNamespacePart(this.purpose) == nil
}

func validNamespacePart(value string) error {
	if value == "" || len(value) > MaxNamespacePartBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: namespace component has invalid length or surrounding whitespace", ErrInvalid)
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e {
			return fmt.Errorf("%w: namespace component contains a control or non-ASCII character", ErrInvalid)
		}
	}
	return nil
}

type Address struct {
	NamespaceDigest [32]byte
	PartitionDigest [32]byte
	KeyDigest       [32]byte
}

func (this Address) String() string { return "[cache address]" }

func addressOf[K any](scope Scope[K], codec KeyCodec[K], version KeyVersion, key K, maxKeyBytes int) (Address, int, error) {
	if !scope.valid() || nilInterface(codec) {
		return Address{}, 0, failure("encode key", fmt.Errorf("%w: scope or key codec is invalid", ErrInvalid))
	}
	if version == 0 {
		return Address{}, 0, failure("encode key", fmt.Errorf("%w: key version is zero", ErrInvalid))
	}
	raw, err := invokeKeyEncode(codec, key, KeyLimit{MaxBytes: maxKeyBytes})
	if err != nil {
		return Address{}, 0, failure("encode key", err)
	}
	if maxKeyBytes <= 0 || len(raw) == 0 || len(raw) > maxKeyBytes {
		return Address{}, 0, failure("encode key", fmt.Errorf("%w: encoded key has %d bytes", ErrTooLarge, len(raw)))
	}
	partitionLimit := maxKeyBytes - len(raw)
	if scope.global {
		partitionLimit = maxKeyBytes
	}
	partition, partitionBytes, err := scope.partitionOf(key, KeyLimit{MaxBytes: partitionLimit})
	if err != nil {
		return Address{}, 0, failure("partition key", err)
	}
	var versionBytes [4]byte
	binary.BigEndian.PutUint32(versionBytes[:], uint32(version))
	digest := sha256.New()
	_, _ = digest.Write(versionBytes[:])
	_, _ = digest.Write(raw)
	var keyDigest [32]byte
	copy(keyDigest[:], digest.Sum(nil))
	return Address{
		NamespaceDigest: scope.namespace.digest,
		PartitionDigest: partition,
		KeyDigest:       keyDigest,
	}, len(raw) + partitionBytes, nil
}

func invokeKeyEncode[K any](codec KeyCodec[K], key K, limit KeyLimit) (encoded []byte, err error) {
	defer func() {
		if recover() != nil {
			encoded = nil
			err = fmt.Errorf("key codec panicked")
		}
	}()
	encoded, err = codec.Encode(key, limit)
	if err != nil {
		encoded = nil
		err = sanitizedError(err, ErrInvalid)
	}
	return encoded, err
}
