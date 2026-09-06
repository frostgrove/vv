package tenancy

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxReferenceBytes = 128

type Reference struct{ value string }

func ParseReference(raw string) (Reference, error) {
	reference := Reference{value: strings.Clone(raw)}
	if !reference.valid() {
		return Reference{}, ErrMalformed
	}
	return reference, nil
}

func (this Reference) Value() string  { return this.value }
func (this Reference) IsZero() bool   { return this.value == "" }
func (this Reference) String() string { return "[tenant reference]" }

func (this Reference) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

func (this Reference) LogValue() slog.Value { return slog.StringValue(this.String()) }

func (Reference) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("tenancy: a tenant reference cannot be serialized: %w", ErrMalformed)
}

// The reference is compared byte for byte and never folded: whether `Acme` and
// `acme` are one tenant is the control plane's answer, and a library that folded
// here would merge two tenants the control plane keeps apart.
func (this Reference) valid() bool {
	if this.value == "" || len(this.value) > MaxReferenceBytes || !utf8.ValidString(this.value) {
		return false
	}
	if strings.TrimSpace(this.value) != this.value {
		return false
	}
	for _, r := range this.value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

type Epoch uint64

func NewEpoch(value uint64) (Epoch, error) {
	if value == 0 {
		return 0, ErrMalformed
	}
	return Epoch(value), nil
}

func (this Epoch) Value() uint64 { return uint64(this) }
func (this Epoch) IsZero() bool  { return this == 0 }
func (this Epoch) valid() bool   { return this != 0 }
