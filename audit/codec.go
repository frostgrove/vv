package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type CodecVersion uint32

type CodecWireFixture struct {
	name     FixtureName
	version  CodecVersion
	wire     []byte
	current  []byte
	accepted bool
}

type CodecSpec struct {
	Name         string
	WriteVersion CodecVersion
	ReadVersions []CodecVersion
	Fixtures     []CodecWireFixture
}

type CodecDescription struct {
	Name         string
	WriteVersion CodecVersion
	ReadVersions []CodecVersion
	Fingerprint  CodecSemanticFingerprint
}

type CodecEngine[V any] interface {
	Encode(V) ([]byte, error)
	Decode(CodecVersion, []byte) (V, error)
}

type codec[V any] struct {
	description CodecDescription
	engine      CodecEngine[V]
}

type Codec[V any] struct {
	value *codec[V]
}

func GoldenWire(name FixtureName, version CodecVersion, wire, current []byte) CodecWireFixture {
	fixture, err := TryGoldenWire(name, version, wire, current)
	if err != nil {
		panic(err)
	}
	return fixture
}

func TryGoldenWire(name FixtureName, version CodecVersion, wire, current []byte) (CodecWireFixture, error) {
	if err := validateCodecFixture(name, version, wire); err != nil {
		return CodecWireFixture{}, err
	}
	if len(current) > MaxValueBytes {
		return CodecWireFixture{}, fmt.Errorf("%w: codec fixture current value exceeds %d bytes", ErrTooLarge, MaxValueBytes)
	}
	return CodecWireFixture{name: name, version: version, wire: bytes.Clone(wire), current: bytes.Clone(current), accepted: true}, nil
}

func RejectedWire(name FixtureName, version CodecVersion, wire []byte) CodecWireFixture {
	fixture, err := TryRejectedWire(name, version, wire)
	if err != nil {
		panic(err)
	}
	return fixture
}

func TryRejectedWire(name FixtureName, version CodecVersion, wire []byte) (CodecWireFixture, error) {
	if err := validateCodecFixture(name, version, wire); err != nil {
		return CodecWireFixture{}, err
	}
	return CodecWireFixture{name: name, version: version, wire: bytes.Clone(wire)}, nil
}

func DefineCodec[V any](spec CodecSpec, engine CodecEngine[V]) Codec[V] {
	codec, err := TryDefineCodec(spec, engine)
	if err != nil {
		panic(err)
	}
	return codec
}

func TryDefineCodec[V any](spec CodecSpec, engine CodecEngine[V]) (Codec[V], error) {
	if nilByReflection(engine) {
		return Codec[V]{}, fmt.Errorf("%w: codec engine is nil", ErrDeclaration)
	}
	if err := validateCodecName(spec.Name); err != nil {
		return Codec[V]{}, err
	}
	if spec.WriteVersion == 0 {
		return Codec[V]{}, fmt.Errorf("%w: codec write version is zero", ErrDeclaration)
	}
	versions, err := canonicalCodecVersions(spec.ReadVersions, spec.WriteVersion)
	if err != nil {
		return Codec[V]{}, err
	}
	fixtures, err := validateCodecFixtures(engine, versions, spec.WriteVersion, spec.Fixtures)
	if err != nil {
		return Codec[V]{}, err
	}
	description := CodecDescription{
		Name:         spec.Name,
		WriteVersion: spec.WriteVersion,
		ReadVersions: versions,
		Fingerprint:  codecFingerprint(spec.Name, spec.WriteVersion, versions, fixtures),
	}
	return Codec[V]{value: &codec[V]{description: description, engine: engine}}, nil
}

func (c Codec[V]) Description() CodecDescription {
	if c.value == nil {
		return CodecDescription{}
	}
	description := c.value.description
	description.ReadVersions = slices.Clone(description.ReadVersions)
	return description
}

func (c Codec[V]) Encode(value V) ([]byte, error) {
	if c.value == nil {
		return nil, fmt.Errorf("%w: codec is not defined", ErrInvalid)
	}
	wire, err := c.value.engine.Encode(value)
	if err != nil {
		return nil, auditError(ErrInvalid, err)
	}
	if len(wire) > MaxValueBytes {
		return nil, auditTooLarge("value", MaxValueBytes)
	}
	return bytes.Clone(wire), nil
}

func (c Codec[V]) Decode(version CodecVersion, wire []byte) (V, error) {
	var zero V
	if c.value == nil {
		return zero, fmt.Errorf("%w: codec is not defined", ErrInvalid)
	}
	if !slices.Contains(c.value.description.ReadVersions, version) {
		return zero, fmt.Errorf("%w: codec version is not readable", ErrInvalid)
	}
	if len(wire) > MaxValueBytes {
		return zero, fmt.Errorf("%w: encoded value exceeds %d bytes", ErrTooLarge, MaxValueBytes)
	}
	value, err := c.value.engine.Decode(version, bytes.Clone(wire))
	if err != nil {
		return zero, auditError(ErrInvalid, err)
	}
	return value, nil
}

