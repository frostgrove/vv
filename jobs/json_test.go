package jobs

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

type codecIsZero int

type trustedDecodeString string

func (this *trustedDecodeString) UnmarshalJSON(encoded []byte) error {
	codecHookCalls.Add(1)
	var value string
	if err := json.Unmarshal(encoded, &value); err != nil {
		return err
	}
	*this = trustedDecodeString(value)
	return nil
}

func (codecIsZero) IsZero() bool {
	codecHookCalls.Add(1)
	return true
}

func TestSafeJSONRejectsUnsupportedShapesAndIsZeroWithoutInvocation(t *testing.T) {
	codecHookCalls.Store(0)
	tests := []any{
		JSON[any](1),
		JSON[interface{ Value() string }](1),
		JSON[func()](1),
		JSON[chan int](1),
		JSON[complex128](1),
		JSON[map[float64]int](1),
		JSON[struct {
			Value codecIsZero `json:",omitzero"`
		}](1),
		JSON[struct {
			Value codecIsZero `json:"-,omitzero"`
		}](1),
	}
	for index, codec := range tests {
		validator := codec.(interface{ validateCodec() error })
		if !errors.Is(validator.validateCodec(), ErrInvalid) {
			t.Fatalf("case %d did not fail closed: %v", index, validator.validateCodec())
		}
	}
	if codecHookCalls.Load() != 0 {
		t.Fatalf("validation invoked IsZero %d times", codecHookCalls.Load())
	}
	ignored := JSON[struct {
		Value codecHook `json:"-"`
	}](1)
	if safeJSONRuntimeSupported {
		if err := ignored.(interface{ validateCodec() error }).validateCodec(); err != nil {
			t.Fatalf("exact ignored field was not skipped: %v", err)
		}
	}
}

func TestJSONDashTagFallbackEscapingAndNumberBounds(t *testing.T) {
	if !safeJSONRuntimeSupported {
		t.Skip("safe JSON intentionally refuses jsonv2")
	}
	type namedDash struct {
		Value string `json:"-,omitempty"`
	}
	dash := JSON[namedDash](1)
	wantDash := `{"-":"value"}`
	if encoded, err := dash.Encode(namedDash{Value: "value"}, PayloadLimit{MaxBytes: len(wantDash), MaxDecodedBytes: 4096, MaxDepth: 2}); err != nil || string(encoded) != wantDash {
		t.Fatalf("named dash mismatch: %q, %v", encoded, err)
	}
	type invalidTag struct {
		FallbackName string `json:"bad\\name"`
	}
	fallback := JSON[invalidTag](1)
	wantFallback := `{"FallbackName":"value"}`
	if encoded, err := fallback.Encode(invalidTag{FallbackName: "value"}, PayloadLimit{MaxBytes: len(wantFallback), MaxDecodedBytes: 4096, MaxDepth: 2}); err != nil || string(encoded) != wantFallback {
		t.Fatalf("invalid tag fallback mismatch: %q, %v", encoded, err)
	}
	wantEscaped := `"\u003c\u003e\u0026"`
	if encoded, err := JSON[string](1).Encode("<>&", PayloadLimit{MaxBytes: len(wantEscaped), MaxDecodedBytes: 4096, MaxDepth: 1}); err != nil || string(encoded) != wantEscaped {
		t.Fatalf("escaped wire mismatch: %q, %v", encoded, err)
	}
	if encoded, err := JSON[json.Number](1).Encode(json.Number(""), PayloadLimit{MaxBytes: 1, MaxDecodedBytes: 4096, MaxDepth: 1}); err != nil || string(encoded) != "0" {
		t.Fatalf("empty number mismatch: %q, %v", encoded, err)
	}
	if _, err := JSON[json.Number](1).Encode(json.Number("not-a-number"), PayloadLimit{MaxBytes: 64, MaxDecodedBytes: 4096, MaxDepth: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid number classification mismatch: %v", err)
	}
}

func TestJSONScannerMatchesStandardGrammarAndStrictUTF8(t *testing.T) {
	if !safeJSONRuntimeSupported {
		t.Skip("safe JSON intentionally refuses jsonv2")
	}
	cases := [][]byte{
		[]byte(`null`),
		[]byte(" \t\r\n[true,false,{\"x\":-1.25e+3}] \n"),
		[]byte(`{"a":"\ud83d\ude00","a":"\ud800"}`),
		{'"', 0xff, '"'},
		{},
		[]byte(`01`),
		[]byte(`[1,]`),
		[]byte(`{"x":}`),
		[]byte(`true false`),
		[]byte(`"\u12xz"`),
	}
	for _, encoded := range cases {
		got := scanJSON(encoded, MaxPayloadDepth, nil) == nil
		want := json.Valid(encoded) && utf8.Valid(encoded)
		if got != want {
			t.Fatalf("scanner parity for %q = %t, want %t", encoded, got, want)
		}
	}
}

func FuzzJSONScannerMatchesStandardGrammar(f *testing.F) {
	for _, seed := range [][]byte{[]byte(`null`), []byte(`{"key":[1,true,"value"]}`), {'"', 0xff, '"'}, []byte(`[1,]`)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if !safeJSONRuntimeSupported || len(encoded) > 512 {
			t.Skip()
		}
		got := scanJSON(encoded, MaxPayloadDepth, nil) == nil
		want := json.Valid(encoded) && utf8.Valid(encoded)
		if got != want {
			t.Fatalf("scanner parity for %q = %t, want %t", encoded, got, want)
		}
	})
}

