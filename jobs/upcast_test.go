package jobs

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type revisionOne struct {
	Text string `json:"text"`
}

type revisionTwo struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

type revisionThree struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

func TestTypedUpcasterChainIsSortedContiguousAndBoundedPerHop(t *testing.T) {
	v1 := TrustedJSON[revisionOne](1)
	v2 := TrustedJSON[revisionTwo](2)
	v3 := TrustedJSON[revisionThree](3)
	definition, err := Define(DefinitionSpec[revisionThree]{
		Name:  testJobName(t, "documents.revisioned"),
		Codec: v3,
		Upcasters: []Upcaster{
			Upcast(v2, v3, func(value revisionTwo) (revisionThree, error) {
				return revisionThree{Message: value.Text, Count: value.Count}, nil
			}),
			Upcast(v1, v2, func(value revisionOne) (revisionTwo, error) {
				return revisionTwo{Text: value.Text, Count: len(value.Text)}, nil
			}),
		},
		Policy: testPolicy(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := v1.Encode(revisionOne{Text: "hello"}, DefaultPayloadLimit())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := NewEncodedPayload(v1.ID(), 1, encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := definition.Decode(payload)
	if err != nil || decoded != (revisionThree{Message: "hello", Count: 5}) {
		t.Fatalf("upcast failed: %#v, %v", decoded, err)
	}
	description := definition.Describe().Codec
	if len(description.SupportedRevisions) != 3 || description.SupportedRevisions[0] != 1 || description.SupportedRevisions[2] != 3 || len(description.Upcasts) != 2 {
		t.Fatalf("unexpected compatibility window: %#v", description)
	}
}

func TestUpcasterPreservesRuntimeFailureProvenance(t *testing.T) {
	const secret = "private-transform-error-material"
	tests := []struct {
		fn   func(revisionOne) (revisionTwo, error)
		want error
	}{
		{fn: func(revisionOne) (revisionTwo, error) { panic(secret) }, want: ErrInvalid},
		{fn: func(revisionOne) (revisionTwo, error) { return revisionTwo{}, errors.New(secret) }, want: ErrInvalid},
		{fn: func(revisionOne) (revisionTwo, error) { return revisionTwo{}, fmt.Errorf("%w: %s", ErrCorrupt, secret) }, want: ErrCorrupt},
	}
	for _, test := range tests {
		fn := test.fn
		v1 := TrustedJSON[revisionOne](1)
		v2 := TrustedJSON[revisionTwo](2)
		definition, err := Define(DefinitionSpec[revisionTwo]{Name: testJobName(t, "documents.failure"), Codec: v2, Upcasters: []Upcaster{Upcast(v1, v2, fn)}, Policy: testPolicy(t)})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := v1.Encode(revisionOne{Text: "x"}, DefaultPayloadLimit())
		if err != nil {
			t.Fatal(err)
		}
		payload, err := NewEncodedPayload(v1.ID(), 1, encoded)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := definition.Decode(payload); !errors.Is(err, test.want) || strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("expected %v, got %v", test.want, err)
		}
	}
}

func TestUpcasterIntermediatePayloadCannotExceedDefinitionLimit(t *testing.T) {
	v1 := TrustedJSON[revisionOne](1)
	v2 := TrustedJSON[revisionTwo](2)
	policy := testPolicy(t, MaxBytes(128), DecodedBytes(512))
	definition, err := Define(DefinitionSpec[revisionTwo]{
		Name:  testJobName(t, "documents.expanding"),
		Codec: v2,
		Upcasters: []Upcaster{Upcast(v1, v2, func(revisionOne) (revisionTwo, error) {
			return revisionTwo{Text: strings.Repeat("x", 256)}, nil
		})},
		Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := v1.Encode(revisionOne{Text: "x"}, policy.Payload)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := NewEncodedPayload(v1.ID(), 1, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Decode(payload); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expanded intermediate payload escaped limit: %v", err)
	}
}

func TestTypedUpcasterNormalizesSourceAndTargetCodecErrors(t *testing.T) {
	const secret = "private-upcast-codec-error"
	id := builtinCodecID("upcast-secret")
	workingV1 := secretStringCodec{id: id, version: 1, secret: secret}
	workingV2 := secretStringCodec{id: id, version: 2, secret: secret}
	payload := mustEncodedPayload(t, id.String(), 1, "value")
	sourceCases := []struct {
		err  error
		want error
	}{
		{fmt.Errorf("%w: %s", ErrTooLarge, secret), ErrTooLarge},
		{fmt.Errorf("%w: %s", ErrUnsupported, secret), ErrUnsupported},
		{fmt.Errorf("%w: %s", ErrCorrupt, secret), ErrCorrupt},
		{fmt.Errorf("%w: %s", ErrInvalid, secret), ErrInvalid},
		{errors.New(secret), ErrInvalid},
	}
	for index, test := range sourceCases {
		source := secretStringCodec{id: id, version: 1, secret: secret, decodeErr: test.err}
		definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, fmt.Sprintf("upcast.source-%d", index)), Codec: workingV2, Upcasters: []Upcaster{Upcast(source, workingV2, func(value string) (string, error) { return value, nil })}, Policy: testPolicy(t)})
		if _, err := definition.Decode(payload); !errors.Is(err, test.want) || strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("source case %d = %v", index, err)
		}
	}
	targetCases := []struct {
		err  error
		want error
	}{
		{fmt.Errorf("%w: %s", ErrTooLarge, secret), ErrTooLarge},
		{fmt.Errorf("%w: %s", ErrUnsupported, secret), ErrInvalid},
		{fmt.Errorf("%w: %s", ErrCorrupt, secret), ErrInvalid},
		{fmt.Errorf("%w: %s", ErrInvalid, secret), ErrInvalid},
		{errors.New(secret), ErrInvalid},
	}
	for index, test := range targetCases {
		target := secretStringCodec{id: id, version: 2, secret: secret, encodeErr: test.err}
		definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, fmt.Sprintf("upcast.target-%d", index)), Codec: target, Upcasters: []Upcaster{Upcast(workingV1, target, func(value string) (string, error) { return value, nil })}, Policy: testPolicy(t)})
		if _, err := definition.Decode(payload); !errors.Is(err, test.want) || strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("target case %d = %v", index, err)
		}
	}
}

