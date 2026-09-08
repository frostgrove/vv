package event

import (
	"strings"
	"unicode/utf8"
)

type Key string

type Version uint64

type Position uint64

type Cursor string

type Stream struct {
	Family string
	Key    Key
}

// A family that arrived on an envelope is a store's data and not a declared
// identifier, so it is rendered only when it passes the rule a declaration is
// held to. Otherwise this is the refusal that a forged log line, a second
// bracketed field, a newline or a megabyte of family name would have travelled
// in.
func (this Stream) String() string {
	if checkName(this.Family) != "" {
		return fieldOpen + "stream unnameable" + fieldClose
	}
	return fieldOpen + "stream " + this.Family + fieldClose
}

const (
	composeSeparator = '/'
	composeEscape    = '%'
	composeDigits    = "0123456789ABCDEF"
)

// Composes an identity's parts into one key, and the rendering is frozen: it is
// part of every stream ever written under it, so changing it orphans every
// aggregate that used it — each one reads as version 0, silently. Each part is
// escaped and the escaped parts are joined with a separator; what the escape
// covers is the separator, the escape byte itself, and exactly what the kernel's
// text rule refuses, read through that rule's own predicates so the two cannot
// drift apart. The result is therefore legal text for any parts at all, and two
// distinct part lists never render one key — the single pair that renders equal
// is no parts and one empty part, and both are refused as empty at every door.
func Compose(parts ...string) Key {
	var rendered strings.Builder
	for index, part := range parts {
		if index > 0 {
			rendered.WriteByte(composeSeparator)
		}
		escapePart(&rendered, part)
	}
	return Key(rendered.String())
}

func escapePart(rendered *strings.Builder, part string) {
	for index := 0; index < len(part); {
		decoded, size := utf8.DecodeRuneInString(part[index:])
		switch {
		case invalidRune(decoded, size):
			escapeByte(rendered, part[index])
			index++
		case controlRune(decoded):
			for offset := 0; offset < size; offset++ {
				escapeByte(rendered, part[index+offset])
			}
			index += size
		case decoded == composeSeparator || decoded == composeEscape:
			escapeByte(rendered, part[index])
			index++
		default:
			rendered.WriteString(part[index : index+size])
			index += size
		}
	}
}

func escapeByte(rendered *strings.Builder, value byte) {
	rendered.WriteByte(composeEscape)
	rendered.WriteByte(composeDigits[value>>4])
	rendered.WriteByte(composeDigits[value&0x0f])
}
