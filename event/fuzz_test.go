package event

import (
	"bytes"
	"errors"
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

// The bytes a fold reads are a store's, and a store's are whatever was written
// into the log by an older deploy, a hand-edited row or a codec that has since
// changed. So the reader chain is driven directly, at revisions it retains and
// revisions it does not, over payloads nobody wrote.
func FuzzAStoredPayloadIsFoldedOrRefused(f *testing.F) {
	for _, seed := range []struct {
		revision int
		payload  string
	}{
		{1, `{"Minor":250}`},
		{2, `{"Minor":250,"Reason":"deposit"}`},
		{2, `{"Minor":9223372036854775807,"Reason":"\ud800"}`},
		{2, `{"Minor":1e400}`},
		{1, `{"Minor":-1,"Reason":null}`},
		{2, `{}`},
		{2, `null`},
		{2, `[1,2,3]`},
		{2, `{"Minor":"250"}`},
		{2, "{\"Reason\":\"\x00\"}"},
		{2, "\xff\xfe"},
		{2, ``},
		{0, `{"Minor":1}`},
		{3, `{"Minor":1}`},
		{-1, `{"Minor":1}`},
		{2, strings.Repeat(`[`, 200)},
	} {
		f.Add(seed.revision, []byte(seed.payload))
	}

	aggregate := Define[balance]("fuzz.balances", accountKey)
	credited := Declare(aggregate, "fuzz.credited",
		Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2),
		func(this balance, event creditedV2) balance {
			return this + balance(event.Minor) + balance(len(event.Reason))
		})

	for _, unreadable := range []string{`[1,2,3]`, `{"Minor":"250"}`, "\xff\xfe", ""} {
		if _, err := credited.apply(0, 2, []byte(unreadable)); !errors.Is(err, ErrPayload) {
			f.Fatalf("stored bytes no declaration can read (%q) folded with %v; without a payload the declaration must refuse, \"folded or refused\" is satisfied by a codec that refuses nothing at all, because a zero value folds deterministically and every execution below takes the second arm", unreadable, err)
		}
	}

	f.Fuzz(func(t *testing.T, revision int, payload []byte) {
		state, err := credited.apply(0, revision, bytes.Clone(payload))
		if err != nil {
			if !errors.Is(err, ErrRevision) && !errors.Is(err, ErrPayload) {
				t.Fatalf("stored bytes at revision %d were refused with %v, which is neither of the two refusals a recorded fact this declaration cannot read is allowed to be", revision, err)
			}
			if errors.Is(err, ErrDeclaration) || errors.Is(err, ErrKey) {
				t.Fatalf("stored bytes at revision %d were reported as a programmer's or a caller's fault: %v", revision, err)
			}
			if state != 0 {
				t.Fatalf("a refused fold answered %d rather than the state it was given", state)
			}
			return
		}
		again, second := credited.apply(0, revision, bytes.Clone(payload))
		if second != nil || again != state {
			t.Fatalf("the same bytes at revision %d folded to %d and then to %d (%v), so what a replay produces depends on something other than the bytes", revision, state, again, second)
		}
	})
}

func FuzzADecidedFactIsFrozenAgainstItsCallersBuffer(f *testing.F) {
	for _, seed := range []string{"", "one", "\x00\xff", "{\"Minor\":1}", strings.Repeat("f", 300), "\xff\xfe\xfd", "рога"} {
		f.Add([]byte(seed))
	}

	acme := accountID{tenant: "acme", number: "A-17"}
	aggregate := Define[carrier]("fuzz.notes", accountKey)
	written := Declare(aggregate, "fuzz.written", From[note](aliasCodec{}), carryNote)

	f.Fuzz(func(t *testing.T, body []byte) {
		mine := bytes.Clone(body)
		change := written.New(acme, note{Body: mine})
		if err := change.Err(); err != nil {
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("a payload of %d bytes was refused with %v", len(mine), err)
			}
			return
		}
		if cap(change.payload) != len(change.payload) {
			t.Fatalf("a decision of %d bytes was frozen into an array with %d bytes of capacity behind it", len(mine), cap(change.payload)-len(change.payload))
		}
		decided := bytes.Clone(mine)
		for index := range mine {
			mine[index] ^= 0xff
		}
		folded, err := aggregate.Fold(acme, carrier{}, change)
		if err != nil {
			t.Fatalf("the fold of a decided fact was refused: %v", err)
		}
		if !bytes.Equal(folded.Body, decided) {
			t.Fatalf("the recorded fact reads %q after the caller overwrote its own payload, and it was decided as %q", folded.Body, decided)
		}
	})
}
