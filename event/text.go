package event

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	textEmpty     = "is empty"
	textOverCap   = "is longer than its limit"
	textNotUTF8   = "is not valid UTF-8"
	textControl   = "contains a control character"
	textBracketed = "contains a bracket"
)

// The kernel text rule, in one place and applied to a key at every door it
// crosses and, through checkName, under every declared identifier's own rule:
// non-empty, valid UTF-8, no NUL and no other control character, within its cap.
// No case folding, no trimming, no Unicode normalisation — keys compare as
// bytes. The answer is one of a closed set of phrases, because a refusal names
// the rule that was broken and never the text that broke it.
func checkText(value string, limit int) string {
	if value == "" {
		return textEmpty
	}
	if len(value) > limit {
		return textOverCap
	}
	for index := 0; index < len(value); {
		decoded, size := utf8.DecodeRuneInString(value[index:])
		if invalidRune(decoded, size) {
			return textNotUTF8
		}
		if controlRune(decoded) {
			return textControl
		}
		index += size
	}
	return ""
}

// The two characters a rendered field is framed with, named here because the
// rule below refuses them and Stream.String writes them, and a frame the guard
// does not read is a guard that protects nothing.
const (
	fieldOpen  = "["
	fieldClose = "]"
)

// The declared-identifier rule — a stream family and a wire type name, the two
// MaxNameBytes governs — which is the text rule and the frame characters more.
// Both reach a refusal's text: a family renders unquoted inside a field of its
// own and a type name renders beside one, so either one carrying a delimiter
// closes the field the renderer opened and writes a second one after it, which
// is the forged log line the control rule refuses reached by the route the
// control rule does not cover. Quoting does not answer it — Go's quoting escapes
// quotes and non-printables and leaves these two alone. Each identifier is held
// to it at both doors it crosses: the declaration, so an identifier a program
// declared always names itself in a log line, and the envelope, because what a
// store recorded was declared by nobody this program can see.
func checkName(value string) string {
	if broken := checkText(value, MaxNameBytes); broken != "" {
		return broken
	}
	if strings.ContainsAny(value, fieldOpen+fieldClose) {
		return textBracketed
	}
	return ""
}

func invalidRune(decoded rune, size int) bool { return decoded == utf8.RuneError && size <= 1 }

func controlRune(decoded rune) bool { return unicode.IsControl(decoded) }
