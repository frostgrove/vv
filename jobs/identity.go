package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Name struct{ value string }

func ParseName(raw string) (Name, error) {
	value, err := parseRegistryName(raw, MaxNameBytes, "name")
	if err != nil {
		return Name{}, err
	}
	return Name{value: value}, nil
}

func (n Name) Value() string  { return n.value }
func (n Name) String() string { return n.value }
func (n Name) IsZero() bool   { return n.value == "" }
func (n Name) valid() bool    { return validRegistryName(n.value, MaxNameBytes) }

type QueueName struct{ value string }

func ParseQueueName(raw string) (QueueName, error) {
	value, err := parseRegistryName(raw, MaxQueueNameBytes, "queue name")
	if err != nil {
		return QueueName{}, err
	}
	return QueueName{value: value}, nil
}

func (n QueueName) Value() string  { return n.value }
func (n QueueName) String() string { return n.value }
func (n QueueName) IsZero() bool   { return n.value == "" }
func (n QueueName) valid() bool    { return validRegistryName(n.value, MaxQueueNameBytes) }

type BindingName struct{ value string }

func ParseBindingName(raw string) (BindingName, error) {
	value, err := parseRegistryName(raw, MaxBindingNameBytes, "binding name")
	if err != nil {
		return BindingName{}, err
	}
	return BindingName{value: value}, nil
}

func (n BindingName) Value() string  { return n.value }
func (n BindingName) String() string { return n.value }
func (n BindingName) IsZero() bool   { return n.value == "" }
func (n BindingName) valid() bool    { return validRegistryName(n.value, MaxBindingNameBytes) }

type CodecID struct{ value string }

func ParseCodecID(raw string) (CodecID, error) {
	value, err := parseRegistryName(raw, MaxCodecIDBytes, "codec id")
	if err != nil {
		return CodecID{}, err
	}
	return CodecID{value: value}, nil
}

func (id CodecID) Value() string  { return id.value }
func (id CodecID) String() string { return id.value }
func (id CodecID) IsZero() bool   { return id.value == "" }
func (id CodecID) valid() bool    { return validRegistryName(id.value, MaxCodecIDBytes) }

type BuildID struct{ value string }

func ParseBuildID(raw string) (BuildID, error) {
	if raw == "" {
		return BuildID{}, invalid("build id is empty")
	}
	if len(raw) > MaxBuildIDBytes {
		return BuildID{}, tooLarge("build id")
	}
	if !asciiAlphaNumeric(raw[0]) || !asciiAlphaNumeric(raw[len(raw)-1]) {
		return BuildID{}, invalid("build id has an unsupported boundary")
	}
	for index := 0; index < len(raw); index++ {
		value := raw[index]
		if !asciiAlphaNumeric(value) && !strings.ContainsRune("._:/@+-", rune(value)) {
			return BuildID{}, invalid("build id contains an unsupported character")
		}
	}
	return BuildID{value: strings.Clone(raw)}, nil
}

func (id BuildID) Value() string  { return id.value }
func (id BuildID) String() string { return id.value }
func (id BuildID) IsZero() bool   { return id.value == "" }
func (id BuildID) valid() bool {
	parsed, err := ParseBuildID(id.value)
	return err == nil && parsed.value == id.value
}

type ProducerIntent struct {
	value    string
	rejected bool
}

func Intent(raw string) ProducerIntent {
	if len(raw) > MaxIntentBytes {
		return ProducerIntent{rejected: true}
	}
	return ProducerIntent{value: strings.Clone(raw)}
}

func ParseIntent(raw string) (ProducerIntent, error) {
	if len(raw) > MaxIntentBytes {
		return ProducerIntent{}, tooLarge("intent")
	}
	intent := Intent(raw)
	if !intent.valid() {
		return ProducerIntent{}, invalid("intent has invalid text")
	}
	return intent, nil
}

func (i ProducerIntent) String() string { return "[job intent]" }
func (i ProducerIntent) IsZero() bool   { return i.value == "" && !i.rejected }
func (i ProducerIntent) valid() bool {
	if i.rejected || i.value == "" || len(i.value) > MaxIntentBytes || strings.TrimSpace(i.value) != i.value || !utf8.ValidString(i.value) {
		return false
	}
	for _, value := range i.value {
		if unicode.IsControl(value) || value == '\u2028' || value == '\u2029' {
			return false
		}
	}
	return true
}
func (i ProducerIntent) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, i.String())
}
func (i ProducerIntent) LogValue() slog.Value { return slog.StringValue(i.String()) }
func (ProducerIntent) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: producer intent cannot be serialized", ErrUnsupported)
}

