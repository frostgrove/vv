package jobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestRegistryIdentitiesAcceptOnlyBoundedPortableNames(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		parse func(string) (string, error)
	}{
		{"name", MaxNameBytes, func(raw string) (string, error) { value, err := ParseName(raw); return value.Value(), err }},
		{"queue", MaxQueueNameBytes, func(raw string) (string, error) { value, err := ParseQueueName(raw); return value.Value(), err }},
		{"binding", MaxBindingNameBytes, func(raw string) (string, error) { value, err := ParseBindingName(raw); return value.Value(), err }},
		{"codec", MaxCodecIDBytes, func(raw string) (string, error) { value, err := ParseCodecID(raw); return value.Value(), err }},
	}
	valid := []string{"a", "workspace.translate", "legacy_job-2", "json"}
	invalidValues := []string{"", "Upper", ".start", "end-", "two..parts", "two-_parts", "white space", "кириллица", "line\nbreak"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, raw := range valid {
				if len(raw) > test.limit {
					continue
				}
				got, err := test.parse(raw)
				if err != nil || got != raw {
					t.Fatalf("parse %q = (%q, %v)", raw, got, err)
				}
			}
			exact := "a" + strings.Repeat("b", test.limit-1)
			if got, err := test.parse(exact); err != nil || got != exact {
				t.Fatalf("exact byte bound = (%d, %v)", len(got), err)
			}
			if _, err := test.parse(exact + "c"); !errors.Is(err, ErrTooLarge) {
				t.Fatalf("oversize error = %v", err)
			}
			for _, raw := range invalidValues {
				if _, err := test.parse(raw); !errors.Is(err, ErrInvalid) {
					t.Fatalf("parse %q error = %v", raw, err)
				}
			}
		})
	}
	if !(Name{}).IsZero() || !(QueueName{}).IsZero() || !(BindingName{}).IsZero() || !(CodecID{}).IsZero() {
		t.Fatal("zero registry identity was not zero")
	}
}

