package event

import (
	"unicode"
	"unicode/utf8"
)

const (
	textEmpty   = "is empty"
	textOverCap = "is longer than its limit"
	textNotUTF8 = "is not valid UTF-8"
	textControl = "contains a control character"
)

// The kernel text rule, in one place and applied to a key at every door it
// crosses and to a declared identifier at declaration: non-empty, valid UTF-8,
// no NUL and no other control character, within its cap. No case folding, no
// trimming, no Unicode normalisation — keys compare as bytes. The answer is one
// of a closed set of phrases, because a refusal names the rule that was broken
// and never the text that broke it.
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

func invalidRune(decoded rune, size int) bool { return decoded == utf8.RuneError && size <= 1 }

func controlRune(decoded rune) bool { return unicode.IsControl(decoded) }
