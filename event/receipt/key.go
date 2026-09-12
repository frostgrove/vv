package receipt

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/frostgrove/vv/event"
)

// The caller's own identity for one operation, minted before the command and
// carried with every retry of it. The framework never derives one: a key derived
// from the payload makes two genuinely different commands one operation — two
// identical credits are two operations and both must land — and a key derived
// from a request id is the caller's to supply.
//
// It renders "[operation key]" and never its value, because a key is a request
// identity and a refusal carries no data. Value is the one door the value comes
// back out of, and it exists for the ledger's own statement.
//
// The zero value is the one a caller can build and every door refuses it.
type Key struct{ value string }

// The kernel's own identifier rule, applied to a value this package stores in
// somebody else's primary key: non-empty, within MaxNameBytes, valid UTF-8, no
// control character and no bracket. The last is the frame a rendered field is
// written with, refused here for the reason event.checkName refuses it — a value
// that carries one closes a field some other renderer opened.
func NewKey(raw string) (Key, error) {
	if broken := checkKeyText(raw); broken != "" {
		return Key{}, fmt.Errorf("%w: the operation key %s, and a key is written into the ledger's primary key and compared as bytes", ErrSpec, broken)
	}
	return Key{value: strings.Clone(raw)}, nil
}

func (this Key) Zero() bool { return this.value == "" }

func (this Key) String() string { return "[operation key]" }

// What a Ledger binds as a parameter, and nothing else. Every other reader of a
// key is reading a request identity it has no business rendering: a log line, a
// refusal and an error message all take String.
func (this Key) Value() string { return this.value }

func checkKeyText(value string) string {
	if value == "" {
		return "is empty"
	}
	if len(value) > event.MaxNameBytes {
		return fmt.Sprintf("is longer than the %d bytes an identifier of this framework may hold", event.MaxNameBytes)
	}
	for index := 0; index < len(value); {
		decoded, size := utf8.DecodeRuneInString(value[index:])
		if decoded == utf8.RuneError && size <= 1 {
			return "is not valid UTF-8"
		}
		if unicode.IsControl(decoded) {
			return "contains a control character"
		}
		index += size
	}
	if strings.ContainsAny(value, "[]") {
		return "contains a bracket"
	}
	return ""
}