type stubUpcaster struct {
	from SchemaVersion
	to   SchemaVersion
	id   CodecID
}

type failingUpcaster struct {
	from          SchemaVersion
	to            SchemaVersion
	id            CodecID
	runtimeErr    error
	validationErr error
	panicAt       string
	secret        string
}

type ownedTailUpcaster struct{ backing []byte }

type aliasingBytesCodec struct{ version SchemaVersion }

type oversizedBytesCodec struct{ version SchemaVersion }

func (this stubUpcaster) From() SchemaVersion                 { return this.from }
func (this stubUpcaster) To() SchemaVersion                   { return this.to }
func (this stubUpcaster) SourceCodec() CodecID                { return this.id }
func (this stubUpcaster) TargetCodec() CodecID                { return this.id }
func (stubUpcaster) validateUpcasterLimit(PayloadLimit) error { return nil }
func (stubUpcaster) upcasterMarker()                          {}
func (stubUpcaster) upcast(value []byte, _ PayloadLimit) ([]byte, error) {
	return bytes.Clone(value), nil
}

func (this failingUpcaster) From() SchemaVersion  { return this.from }
func (this failingUpcaster) To() SchemaVersion    { return this.to }
func (this failingUpcaster) SourceCodec() CodecID { return this.id }
func (this failingUpcaster) TargetCodec() CodecID { return this.id }
func (this failingUpcaster) validateUpcasterLimit(PayloadLimit) error {
	if this.panicAt == "validate" {
		panic(this.secret)
	}
	return this.validationErr
}
func (failingUpcaster) upcasterMarker() {}
func (this failingUpcaster) upcast(value []byte, _ PayloadLimit) ([]byte, error) {
	if this.panicAt == "upcast" {
		panic(this.secret)
	}
	if this.runtimeErr != nil {
		return []byte(this.secret), this.runtimeErr
	}
	return bytes.Clone(value), nil
}

func (ownedTailUpcaster) From() SchemaVersion                      { return 1 }
func (ownedTailUpcaster) To() SchemaVersion                        { return 2 }
func (ownedTailUpcaster) SourceCodec() CodecID                     { return builtinCodecID("bytes") }
func (ownedTailUpcaster) TargetCodec() CodecID                     { return builtinCodecID("bytes") }
func (ownedTailUpcaster) validateUpcasterLimit(PayloadLimit) error { return nil }
func (ownedTailUpcaster) upcasterMarker()                          {}
func (u ownedTailUpcaster) upcast([]byte, PayloadLimit) ([]byte, error) {
	return bytes.Clone(u.backing[:7]), nil
}
func (u ownedTailUpcaster) upcastOwned([]byte, PayloadLimit) ([]byte, error) {
	return u.backing[:7], nil
}