func TestBuildIDUsesASeparateBoundedPortableAlphabet(t *testing.T) {
	valid := []string{"a", "git:ABC123", "registry.example/image@sha256:0123", "release_2026-09-01+hotfix"}
	for _, raw := range valid {
		id, err := ParseBuildID(raw)
		if err != nil || id.Value() != raw || id.String() != raw || id.IsZero() || !id.valid() {
			t.Fatalf("ParseBuildID(%q) = (%q, %v)", raw, id.Value(), err)
		}
	}
	exact := "a" + strings.Repeat("b", MaxBuildIDBytes-1)
	if _, err := ParseBuildID(exact); err != nil {
		t.Fatalf("exact byte bound: %v", err)
	}
	if _, err := ParseBuildID(exact + "c"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	for _, raw := range []string{"", "/leading", "trailing/", "white space", "bad\\path", "bad?query", "кириллица", "line\nbreak"} {
		if _, err := ParseBuildID(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ParseBuildID(%q) error = %v", raw, err)
		}
	}
	if !(BuildID{}).IsZero() || (BuildID{}).valid() {
		t.Fatal("zero build id is valid")
	}
}

func TestIntentCountsUTF8BytesAndNeverFormatsItsValue(t *testing.T) {
	exact := strings.Repeat("界", 170) + "ab"
	intent, err := ParseIntent(exact)
	if err != nil || intent.IsZero() || !intent.valid() {
		t.Fatalf("exact intent = (%v, %v)", intent, err)
	}
	if _, err := ParseIntent(exact + "c"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	for _, raw := range []string{"", " leading", "trailing ", "line\nbreak", "line\u2028break", string([]byte{0xff})} {
		if _, err := ParseIntent(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ParseIntent(%q) error = %v", raw, err)
		}
	}
	secret, err := ParseIntent("private-caller-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"%s", "%q", "%v", "%+v", "%#v"} {
		formatted := fmt.Sprintf(format, secret)
		if strings.Contains(formatted, "private-caller-token") || !strings.Contains(formatted, "job intent") {
			t.Fatalf("format %q = %q", format, formatted)
		}
	}
	if !(ProducerIntent{}).IsZero() || (ProducerIntent{}).valid() {
		t.Fatal("zero intent is valid")
	}
	legacy := protectLegacyIntent(secret)
	if legacy.IsZero() || !legacy.valid() || legacy.Value() != "private-caller-token" || strings.Contains(fmt.Sprintf("%#v", legacy), "private") {
		t.Fatalf("legacy intent = %+v", legacy)
	}
	if _, err := json.Marshal(legacy); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("legacy intent JSON = %v", err)
	}
	restored, err := RestoreLegacyIntent("private-caller-token")
	if err != nil || restored.Value() != legacy.Value() {
		t.Fatalf("restored legacy intent = %+v, %v", restored, err)
	}
	if _, err := RestoreLegacyIntent(strings.Repeat("x", MaxIntentBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized legacy intent = %v", err)
	}
	if _, err := RestoreLegacyIntent(" leading"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid legacy intent = %v", err)
	}
}

func TestIntentMagicDefersValidationAndCannotSerializeRawText(t *testing.T) {
	const raw = "private-caller-token"
	intent := Intent(raw)
	if intent.IsZero() || !intent.valid() {
		t.Fatal("valid magic intent was rejected")
	}
	for _, invalidIntent := range []ProducerIntent{Intent(""), Intent(" leading"), Intent("line\nbreak"), Intent(strings.Repeat("x", MaxIntentBytes+1))} {
		if invalidIntent.valid() {
			t.Fatalf("invalid magic intent passed operation validation: %v", invalidIntent)
		}
	}
	if _, err := ParseIntent(strings.Repeat(" ", MaxIntentBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized whitespace error = %v", err)
	}
	if _, err := ParseIntent(strings.Repeat("x", MaxIntentBytes) + string([]byte{0xff})); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized invalid UTF-8 error = %v", err)
	}
	encoded, err := json.Marshal(intent)
	if !errors.Is(err, ErrUnsupported) || len(encoded) != 0 || strings.Contains(fmt.Sprint(err), raw) {
		t.Fatalf("JSON serialization = (%q, %v)", encoded, err)
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logger.Info("intent", "value", intent)
	if strings.Contains(output.String(), raw) || !strings.Contains(output.String(), "job intent") {
		t.Fatalf("slog output = %q", output.String())
	}
}

func TestIntentDigestIsImmutableNonzeroAndAlwaysRedacted(t *testing.T) {
	var raw [IntentDigestBytes]byte
	for index := range raw {
		raw[index] = byte(index + 1)
	}
	digest, err := IntentDigestFromBytes(raw)
	if err != nil || digest.IsZero() || !digest.valid() || digest.Bytes() != raw {
		t.Fatalf("digest = (%v, %v)", digest, err)
	}
	copy := digest.Bytes()
	copy[0] = 0xff
	if digest.Bytes() != raw {
		t.Fatal("byte getter aliased the intent digest")
	}
	for _, format := range []string{"%s", "%q", "%x", "%v", "%+v", "%#v"} {
		formatted := fmt.Sprintf(format, digest)
		if strings.Contains(formatted, "01020304") || !strings.Contains(formatted, "job intent digest") {
			t.Fatalf("format %q = %q", format, formatted)
		}
	}
	if _, err := IntentDigestFromBytes([IntentDigestBytes]byte{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero digest error = %v", err)
	}
	if !(IntentDigest{}).IsZero() || (IntentDigest{}).valid() {
		t.Fatal("zero intent digest is valid")
	}
}

func TestInvocationIDIsCanonicalAndGeneratedAsUUIDV4(t *testing.T) {
	bytes := [16]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x46, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	id, err := InvocationIDFromBytes(bytes)
	if err != nil {
		t.Fatal(err)
	}
	const encoded = "00112233-4455-4677-8899-aabbccddeeff"
	if id.String() != encoded || id.IsZero() || !id.valid() || id.Bytes() != bytes {
		t.Fatalf("id = (%q, %x)", id.String(), id.Bytes())
	}
	parsed, err := ParseInvocationID(encoded)
	if err != nil || parsed != id {
		t.Fatalf("parse = (%v, %v)", parsed, err)
	}
	copy := id.Bytes()
	copy[0] = 0xff
	if id.Bytes() != bytes {
		t.Fatal("byte getter aliased the invocation id")
	}
	for _, raw := range []string{"", strings.ToUpper(encoded), "00112233445546778899aabbccddeeff", "00112233-4455-4677-8899-aabbccddeezz", "00000000-0000-0000-0000-000000000000"} {
		if _, err := ParseInvocationID(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ParseInvocationID(%q) error = %v", raw, err)
		}
	}
	if _, err := InvocationIDFromBytes([16]byte{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero bytes error = %v", err)
	}
	generated, err := NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	generatedBytes := generated.Bytes()
	if generated.IsZero() || generatedBytes[6]>>4 != 4 || generatedBytes[8]&0xc0 != 0x80 {
		t.Fatalf("generated id = %q", generated)
	}
	if reparsed, err := ParseInvocationID(generated.String()); err != nil || reparsed != generated {
		t.Fatalf("generated round trip = (%v, %v)", reparsed, err)
	}
	if (InvocationID{}).String() != "" || !(InvocationID{}).IsZero() || (InvocationID{}).valid() {
		t.Fatal("zero invocation id is valid")
	}
}
