package audit_test

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
)

func TestBuiltInCodecsRoundTripCanonicalValues(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "text", run: func(t *testing.T) {
			codec := audit.Text()
			wire, err := codec.Encode("Привет")
			if err != nil || string(wire) != "Привет" {
				t.Fatalf("Encode = (%q, %v)", wire, err)
			}
			value, err := codec.Decode(1, wire)
			if err != nil || value != "Привет" {
				t.Fatalf("Decode = (%q, %v)", value, err)
			}
		}},
		{name: "bool", run: func(t *testing.T) {
			codec := audit.Bool()
			if value, err := codec.Decode(1, []byte{1}); err != nil || !value {
				t.Fatalf("Decode true = (%v, %v)", value, err)
			}
			if _, err := codec.Decode(1, []byte{2}); !errors.Is(err, audit.ErrInvalid) {
				t.Fatalf("noncanonical bool = %v", err)
			}
		}},
		{name: "int64", run: func(t *testing.T) {
			codec := audit.Int64()
			wire, err := codec.Encode(math.MinInt64)
			if err != nil {
				t.Fatal(err)
			}
			value, err := codec.Decode(1, wire)
			if err != nil || value != math.MinInt64 {
				t.Fatalf("round trip = (%d, %v)", value, err)
			}
		}},
		{name: "uint64", run: func(t *testing.T) {
			codec := audit.Uint64()
			wire, err := codec.Encode(math.MaxUint64)
			if err != nil {
				t.Fatal(err)
			}
			value, err := codec.Decode(1, wire)
			if err != nil || value != math.MaxUint64 {
				t.Fatalf("round trip = (%d, %v)", value, err)
			}
		}},
		{name: "decimal", run: func(t *testing.T) {
			codec := audit.DecimalText()
			for _, value := range []string{"0", "1", "-1", "10.25", "-10.01"} {
				wire, err := codec.Encode(value)
				if err != nil || string(wire) != value {
					t.Fatalf("Encode(%q) = (%q, %v)", value, wire, err)
				}
			}
			for _, value := range []string{"", "-0", "+1", "01", "0.01", "1.0", "1e2", " 1"} {
				if _, err := codec.Encode(value); !errors.Is(err, audit.ErrInvalid) {
					t.Fatalf("Encode(%q) = %v", value, err)
				}
			}
		}},
		{name: "bytes", run: func(t *testing.T) {
			codec := audit.Bytes()
			input := []byte{1, 2, 3}
			wire, err := codec.Encode(input)
			if err != nil {
				t.Fatal(err)
			}
			input[0] = 9
			if wire[0] != 1 {
				t.Fatal("encoded bytes alias their input")
			}
			decoded, err := codec.Decode(1, wire)
			if err != nil {
				t.Fatal(err)
			}
			decoded[0] = 8
			again, _ := codec.Decode(1, wire)
			if again[0] != 1 {
				t.Fatal("decoded bytes alias a prior result")
			}
		}},
		{name: "reference", run: func(t *testing.T) {
			codec := audit.ReferenceText()
			if _, err := codec.Encode(audit.Reference("tenant\tsecret")); !errors.Is(err, audit.ErrInvalid) {
				t.Fatalf("control-bearing reference = %v", err)
			}
		}},
		{name: "uuid", run: func(t *testing.T) {
			codec := audit.UUIDReference()
			value := audit.Reference("00112233-4455-6677-8899-aabbccddeeff")
			wire, err := codec.Encode(value)
			if err != nil || string(wire) != string(value) {
				t.Fatalf("Encode = (%q, %v)", wire, err)
			}
			if _, err := codec.Decode(1, []byte("00112233-4455-6677-8899-AABBCCDDEEFF")); !errors.Is(err, audit.ErrInvalid) {
				t.Fatalf("uppercase UUID = %v", err)
			}
		}},
		{name: "duration", run: func(t *testing.T) {
			codec := audit.Duration()
			wire, err := codec.Encode(-17 * time.Second)
			if err != nil {
				t.Fatal(err)
			}
			value, err := codec.Decode(1, wire)
			if err != nil || value != -17*time.Second {
				t.Fatalf("round trip = (%s, %v)", value, err)
			}
		}},
		{name: "time", run: func(t *testing.T) {
			codec := audit.Time()
			input := time.Date(2026, 9, 9, 12, 13, 14, 15, time.FixedZone("local", 6*60*60))
			wire, err := codec.Encode(input)
			if err != nil {
				t.Fatal(err)
			}
			value, err := codec.Decode(1, wire)
			if err != nil || !value.Equal(input) || value.Location() != time.UTC {
				t.Fatalf("round trip = (%s, %v)", value, err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

type versionedTextEngine struct {
	scratch []byte
}

func (engine *versionedTextEngine) Encode(value string) ([]byte, error) {
	engine.scratch = append(engine.scratch[:0], value...)
	return engine.scratch, nil
}

func (*versionedTextEngine) Decode(version audit.CodecVersion, wire []byte) (string, error) {
	if version == 1 {
		return string(bytes.TrimPrefix(wire, []byte("v1:"))), nil
	}
	if version == 2 {
		return string(wire), nil
	}
	return "", errors.New("unsupported")
}

func TestCustomCodecFreezesItsManifestAndOutput(t *testing.T) {
	versions := []audit.CodecVersion{2, 1}
	wire := []byte("v1:sample")
	current := []byte("sample")
	codec, err := audit.TryDefineCodec(audit.CodecSpec{
		Name:         "example.codec",
		WriteVersion: 2,
		ReadVersions: versions,
		Fixtures: []audit.CodecWireFixture{
			audit.GoldenWire("legacy", 1, wire, current),
			audit.GoldenWire("current", 2, current, current),
			audit.RejectedWire("invalid", 1, []byte("bad")),
		},
	}, &strictVersionedTextEngine{})
	if err != nil {
		t.Fatal(err)
	}
	versions[0] = 99
	wire[0] = 'x'
	current[0] = 'x'
	description := codec.Description()
	if !slices.Equal(description.ReadVersions, []audit.CodecVersion{1, 2}) {
		t.Fatalf("read versions = %v", description.ReadVersions)
	}
	description.ReadVersions[0] = 99
	if codec.Description().ReadVersions[0] != 1 {
		t.Fatal("description aliases a prior result")
	}
}

type strictVersionedTextEngine struct{}

func (*strictVersionedTextEngine) Encode(value string) ([]byte, error) { return []byte(value), nil }

func (*strictVersionedTextEngine) Decode(version audit.CodecVersion, wire []byte) (string, error) {
	if version == 1 {
		if !bytes.HasPrefix(wire, []byte("v1:")) {
			return "", errors.New("invalid legacy wire")
		}
		return string(wire[3:]), nil
	}
	if version == 2 {
		return string(wire), nil
	}
	return "", errors.New("unsupported")
}