type LegacyIntent struct{ value string }

func RestoreLegacyIntent(raw string) (LegacyIntent, error) {
	intent, err := ParseIntent(raw)
	if err != nil {
		return LegacyIntent{}, err
	}
	return protectLegacyIntent(intent), nil
}

func (i LegacyIntent) Value() string  { return strings.Clone(i.value) }
func (i LegacyIntent) IsZero() bool   { return i.value == "" }
func (i LegacyIntent) String() string { return "[legacy job intent]" }
func (i LegacyIntent) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, i.String())
}
func (i LegacyIntent) LogValue() slog.Value { return slog.StringValue(i.String()) }
func (LegacyIntent) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: legacy intent cannot be serialized", ErrUnsupported)
}
func (i LegacyIntent) valid() bool {
	return ProducerIntent{value: i.value}.valid()
}

func protectLegacyIntent(intent ProducerIntent) LegacyIntent {
	if !intent.valid() {
		return LegacyIntent{}
	}
	return LegacyIntent{value: strings.Clone(intent.value)}
}

type IntentDigest struct{ value [IntentDigestBytes]byte }

func IntentDigestFromBytes(value [IntentDigestBytes]byte) (IntentDigest, error) {
	if value == [IntentDigestBytes]byte{} {
		return IntentDigest{}, invalid("intent digest is zero")
	}
	return IntentDigest{value: value}, nil
}

func (d IntentDigest) Bytes() [IntentDigestBytes]byte { return d.value }
func (d IntentDigest) IsZero() bool                   { return d.value == [IntentDigestBytes]byte{} }
func (d IntentDigest) String() string                 { return "[job intent digest]" }
func (d IntentDigest) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d IntentDigest) valid() bool { return !d.IsZero() }

type InvocationID struct{ value [16]byte }

func NewInvocationID() (InvocationID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return InvocationID{}, err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return InvocationID{value: value}, nil
}

func InvocationIDFromBytes(value [16]byte) (InvocationID, error) {
	if value == [16]byte{} {
		return InvocationID{}, invalid("invocation id is zero")
	}
	return InvocationID{value: value}, nil
}

func ParseInvocationID(raw string) (InvocationID, error) {
	if len(raw) != 36 || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' {
		return InvocationID{}, invalid("invocation id is not canonical")
	}
	var value [16]byte
	segments := [...]struct {
		encoded string
		decoded []byte
	}{
		{raw[0:8], value[0:4]},
		{raw[9:13], value[4:6]},
		{raw[14:18], value[6:8]},
		{raw[19:23], value[8:10]},
		{raw[24:36], value[10:16]},
	}
	for _, segment := range segments {
		if _, err := hex.Decode(segment.decoded, []byte(segment.encoded)); err != nil {
			return InvocationID{}, invalid("invocation id is not canonical")
		}
	}
	id, err := InvocationIDFromBytes(value)
	if err != nil || id.String() != raw {
		return InvocationID{}, invalid("invocation id is not canonical")
	}
	return id, nil
}

func (id InvocationID) Bytes() [16]byte { return id.value }
func (id InvocationID) IsZero() bool    { return id.value == [16]byte{} }
func (id InvocationID) valid() bool     { return !id.IsZero() }
func (id InvocationID) String() string {
	if id.IsZero() {
		return ""
	}
	var encoded [36]byte
	hex.Encode(encoded[0:8], id.value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], id.value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], id.value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], id.value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], id.value[10:16])
	return string(encoded[:])
}

func parseRegistryName(raw string, limit int, field string) (string, error) {
	if raw == "" {
		return "", invalid(field + " is empty")
	}
	if len(raw) > limit {
		return "", tooLarge(field)
	}
	if !validRegistryName(raw, limit) {
		return "", invalid(field + " contains an unsupported character sequence")
	}
	return strings.Clone(raw), nil
}

func validRegistryName(raw string, limit int) bool {
	if raw == "" || len(raw) > limit || !asciiLowerAlphaNumeric(raw[0]) || !asciiLowerAlphaNumeric(raw[len(raw)-1]) {
		return false
	}
	separator := false
	for index := 1; index < len(raw)-1; index++ {
		value := raw[index]
		if asciiLowerAlphaNumeric(value) {
			separator = false
			continue
		}
		if value != '.' && value != '-' && value != '_' || separator {
			return false
		}
		separator = true
	}
	return true
}

func asciiLowerAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func asciiAlphaNumeric(value byte) bool {
	return asciiLowerAlphaNumeric(value) || value >= 'A' && value <= 'Z'
}
