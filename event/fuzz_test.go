package event

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func decompose(key string) ([]string, error) {
	parts := []string{}
	for _, field := range strings.Split(key, string(composeSeparator)) {
		var raw strings.Builder
		for index := 0; index < len(field); {
			if field[index] != composeEscape {
				raw.WriteByte(field[index])
				index++
				continue
			}
			if index+3 > len(field) {
				return nil, fmt.Errorf("an escape is cut short at byte %d", index)
			}
			value, err := strconv.ParseUint(field[index+1:index+3], 16, 8)
			if err != nil {
				return nil, fmt.Errorf("an escape at byte %d is not two hex digits: %w", index, err)
			}
			raw.WriteByte(byte(value))
			index += 3
		}
		parts = append(parts, raw.String())
	}
	return parts, nil
}

func FuzzComposeRendersAKeyThatIsLegalAndReversible(f *testing.F) {
	for _, seed := range [][3]string{
		{"acme", "A-17", "2026"},
		{"acme/evil", "A-17", ""},
		{"acme", "evil/A-17", "%2F"},
		{"%", "%25", "%%"},
		{"рога", "копыта", "17"},
		{"\x00", "\x7f", ""},
		{"\xff\xfe", "ok", "\xc3"},
		{"", "", ""},
		{strings.Repeat("f", 300), "b", "c"},
		{"a\nb", "c\td", "e\rf"},
		{"a/b", "a", "b"},
		{"�", "‮", "\x00\u0085"},
	} {
		f.Add(seed[0], seed[1], seed[2])
	}

	f.Fuzz(func(t *testing.T, first, second, third string) {
		key := string(Compose(first, second, third))
		if broken := checkText(key, len(key)); broken != "" {
			t.Fatalf("a composed key %s, and the mapper the framework recommends would be refused at every door: %q", broken, key)
		}
		parts, err := decompose(key)
		if err != nil {
			t.Fatalf("a composed key does not read back: %v: %q", err, key)
		}
		want := []string{first, second, third}
		if !slices.Equal(parts, want) {
			t.Fatalf("a composed key reads back as %q where the identity was %q, so two identities can render one stream and their histories are one",
				parts, want)
		}
	})
}