func TestJSONTypeProfilerAndRuntimeWorkAreHardBounded(t *testing.T) {
	deep := reflect.TypeFor[int]()
	for range jsonTypeMaximumDepth + 1 {
		deep = reflect.PointerTo(deep)
	}
	if _, err := newJSONTypeProfiler(false).charge(deep); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("deep pointer type was accepted: %v", err)
	}
	fields := make([]reflect.StructField, 1500)
	for index := range fields {
		fields[index] = reflect.StructField{Name: "Field" + strconv.Itoa(index), Type: reflect.TypeFor[int](), Tag: `json:"-"`}
	}
	wide := reflect.StructOf(fields)
	if _, err := newJSONTypeProfiler(false).charge(wide); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("constructor metadata work was accepted: %v", err)
	}
	if !safeJSONRuntimeSupported {
		return
	}
	fields = fields[:300]
	wide = reflect.StructOf(fields)
	profile, err := newJSONTypeProfiler(false).charge(wide)
	if err != nil {
		t.Fatal(err)
	}
	values := reflect.MakeSlice(reflect.SliceOf(wide), 256, 256)
	limit := PayloadLimit{MaxBytes: MaxPayloadBytes, MaxDecodedBytes: MaxDecodedBytes, MaxDepth: MaxPayloadDepth}
	if err := preflightJSONEncode(values, limit, profile.encodeWork); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("repeated ignored-field scan escaped work cap: %v", err)
	}
}

func TestJSONDecodeChargesStringsNumbersMapsAndEveryContainer(t *testing.T) {
	if !safeJSONRuntimeSupported {
		t.Skip("safe JSON intentionally refuses jsonv2")
	}
	mapCodec := JSON[[]map[string][128]byte](1).(jsonCodec[[]map[string][128]byte])
	inline := max(mapCodec.inline, int64(16))
	const objects = 2
	keyRaw, keyDecoded := testJSONStringCharge(t, []byte(`"x"`))
	keyCharge := keyRaw + 3*keyDecoded + jsonMapEntryBytes
	minimum := mapCodec.root + int64(1+objects*2)*2*inline + objects*(mapCodec.objectBytes+keyCharge)
	encodedMaps := []byte(`[{"x":null},{"x":null}]`)
	if _, err := mapCodec.Decode(encodedMaps, PayloadLimit{MaxBytes: len(encodedMaps), MaxDecodedBytes: int(minimum - 1), MaxDepth: 3}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("repeated map storage was undercharged: %v", err)
	}
	if value, err := mapCodec.Decode(encodedMaps, PayloadLimit{MaxBytes: len(encodedMaps), MaxDecodedBytes: int(minimum), MaxDepth: 3}); err != nil || len(value) != objects {
		t.Fatalf("exact map boundary failed: %#v, %v", value, err)
	}
	numberCodec := JSON[json.Number](1).(jsonCodec[json.Number])
	encodedNumber := []byte(strings.Repeat("9", 30))
	numberInline := max(numberCodec.inline, int64(16))
	numberMinimum := numberCodec.root + 2*numberInline + int64(len(encodedNumber))
	if _, err := numberCodec.Decode(encodedNumber, PayloadLimit{MaxBytes: len(encodedNumber), MaxDecodedBytes: int(numberMinimum - 1), MaxDepth: 1}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("number token was undercharged: %v", err)
	}
	escaped := []byte(`"\ud800"`)
	stringCodec := JSON[string](1).(jsonCodec[string])
	stringInline := max(stringCodec.inline, int64(16))
	stringMinimum := stringCodec.root + 2*stringInline + 3
	if _, err := stringCodec.Decode(escaped, PayloadLimit{MaxBytes: len(escaped), MaxDecodedBytes: int(stringMinimum - 1), MaxDepth: 1}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("escaped replacement was undercharged: %v", err)
	}
}

