package jobs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func testJobName(t *testing.T, value string) Name {
	t.Helper()
	name, err := ParseName(value)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

type changingStringCodec struct{ encodes int }

func (*changingStringCodec) ID() CodecID            { return builtinCodecID("changing-wire") }
func (*changingStringCodec) Version() SchemaVersion { return 1 }
func (c *changingStringCodec) Encode(value string, _ PayloadLimit) ([]byte, error) {
	c.encodes++
	return []byte(fmt.Sprintf("%s:%d", value, c.encodes)), nil
}
func (*changingStringCodec) Decode(value []byte, _ PayloadLimit) (string, error) {
	return string(value), nil
}

type stringPayloadIdentity struct {
	id      CodecID
	version SchemaVersion
	panic   bool
}

func (i stringPayloadIdentity) ID() CodecID            { return i.id }
func (i stringPayloadIdentity) Version() SchemaVersion { return i.version }
func (i stringPayloadIdentity) Digest(value string, _ PayloadLimit) ([32]byte, error) {
	if i.panic {
		panic("private identity panic")
	}
	return sha256.Sum256([]byte(value)), nil
}

func TestDefinitionProvidesStableSemanticIdentityForSafeAndExplicitCodecs(t *testing.T) {
	safe := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "identity.safe"), Codec: String(1), Policy: testPolicy(t)})
	first, err := safe.Digest("same")
	if err != nil {
		t.Fatal(err)
	}
	second, err := safe.Digest("same")
	if err != nil || first != second || first.IsZero() || !safe.PayloadIdentity().Automatic {
		t.Fatalf("safe identity = %+v/%+v/%v", first, second, err)
	}
	safeNext := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "identity.safe-next"), Codec: String(2), Policy: testPolicy(t)})
	acrossCodecRevision, err := safeNext.Digest("same")
	if err != nil || acrossCodecRevision != first {
		t.Fatalf("safe identity changed with wire schema revision: %+v/%+v/%v", first, acrossCodecRevision, err)
	}
	codec := &changingStringCodec{}
	without := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "identity.unsupported"), Codec: codec, Policy: testPolicy(t)})
	if _, err := without.Digest("same"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("missing identity error = %v", err)
	}
	identity := stringPayloadIdentity{id: builtinCodecID("string-semantics"), version: 1}
	explicit := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "identity.explicit"), Codec: codec, Identity: identity, Policy: testPolicy(t)})
	wireOne, digestOne, err := explicit.preparePayload("same", true)
	if err != nil {
		t.Fatal(err)
	}
	wireTwo, digestTwo, err := explicit.preparePayload("same", true)
	if err != nil || string(wireOne.Bytes()) == string(wireTwo.Bytes()) || digestOne != digestTwo || explicit.PayloadIdentity().Automatic {
		t.Fatalf("explicit identity = %q/%q %+v/%+v %v", wireOne.Bytes(), wireTwo.Bytes(), digestOne, digestTwo, err)
	}
	panicking := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "identity.panic"), Codec: String(1), Identity: stringPayloadIdentity{id: builtinCodecID("panic-semantics"), version: 1, panic: true}, Policy: testPolicy(t)})
	if _, err := panicking.Digest("secret"); !errors.Is(err, ErrInvalid) || strings.Contains(fmt.Sprint(err), "private") {
		t.Fatalf("identity panic error = %v", err)
	}
}

