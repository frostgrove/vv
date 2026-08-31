package cache

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

type structKeyFirst struct {
	Tenant uint64 `cachekey:"tenant"`
	Name   string `cachekey:"name"`
	Skip   string `cachekey:"-"`
}

type structKeyReordered struct {
	Renamed string `cachekey:"name"`
	Owner   uint64 `cachekey:"tenant"`
}

type structKeyNestedValue struct {
	Region string  `cachekey:"region"`
	Digest [4]byte `cachekey:"digest"`
}

type structKeyNested struct {
	When  time.Time            `cachekey:"when"`
	Value structKeyNestedValue `cachekey:"value"`
	Data  []byte               `cachekey:"data"`
}

func TestStructKeyUsesStableTagsInsteadOfFieldOrderOrNames(t *testing.T) {
	first := MustStructKey[structKeyFirst](1)
	reordered := MustStructKey[structKeyReordered](1)
	left, err := first.Encode(structKeyFirst{Tenant: 42, Name: "document", Skip: "left"}, KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	right, err := reordered.Encode(structKeyReordered{Owner: 42, Renamed: "document"}, KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatalf("encodings differ: %x != %x", left, right)
	}
	changed, err := first.Encode(structKeyFirst{Tenant: 43, Name: "document"}, KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(left, changed) {
		t.Fatal("different key values collided")
	}
}

func TestStructKeyEncodesNestedValuesAndDistinguishesNilBytes(t *testing.T) {
	codec := MustStructKey[structKeyNested](2)
	base := structKeyNested{
		When: time.Unix(1_900_000_000, 123).UTC(),
		Value: structKeyNestedValue{
			Region: "north",
			Digest: [4]byte{1, 2, 3, 4},
		},
	}
	nilBytes, err := codec.Encode(base, KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	base.Data = []byte{}
	emptyBytes, err := codec.Encode(base, KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(nilBytes, emptyBytes) {
		t.Fatal("nil and empty byte slices collided")
	}
}

func TestStructKeyEnforcesBoundsAndRejectsUnrepresentableTime(t *testing.T) {
	codec := MustStructKey[structKeyNested](1)
	key := structKeyNested{When: time.Unix(1_900_000_000, 0).UTC(), Data: []byte("payload")}
	if encoded, err := codec.Encode(key, KeyLimit{MaxBytes: 8}); !errors.Is(err, ErrTooLarge) || encoded != nil {
		t.Fatalf("bounded encoding = %x, error = %v", encoded, err)
	}
	key.When = time.Time{}
	if encoded, err := codec.Encode(key, KeyLimit{MaxBytes: 1024}); !errors.Is(err, ErrInvalid) || encoded != nil {
		t.Fatalf("zero time encoding = %x, error = %v", encoded, err)
	}
}

func TestStructKeyRejectsAmbiguousOrUnstableShapes(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "zero version", run: func() error { _, err := StructKey[structKeyFirst](0); return err }},
		{name: "not struct", run: func() error { _, err := StructKey[string](1); return err }},
		{name: "missing tag", run: func() error {
			type key struct{ Value string }
			_, err := StructKey[key](1)
			return err
		}},
		{name: "duplicate tag", run: func() error {
			type key struct {
				First  string `cachekey:"same"`
				Second string `cachekey:"same"`
			}
			_, err := StructKey[key](1)
			return err
		}},
		{name: "all ignored", run: func() error {
			type key struct {
				Value string `cachekey:"-"`
			}
			_, err := StructKey[key](1)
			return err
		}},
		{name: "map", run: func() error {
			type key struct {
				Value map[string]string `cachekey:"value"`
			}
			_, err := StructKey[key](1)
			return err
		}},
		{name: "pointer", run: func() error {
			type key struct {
				Value *string `cachekey:"value"`
			}
			_, err := StructKey[key](1)
			return err
		}},
		{name: "float", run: func() error {
			type key struct {
				Value float64 `cachekey:"value"`
			}
			_, err := StructKey[key](1)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("StructKey() error = %v", err)
			}
		})
	}
}