func TestJSONDecodeChargesRawScratchRetentionAndKeyFolding(t *testing.T) {
	if !safeJSONRuntimeSupported {
		t.Skip("safe JSON intentionally refuses jsonv2")
	}
	type record struct {
		Known int `json:"known"`
	}
	unknownText := strings.Repeat("a", 32<<10)
	unknownToken := `"` + strings.Repeat(`\u0061`, len(unknownText)) + `"`
	unknownWire := []byte(`{` + unknownToken + `:null}`)
	unknownCodec := JSON[record](1).(jsonCodec[record])
	unknownRaw, unknownDecoded := testJSONStringCharge(t, []byte(unknownToken))
	unknownInline := max(unknownCodec.inline, int64(16))
	unknownMinimum := unknownCodec.root + 4*unknownInline + unknownRaw + 2*unknownDecoded
	unknownLimit := PayloadLimit{MaxBytes: len(unknownWire), MaxDecodedBytes: int(unknownMinimum - 1), MaxDepth: 2}
	if _, err := unknownCodec.Decode(unknownWire, unknownLimit); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("unknown struct key scratch was undercharged: %v", err)
	}
	unknownLimit.MaxDecodedBytes++
	if value, err := unknownCodec.Decode(unknownWire, unknownLimit); err != nil || value != (record{}) {
		t.Fatalf("unknown struct key exact boundary failed: %#v, %v", value, err)
	}

	mapText := strings.Repeat("b", 1024)
	mapToken := `"` + strings.Repeat(`\u0062`, len(mapText)) + `"`
	mapWire := []byte(`{` + mapToken + `:1}`)
	mapCodec := JSON[map[string]int](1).(jsonCodec[map[string]int])
	mapRaw, mapDecoded := testJSONStringCharge(t, []byte(mapToken))
	mapInline := max(mapCodec.inline, int64(16))
	mapMinimum := mapCodec.root + 4*mapInline + mapCodec.objectBytes + mapRaw + 3*mapDecoded + jsonMapEntryBytes + 1
	mapLimit := PayloadLimit{MaxBytes: len(mapWire), MaxDecodedBytes: int(mapMinimum - 1), MaxDepth: 2}
	if _, err := mapCodec.Decode(mapWire, mapLimit); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("escaped map key scratch was undercharged: %v", err)
	}
	mapLimit.MaxDecodedBytes++
	if value, err := mapCodec.Decode(mapWire, mapLimit); err != nil || value[mapText] != 1 {
		t.Fatalf("escaped map key exact boundary failed: %#v, %v", value, err)
	}

	valueText := strings.Repeat("c", 16<<10)
	valueToken := `"` + strings.Repeat(`\u0063`, len(valueText)) + `"`
	valueWire := []byte(valueToken)
	valueCodec := JSON[string](1).(jsonCodec[string])
	valueRaw, valueDecoded := testJSONStringCharge(t, valueWire)
	valueInline := max(valueCodec.inline, int64(16))
	valueMinimum := valueCodec.root + 2*valueInline + valueRaw + valueDecoded
	valueLimit := PayloadLimit{MaxBytes: len(valueWire), MaxDecodedBytes: int(valueMinimum - 1), MaxDepth: 1}
	if _, err := valueCodec.Decode(valueWire, valueLimit); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("escaped value scratch was undercharged: %v", err)
	}
	valueLimit.MaxDecodedBytes++
	if value, err := valueCodec.Decode(valueWire, valueLimit); err != nil || value != valueText {
		t.Fatalf("escaped value exact boundary failed: %d, %v", len(value), err)
	}
}

func TestJSONDecodePreflightRejectsBeforeTrustedHook(t *testing.T) {
	codecHookCalls.Store(0)
	encoded := []byte(`"` + strings.Repeat(`\u0061`, 1024) + `"`)
	codec := TrustedJSON[trustedDecodeString](1).(jsonCodec[trustedDecodeString])
	raw, decoded := testJSONStringCharge(t, encoded)
	inline := max(codec.inline, int64(16))
	minimum := codec.root + 2*inline + raw + decoded
	limit := PayloadLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(minimum - 1), MaxDepth: 1}
	if _, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || codecHookCalls.Load() != 0 {
		t.Fatalf("preflight result = (%v, calls=%d)", err, codecHookCalls.Load())
	}
	limit.MaxDecodedBytes++
	value, err := codec.Decode(encoded, limit)
	if err != nil || len(value) != 1024 || codecHookCalls.Load() != 1 {
		t.Fatalf("exact hook boundary = (%d, %v, calls=%d)", len(value), err, codecHookCalls.Load())
	}
}

func testJSONStringCharge(t *testing.T, encoded []byte) (int64, int64) {
	t.Helper()
	next, raw, decoded, err := scanJSONString(encoded, 0)
	if err != nil || next != len(encoded) {
		t.Fatalf("string token profile = (%d, %d, %d, %v)", next, raw, decoded, err)
	}
	return raw, decoded
}