func testPolicy(t *testing.T, options ...Option) Policy {
	t.Helper()
	policy, err := Default.With(options...).Build()
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestManualDefinitionEncodesVersionedEnvelopeAndDescribesPolicy(t *testing.T) {
	definition, err := Define(DefinitionSpec[string]{
		Name:   testJobName(t, "documents.translate"),
		Codec:  String(3),
		Policy: testPolicy(t, Retries(0), MaxHandlerDeferrals(0), MaxDeliveryDeferrals(0)),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := definition.Encode("hello")
	if err != nil {
		t.Fatal(err)
	}
	if payload.Codec().String() != "string" || payload.Version() != 3 || string(payload.Bytes()) != "hello" {
		t.Fatalf("unexpected envelope: %#v", payload)
	}
	decoded, err := definition.Decode(payload)
	if err != nil || decoded != "hello" {
		t.Fatalf("decode failed: %q, %v", decoded, err)
	}
	description := definition.Describe()
	if description.Name != definition.Name() || description.Codec.ID.String() != "string" || description.Codec.CurrentVersion != 3 || description.Codec.Mode != SafeCodecMode || len(description.Codec.SupportedRevisions) != 1 || description.Codec.SupportedRevisions[0] != 3 || description.Policy.MaxElapsed != DefaultMaxElapsed || description.Policy.MaxRetries != 0 || description.Policy.MaxHandlerDeferrals != 0 || description.Policy.MaxDeliveryDeferrals != 0 || description.Policy.Profile != "Default" || !description.Resolved {
		t.Fatalf("unexpected descriptor: %#v", description)
	}
}

func TestDefinitionRequiresStableNameCodecPolicyAndMaxElapsed(t *testing.T) {
	valid := DefinitionSpec[string]{Name: testJobName(t, "documents.translate"), Codec: String(1), Policy: testPolicy(t)}
	tests := []struct {
		mutate func(*DefinitionSpec[string])
		want   error
	}{
		{func(value *DefinitionSpec[string]) { value.Name = Name{} }, ErrInvalid},
		{func(value *DefinitionSpec[string]) { value.Codec = nil }, ErrInvalid},
		{func(value *DefinitionSpec[string]) { value.Codec = String(0) }, ErrInvalid},
		{func(value *DefinitionSpec[string]) { value.Partition = PartitionMode(255) }, ErrInvalid},
		{func(value *DefinitionSpec[string]) { value.Policy.MaxElapsed = 0 }, ErrInvalid},
		{func(value *DefinitionSpec[string]) { value.Policy.Payload.MaxBytes = 0 }, ErrInvalid},
	}
	for index, test := range tests {
		value := valid
		test.mutate(&value)
		if _, err := Define(value); !errors.Is(err, test.want) {
			t.Fatalf("case %d: expected %v, got %v", index, test.want, err)
		}
	}
}

func TestDefinitionRejectsSafeJSONShapeThatCannotFitDecodedBound(t *testing.T) {
	if !safeJSONRuntimeSupported {
		t.Skip("safe JSON intentionally refuses jsonv2")
	}
	type huge struct {
		Ignored [300 << 10]byte `json:"-"`
	}
	_, err := Define(DefinitionSpec[huge]{
		Name:   testJobName(t, "documents.huge"),
		Codec:  JSON[huge](1),
		Policy: testPolicy(t),
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected fail-fast decoded bound, got %v", err)
	}
}

type mutableStringCodec struct {
	id      CodecID
	version SchemaVersion
}

type panicStringCodec struct{ phase string }

type secretStringCodec struct {
	id        CodecID
	version   SchemaVersion
	secret    string
	encodeErr error
	decodeErr error
	panicAt   string
}

func (this panicStringCodec) ID() CodecID {
	if this.phase == "describe" {
		panic("describe")
	}
	return builtinCodecID("panic")
}

func (panicStringCodec) Version() SchemaVersion { return 1 }
func (this panicStringCodec) Encode(string, PayloadLimit) ([]byte, error) {
	if this.phase == "encode" {
		panic("encode")
	}
	return []byte("value"), nil
}
func (this panicStringCodec) Decode([]byte, PayloadLimit) (string, error) {
	if this.phase == "decode" {
		panic("decode")
	}
	return "value", nil
}

func (this *mutableStringCodec) ID() CodecID            { return this.id }
func (this *mutableStringCodec) Version() SchemaVersion { return this.version }
func (*mutableStringCodec) Encode(value string, _ PayloadLimit) ([]byte, error) {
	return []byte(value), nil
}
func (*mutableStringCodec) Decode(value []byte, _ PayloadLimit) (string, error) {
	return string(value), nil
}

func (this secretStringCodec) ID() CodecID            { return this.id }
func (this secretStringCodec) Version() SchemaVersion { return this.version }
func (this secretStringCodec) Encode(value string, _ PayloadLimit) ([]byte, error) {
	if this.panicAt == "encode" {
		panic(this.secret)
	}
	if this.encodeErr != nil {
		return []byte(this.secret), this.encodeErr
	}
	return []byte(value), nil
}
func (this secretStringCodec) Decode(value []byte, _ PayloadLimit) (string, error) {
	if this.panicAt == "decode" {
		panic(this.secret)
	}
	if this.decodeErr != nil {
		return this.secret, this.decodeErr
	}
	return string(value), nil
}

func TestDefinitionDetectsMutableCodecIdentity(t *testing.T) {
	codec := &mutableStringCodec{id: builtinCodecID("mutable"), version: 1}
	definition, err := Define(DefinitionSpec[string]{Name: testJobName(t, "documents.mutable"), Codec: codec, Policy: testPolicy(t)})
	if err != nil {
		t.Fatal(err)
	}
	codec.version = 2
	if _, err := definition.Encode("value"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mutable codec drift was accepted: %v", err)
	}
	codec.version = 1
	codec.id = builtinCodecID("changed")
	if _, err := definition.Decode(mustEncodedPayload(t, "mutable", 1, "value")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mutable codec id drift was accepted: %v", err)
	}
}

func TestDefinitionContainsCodecDescriptorEncodeAndDecodePanics(t *testing.T) {
	if _, err := Define(DefinitionSpec[string]{Name: testJobName(t, "documents.panic-descriptor"), Codec: panicStringCodec{phase: "describe"}, Policy: testPolicy(t)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("descriptor panic escaped: %v", err)
	}
	encodeDefinition, err := Define(DefinitionSpec[string]{Name: testJobName(t, "documents.panic-encode"), Codec: panicStringCodec{phase: "encode"}, Policy: testPolicy(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encodeDefinition.Encode("value"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("encode panic escaped: %v", err)
	}
	decodeDefinition, err := Define(DefinitionSpec[string]{Name: testJobName(t, "documents.panic-decode"), Codec: panicStringCodec{phase: "decode"}, Policy: testPolicy(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeDefinition.Decode(mustEncodedPayload(t, "panic", 1, "value")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("decode panic escaped: %v", err)
	}
}

func TestDefinitionRejectsOversizedProducerPayloadEvenForCustomCodec(t *testing.T) {
	codec := &mutableStringCodec{id: builtinCodecID("custom"), version: 1}
	definition, err := Define(DefinitionSpec[string]{Name: testJobName(t, "documents.custom"), Codec: codec, Policy: testPolicy(t, MaxBytes(8))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Encode(strings.Repeat("x", 9)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("custom codec output escaped policy bound: %v", err)
	}
}

func TestDefinitionNormalizesEveryCustomCodecError(t *testing.T) {
	const secret = "private-codec-error-material"
	id := builtinCodecID("secret-codec")
	encodeCases := []struct {
		err  error
		want error
	}{
		{fmt.Errorf("%w: %s", ErrTooLarge, secret), ErrTooLarge},
		{fmt.Errorf("%w: %s", ErrInvalid, secret), ErrInvalid},
		{fmt.Errorf("%w: %s", ErrCorrupt, secret), ErrInvalid},
		{fmt.Errorf("%w: %s", ErrUnsupported, secret), ErrInvalid},
		{errors.New(secret), ErrInvalid},
	}
	for index, test := range encodeCases {
		codec := secretStringCodec{id: id, version: 1, secret: secret, encodeErr: test.err}
		definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, fmt.Sprintf("codec.encode-%d", index)), Codec: codec, Policy: testPolicy(t)})
		payload, err := definition.Encode("value")
		if !errors.Is(err, test.want) || !payload.IsZero() || strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("encode case %d = (%v, %v)", index, payload, err)
		}
	}
	panicDefinition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "codec.encode-panic"), Codec: secretStringCodec{id: id, version: 1, secret: secret, panicAt: "encode"}, Policy: testPolicy(t)})
	if _, err := panicDefinition.Encode("value"); !errors.Is(err, ErrInvalid) || strings.Contains(fmt.Sprint(err), secret) {
		t.Fatalf("encode panic = %v", err)
	}

	decodeCases := []struct {
		err  error
		want error
	}{
		{fmt.Errorf("%w: %s", ErrTooLarge, secret), ErrTooLarge},
		{fmt.Errorf("%w: %s", ErrUnsupported, secret), ErrUnsupported},
		{fmt.Errorf("%w: %s", ErrCorrupt, secret), ErrCorrupt},
		{fmt.Errorf("%w: %s", ErrInvalid, secret), ErrCorrupt},
		{errors.New(secret), ErrCorrupt},
	}
	payload := mustEncodedPayload(t, id.String(), 1, "value")
	for index, test := range decodeCases {
		codec := secretStringCodec{id: id, version: 1, secret: secret, decodeErr: test.err}
		definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, fmt.Sprintf("codec.decode-%d", index)), Codec: codec, Policy: testPolicy(t)})
		value, err := definition.Decode(payload)
		if !errors.Is(err, test.want) || value != "" || strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("decode case %d = (%q, %v)", index, value, err)
		}
	}
	panicDefinition = MustDefine(DefinitionSpec[string]{Name: testJobName(t, "codec.decode-panic"), Codec: secretStringCodec{id: id, version: 1, secret: secret, panicAt: "decode"}, Policy: testPolicy(t)})
	if _, err := panicDefinition.Decode(payload); !errors.Is(err, ErrCorrupt) || strings.Contains(fmt.Sprint(err), secret) {
		t.Fatalf("decode panic = %v", err)
	}
}

func TestDefinitionHoldersNeverFormatRetainedCodecOrUpcasterState(t *testing.T) {
	const secret = "private-codec-field-material"
	codecV1 := secretStringCodec{id: builtinCodecID("secret-codec"), version: 1, secret: secret}
	codecV2 := secretStringCodec{id: builtinCodecID("secret-codec"), version: 2, secret: secret}
	upcaster := Upcast(codecV1, codecV2, func(value string) (string, error) { return value, nil })
	spec := DefinitionSpec[string]{Name: testJobName(t, "codec.formatted"), Codec: codecV2, Upcasters: []Upcaster{upcaster}, Policy: testPolicy(t)}
	definition := MustDefine(spec)
	generated := GeneratedDefinitionSpec[string]{Name: testJobName(t, "codec.automatic-formatted"), Codec: &codecV2, Upcasters: []Upcaster{upcaster}}
	automatic := Auto(Handler[string](func(context.Context, string) error { return nil }))
	MustMaterialize(automatic, generated)
	catalog := MustCatalog(definition, automatic)
	values := []any{spec, &spec, generated, &generated, definition, *definition, upcaster, automatic, catalog, &catalog}
	for index, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			formatted := fmt.Sprintf(format, value)
			if strings.Contains(formatted, secret) {
				t.Fatalf("holder %d format %q leaked %q", index, format, formatted)
			}
		}
	}
}

func mustEncodedPayload(t *testing.T, codec string, version SchemaVersion, value string) EncodedPayload {
	t.Helper()
	payload, err := NewEncodedPayload(builtinCodecID(codec), version, []byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