func validateCodecFixture(name FixtureName, version CodecVersion, wire []byte) error {
	if err := validateCodecName(string(name)); err != nil {
		return fmt.Errorf("%w: invalid codec fixture name", ErrDeclaration)
	}
	if version == 0 {
		return fmt.Errorf("%w: codec fixture version is zero", ErrDeclaration)
	}
	if len(wire) > MaxCodecFixtureBytes {
		return fmt.Errorf("%w: codec fixture exceeds %d bytes", ErrTooLarge, MaxCodecFixtureBytes)
	}
	return nil
}

func validateCodecName(name string) error {
	if name == "" || len(name) > MaxNameBytes || !utf8.ValidString(name) {
		return fmt.Errorf("%w: invalid codec name", ErrDeclaration)
	}
	for _, segment := range strings.Split(name, ".") {
		if segment == "" {
			return fmt.Errorf("%w: invalid codec name", ErrDeclaration)
		}
		for index, r := range segment {
			if r >= 'a' && r <= 'z' || index > 0 && r >= '0' && r <= '9' || index > 0 && r == '_' {
				continue
			}
			return fmt.Errorf("%w: invalid codec name", ErrDeclaration)
		}
	}
	return nil
}

func canonicalCodecVersions(read []CodecVersion, write CodecVersion) ([]CodecVersion, error) {
	if len(read) == 0 {
		return nil, fmt.Errorf("%w: codec has no readable versions", ErrDeclaration)
	}
	versions := slices.Clone(read)
	slices.Sort(versions)
	for index, version := range versions {
		if version == 0 || index > 0 && version == versions[index-1] {
			return nil, fmt.Errorf("%w: codec read versions are invalid", ErrDeclaration)
		}
	}
	if !slices.Contains(versions, write) {
		return nil, fmt.Errorf("%w: codec cannot read its write version", ErrDeclaration)
	}
	return versions, nil
}

func validateCodecFixtures[V any](engine CodecEngine[V], versions []CodecVersion, write CodecVersion, input []CodecWireFixture) ([]CodecWireFixture, error) {
	if len(input) == 0 || len(input) > MaxCodecFixtures {
		return nil, fmt.Errorf("%w: codec fixture count is outside the supported bounds", ErrDeclaration)
	}
	fixtures := make([]CodecWireFixture, len(input))
	names := make(map[FixtureName]struct{}, len(input))
	accepted := make(map[CodecVersion]bool, len(versions))
	total := 0
	for index, fixture := range input {
		if err := validateCodecFixture(fixture.name, fixture.version, fixture.wire); err != nil {
			return nil, err
		}
		if _, duplicate := names[fixture.name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate codec fixture name", ErrDeclaration)
		}
		names[fixture.name] = struct{}{}
		if !slices.Contains(versions, fixture.version) {
			return nil, fmt.Errorf("%w: fixture names an unreadable codec version", ErrDeclaration)
		}
		total += len(fixture.wire) + len(fixture.current)
		if total > MaxCodecFixtureSetBytes {
			return nil, fmt.Errorf("%w: codec fixture set exceeds %d bytes", ErrTooLarge, MaxCodecFixtureSetBytes)
		}
		fixture.wire = bytes.Clone(fixture.wire)
		fixture.current = bytes.Clone(fixture.current)
		if err := verifyCodecFixture(engine, write, fixture); err != nil {
			return nil, err
		}
		accepted[fixture.version] = accepted[fixture.version] || fixture.accepted
		fixtures[index] = fixture
	}
	for _, version := range versions {
		if !accepted[version] {
			return nil, fmt.Errorf("%w: codec version %d has no accepted fixture", ErrDeclaration, version)
		}
	}
	slices.SortFunc(fixtures, func(left, right CodecWireFixture) int {
		if left.version != right.version {
			return int(left.version) - int(right.version)
		}
		return strings.Compare(string(left.name), string(right.name))
	})
	return fixtures, nil
}