func TestOwnedUpcastCannotExposeEncodedTail(t *testing.T) {
	upcaster := ownedTailUpcaster{backing: []byte("visible-private-tail")}
	definition := MustDefine(DefinitionSpec[[]byte]{Name: testJobName(t, "tests.owned-upcast-tail"), Codec: Bytes(2), Upcasters: []Upcaster{upcaster}, Policy: testPolicy(t)})
	payload, err := takeEncodedPayload(builtinCodecID("bytes"), 1, []byte("historic"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := definition.decodeOwned(payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "visible" || cap(decoded) != len(decoded) {
		t.Fatal("owned upcast exposed encoded backing tail")
	}
}

func (aliasingBytesCodec) ID() CodecID                                         { return builtinCodecID("bytes") }
func (c aliasingBytesCodec) Version() SchemaVersion                            { return c.version }
func (aliasingBytesCodec) Encode(value []byte, _ PayloadLimit) ([]byte, error) { return value, nil }
func (aliasingBytesCodec) Decode(value []byte, _ PayloadLimit) ([]byte, error) { return value, nil }

func TestOwnedTypedUpcastTakesCustomCodecOutput(t *testing.T) {
	retained := []byte("retained")
	target := aliasingBytesCodec{version: 2}
	upcaster := Upcast(Bytes(1), target, func([]byte) ([]byte, error) { return retained, nil })
	definition := MustDefine(DefinitionSpec[[]byte]{Name: testJobName(t, "tests.owned-upcast-alias"), Codec: Bytes(2), Upcasters: []Upcaster{upcaster}, Policy: testPolicy(t)})
	payload, err := takeEncodedPayload(builtinCodecID("bytes"), 1, []byte("historic"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := definition.decodeOwned(payload)
	if err != nil {
		t.Fatal(err)
	}
	decoded[0] = 'R'
	if string(retained) != "retained" {
		t.Fatal("owned upcast retained custom codec output alias")
	}
}

func (oversizedBytesCodec) ID() CodecID              { return builtinCodecID("bytes") }
func (c oversizedBytesCodec) Version() SchemaVersion { return c.version }
func (oversizedBytesCodec) Encode([]byte, PayloadLimit) ([]byte, error) {
	return make([]byte, MaxPayloadBytes+1), nil
}
func (oversizedBytesCodec) Decode(value []byte, _ PayloadLimit) ([]byte, error) { return value, nil }

func TestOwnedTypedUpcastRejectsCustomOutputBeforeOwnershipCopy(t *testing.T) {
	target := oversizedBytesCodec{version: 2}
	upcaster := Upcast(Bytes(1), target, func(value []byte) ([]byte, error) { return value, nil })
	definition := MustDefine(DefinitionSpec[[]byte]{Name: testJobName(t, "tests.owned-upcast-oversized"), Codec: Bytes(2), Upcasters: []Upcaster{upcaster}, Policy: testPolicy(t)})
	payload, err := takeEncodedPayload(builtinCodecID("bytes"), 1, []byte("historic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.decodeOwned(payload); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized custom output = %v", err)
	}
}

func TestDefinitionNormalizesCustomUpcasterValidationAndRuntimeErrors(t *testing.T) {
	const secret = "private-custom-upcaster-error"
	id := builtinCodecID("string")
	validationCases := []struct {
		err  error
		want error
	}{
		{fmt.Errorf("%w: %s", ErrTooLarge, secret), ErrTooLarge},
		{fmt.Errorf("%w: %s", ErrInvalid, secret), ErrInvalid},
		{fmt.Errorf("%w: %s", ErrCorrupt, secret), ErrInvalid},
		{errors.New(secret), ErrInvalid},
	}
	for index, test := range validationCases {
		upcaster := failingUpcaster{from: 1, to: 2, id: id, validationErr: test.err, secret: secret}
		_, err := Define(DefinitionSpec[string]{Name: testJobName(t, fmt.Sprintf("upcast.validation-%d", index)), Codec: String(2), Upcasters: []Upcaster{upcaster}, Policy: testPolicy(t)})
		if !errors.Is(err, test.want) || strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("validation case %d = %v", index, err)
		}
	}
	for _, phase := range []string{"validate", "upcast"} {
		upcaster := failingUpcaster{from: 1, to: 2, id: id, panicAt: phase, secret: secret}
		definition, err := Define(DefinitionSpec[string]{Name: testJobName(t, "upcast.panic-"+phase), Codec: String(2), Upcasters: []Upcaster{upcaster}, Policy: testPolicy(t)})
		if phase == "validate" {
			if !errors.Is(err, ErrInvalid) || strings.Contains(fmt.Sprint(err), secret) {
				t.Fatalf("validation panic = %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		_, err = definition.Decode(mustEncodedPayload(t, "string", 1, "value"))
		if !errors.Is(err, ErrInvalid) || strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("runtime panic = %v", err)
		}
	}
	runtimeCases := []struct {
		err  error
		want error
	}{
		{fmt.Errorf("%w: %s", ErrTooLarge, secret), ErrTooLarge},
		{fmt.Errorf("%w: %s", ErrUnsupported, secret), ErrUnsupported},
		{fmt.Errorf("%w: %s", ErrCorrupt, secret), ErrCorrupt},
		{fmt.Errorf("%w: %s", ErrInvalid, secret), ErrInvalid},
		{errors.New(secret), ErrInvalid},
	}
	for index, test := range runtimeCases {
		upcaster := failingUpcaster{from: 1, to: 2, id: id, runtimeErr: test.err, secret: secret}
		definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, fmt.Sprintf("upcast.runtime-%d", index)), Codec: String(2), Upcasters: []Upcaster{upcaster}, Policy: testPolicy(t)})
		_, err := definition.Decode(mustEncodedPayload(t, "string", 1, "value"))
		if !errors.Is(err, test.want) || strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("runtime case %d = %v", index, err)
		}
	}
}

func TestUpcasterGraphRejectsGapsDuplicatesAndOversizedWindows(t *testing.T) {
	id := builtinCodecID("string")
	current := String(4)
	tests := []struct {
		name      string
		upcasters []Upcaster
		want      error
	}{
		{"gap", []Upcaster{stubUpcaster{1, 2, id}, stubUpcaster{3, 4, id}}, ErrInvalid},
		{"duplicate", []Upcaster{stubUpcaster{1, 2, id}, stubUpcaster{1, 2, id}, stubUpcaster{2, 3, id}, stubUpcaster{3, 4, id}}, ErrConflict},
		{"wrong-terminal", []Upcaster{stubUpcaster{1, 2, id}}, ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Define(DefinitionSpec[string]{Name: testJobName(t, "documents."+test.name), Codec: current, Upcasters: test.upcasters, Policy: testPolicy(t)})
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
	tooMany := make([]Upcaster, MaxUpcastHops+1)
	for index := range tooMany {
		tooMany[index] = stubUpcaster{from: SchemaVersion(index + 1), to: SchemaVersion(index + 2), id: id}
	}
	if _, err := Define(DefinitionSpec[string]{Name: testJobName(t, "documents.too-many"), Codec: String(SchemaVersion(len(tooMany) + 1)), Upcasters: tooMany, Policy: testPolicy(t)}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

func TestMaximumUpcastWindowAndUnsupportedRevisionHandling(t *testing.T) {
	id := builtinCodecID("string")
	upcasters := make([]Upcaster, MaxUpcastHops)
	for index := range upcasters {
		upcasters[index] = stubUpcaster{from: SchemaVersion(index + 1), to: SchemaVersion(index + 2), id: id}
	}
	definition, err := Define(DefinitionSpec[string]{Name: testJobName(t, "documents.maximum-window"), Codec: String(MaxSupportedRevisions), Upcasters: upcasters, Policy: testPolicy(t)})
	if err != nil {
		t.Fatal(err)
	}
	value, err := definition.Decode(mustEncodedPayload(t, "string", 1, "value"))
	if err != nil || value != "value" {
		t.Fatalf("maximum chain failed: %q, %v", value, err)
	}
	for _, version := range []SchemaVersion{0, MaxSupportedRevisions + 1} {
		payload := EncodedPayload{codec: id, version: version, data: []byte("value")}
		if _, err := definition.Decode(payload); !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrUnsupported) {
			t.Fatalf("version %d: expected invalid/unsupported, got %v", version, err)
		}
	}
	if _, err := definition.Decode(mustEncodedPayload(t, "bytes", 1, "value")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("historic codec mismatch was accepted: %v", err)
	}
}

func TestTypedUpcasterRequiresAdjacentCodecVersionsAndFunction(t *testing.T) {
	v1 := TrustedJSON[revisionOne](1)
	v3 := TrustedJSON[revisionThree](3)
	invalid := []Upcaster{
		Upcast(v1, v3, func(value revisionOne) (revisionThree, error) { return revisionThree{Message: value.Text}, nil }),
		Upcast[revisionOne, revisionTwo](v1, TrustedJSON[revisionTwo](2), nil),
	}
	for index, upcaster := range invalid {
		if _, err := describeUpcaster(upcaster); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: expected ErrInvalid, got %v", index, err)
		}
	}
	if _, err := describeUpcaster(stubUpcaster{from: ^SchemaVersion(0), to: 0, id: builtinCodecID("x")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overflowing revision accepted: %v", err)
	}
}