func verifyCodecFixture[V any](engine CodecEngine[V], write CodecVersion, fixture CodecWireFixture) error {
	for range 2 {
		value, err := probeCodecDecode(engine, fixture.version, bytes.Clone(fixture.wire))
		if !fixture.accepted {
			if err == nil {
				return fmt.Errorf("%w: rejected codec fixture was accepted", ErrDeclaration)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("%w: accepted codec fixture was rejected", ErrDeclaration)
		}
		current, err := probeCodecEncode(engine, value)
		if err != nil || !bytes.Equal(current, fixture.current) {
			return fmt.Errorf("%w: codec fixture does not produce its declared current wire", ErrDeclaration)
		}
		if fixture.version == write && !bytes.Equal(fixture.wire, current) {
			return fmt.Errorf("%w: write-version fixture is not canonical", ErrDeclaration)
		}
	}
	return nil
}

func probeCodecEncode[V any](engine CodecEngine[V], value V) (wire []byte, err error) {
	defer func() {
		if recover() != nil {
			wire = nil
			err = ErrInvalid
		}
	}()
	return engine.Encode(value)
}

func probeCodecDecode[V any](engine CodecEngine[V], version CodecVersion, wire []byte) (value V, err error) {
	defer func() {
		if recover() != nil {
			var zero V
			value = zero
			err = ErrInvalid
		}
	}()
	return engine.Decode(version, wire)
}

func codecFingerprint(name string, write CodecVersion, versions []CodecVersion, fixtures []CodecWireFixture) CodecSemanticFingerprint {
	hash := sha256.New()
	writeFrame(hash, []byte("frostgrove.audit.codec/v1"))
	writeFrame(hash, []byte(name))
	writeUint32(hash, uint32(write))
	writeUint32(hash, uint32(len(versions)))
	for _, version := range versions {
		writeUint32(hash, uint32(version))
	}
	writeUint32(hash, uint32(len(fixtures)))
	for _, fixture := range fixtures {
		writeFrame(hash, []byte(fixture.name))
		writeUint32(hash, uint32(fixture.version))
		if fixture.accepted {
			writeUint32(hash, 1)
		} else {
			writeUint32(hash, 0)
		}
		writeFrame(hash, fixture.wire)
		writeFrame(hash, fixture.current)
	}
	var fingerprint CodecSemanticFingerprint
	copy(fingerprint[:], hash.Sum(nil))
	return fingerprint
}

type textCodecEngine struct{}

func (textCodecEngine) Encode(value string) ([]byte, error) {
	if !utf8.ValidString(value) {
		return nil, ErrInvalid
	}
	return []byte(value), nil
}

func (textCodecEngine) Decode(_ CodecVersion, wire []byte) (string, error) {
	if !utf8.Valid(wire) {
		return "", ErrInvalid
	}
	return string(wire), nil
}

type boolCodecEngine struct{}

func (boolCodecEngine) Encode(value bool) ([]byte, error) {
	if value {
		return []byte{1}, nil
	}
	return []byte{0}, nil
}

func (boolCodecEngine) Decode(_ CodecVersion, wire []byte) (bool, error) {
	if len(wire) != 1 || wire[0] > 1 {
		return false, ErrInvalid
	}
	return wire[0] == 1, nil
}

type int64CodecEngine struct{}

func (int64CodecEngine) Encode(value int64) ([]byte, error) {
	wire := make([]byte, 8)
	binary.BigEndian.PutUint64(wire, uint64(value))
	return wire, nil
}

func (int64CodecEngine) Decode(_ CodecVersion, wire []byte) (int64, error) {
	if len(wire) != 8 {
		return 0, ErrInvalid
	}
	return int64(binary.BigEndian.Uint64(wire)), nil
}

type uint64CodecEngine struct{}

func (uint64CodecEngine) Encode(value uint64) ([]byte, error) {
	wire := make([]byte, 8)
	binary.BigEndian.PutUint64(wire, value)
	return wire, nil
}

func (uint64CodecEngine) Decode(_ CodecVersion, wire []byte) (uint64, error) {
	if len(wire) != 8 {
		return 0, ErrInvalid
	}
	return binary.BigEndian.Uint64(wire), nil
}

type decimalCodecEngine struct{}

func (decimalCodecEngine) Encode(value string) ([]byte, error) {
	if !canonicalDecimal(value) {
		return nil, ErrInvalid
	}
	return []byte(value), nil
}

func (decimalCodecEngine) Decode(_ CodecVersion, wire []byte) (string, error) {
	value := string(wire)
	if !utf8.Valid(wire) || !canonicalDecimal(value) {
		return "", ErrInvalid
	}
	return value, nil
}

func canonicalDecimal(value string) bool {
	if value == "0" {
		return true
	}
	if strings.HasPrefix(value, "-") {
		value = value[1:]
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || parts[0][0] == '0' {
		return false
	}
	for _, r := range parts[0] {
		if r < '0' || r > '9' {
			return false
		}
	}
	if len(parts) == 2 {
		if parts[1] == "" || strings.HasSuffix(parts[1], "0") {
			return false
		}
		for _, r := range parts[1] {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

type bytesCodecEngine struct{}

func (bytesCodecEngine) Encode(value []byte) ([]byte, error) { return bytes.Clone(value), nil }
func (bytesCodecEngine) Decode(_ CodecVersion, wire []byte) ([]byte, error) {
	return bytes.Clone(wire), nil
}

type referenceCodecEngine struct {
	uuid bool
}

func (engine referenceCodecEngine) Encode(value Reference) ([]byte, error) {
	if !validReferenceText(string(value)) || engine.uuid && !canonicalUUID(string(value)) {
		return nil, ErrInvalid
	}
	return []byte(value), nil
}

func (engine referenceCodecEngine) Decode(_ CodecVersion, wire []byte) (Reference, error) {
	value := string(wire)
	if !utf8.Valid(wire) || !validReferenceText(value) || engine.uuid && !canonicalUUID(value) {
		return "", ErrInvalid
	}
	return Reference(value), nil
}

func validReferenceText(value string) bool {
	if value == "" || len(value) > MaxReferenceBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func canonicalUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, r := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if r < '0' || r > '9' && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

type durationCodecEngine struct{}

func (durationCodecEngine) Encode(value time.Duration) ([]byte, error) {
	return int64CodecEngine{}.Encode(int64(value))
}

func (durationCodecEngine) Decode(version CodecVersion, wire []byte) (time.Duration, error) {
	value, err := int64CodecEngine{}.Decode(version, wire)
	return time.Duration(value), err
}

type timeCodecEngine struct{}

func (timeCodecEngine) Encode(value time.Time) ([]byte, error) {
	value = value.UTC()
	wire := make([]byte, 12)
	binary.BigEndian.PutUint64(wire[:8], uint64(value.Unix()))
	binary.BigEndian.PutUint32(wire[8:], uint32(value.Nanosecond()))
	return wire, nil
}

func (timeCodecEngine) Decode(_ CodecVersion, wire []byte) (time.Time, error) {
	if len(wire) != 12 {
		return time.Time{}, ErrInvalid
	}
	seconds := int64(binary.BigEndian.Uint64(wire[:8]))
	nanoseconds := binary.BigEndian.Uint32(wire[8:])
	if nanoseconds >= uint32(time.Second) {
		return time.Time{}, ErrInvalid
	}
	value := time.Unix(seconds, int64(nanoseconds)).UTC()
	encoded, _ := timeCodecEngine{}.Encode(value)
	if !bytes.Equal(encoded, wire) {
		return time.Time{}, ErrInvalid
	}
	return value, nil
}

func Text() Codec[string] {
	return builtinCodec("frostgrove.audit.codec.text", textCodecEngine{}, "empty", nil, nil)
}

func Bool() Codec[bool] {
	return builtinCodec("frostgrove.audit.codec.bool", boolCodecEngine{}, "false", []byte{0}, []byte{0})
}

func Int64() Codec[int64] {
	zero := make([]byte, 8)
	return builtinCodec("frostgrove.audit.codec.int64", int64CodecEngine{}, "zero", zero, zero)
}

func Uint64() Codec[uint64] {
	zero := make([]byte, 8)
	return builtinCodec("frostgrove.audit.codec.uint64", uint64CodecEngine{}, "zero", zero, zero)
}

func DecimalText() Codec[string] {
	return builtinCodec("frostgrove.audit.codec.decimal_text", decimalCodecEngine{}, "zero", []byte("0"), []byte("0"))
}

func Bytes() Codec[[]byte] {
	return builtinCodec("frostgrove.audit.codec.bytes", bytesCodecEngine{}, "empty", nil, nil)
}

func ReferenceText() Codec[Reference] {
	return builtinCodec("frostgrove.audit.codec.reference", referenceCodecEngine{}, "sample", []byte("sample"), []byte("sample"))
}

func UUIDReference() Codec[Reference] {
	value := []byte("00000000-0000-0000-0000-000000000000")
	return builtinCodec("frostgrove.audit.codec.uuid_reference", referenceCodecEngine{uuid: true}, "zero", value, value)
}

func Duration() Codec[time.Duration] {
	zero := make([]byte, 8)
	return builtinCodec("frostgrove.audit.codec.duration", durationCodecEngine{}, "zero", zero, zero)
}

func Time() Codec[time.Time] {
	wire := make([]byte, 12)
	return builtinCodec("frostgrove.audit.codec.time", timeCodecEngine{}, "epoch", wire, wire)
}

func builtinCodec[V any](name string, engine CodecEngine[V], fixture string, wire, current []byte) Codec[V] {
	return DefineCodec(CodecSpec{
		Name:         name,
		WriteVersion: 1,
		ReadVersions: []CodecVersion{1},
		Fixtures:     []CodecWireFixture{GoldenWire(FixtureName(fixture), 1, wire, current)},
	}, engine)
}

func writeFrame(target interface{ Write([]byte) (int, error) }, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = target.Write(size[:])
	_, _ = target.Write(value)
}

func writeUint32(target interface{ Write([]byte) (int, error) }, value uint32) {
	var wire [4]byte
	binary.BigEndian.PutUint32(wire[:], value)
	_, _ = target.Write(wire[:])
}
