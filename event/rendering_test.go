package event

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

type forbiddenValue struct {
	what  string
	value string
}

func forbiddenValues() []forbiddenValue {
	return []forbiddenValue{
		{"a stream key", "acme%2Fevil/A-17"},
		{"a payload", `{"amount":4210,"holder":"Ada"}`},
		{"a version", "4611686018427387904"},
		{"a position", "9007199254740993"},
		{"a cursor", "eyJwb3NpdGlvbiI6NDJ9"},
		{"an identity", "postgres://admin:hunter2@ledger/db"},
		{"a driver's text", `pq: password authentication failed for user "admin"`},
	}
}

func loudCause() error {
	spoken := make([]string, 0, len(forbiddenValues()))
	for _, forbidden := range forbiddenValues() {
		spoken = append(spoken, forbidden.value)
	}
	return errors.New(strings.Join(spoken, " "))
}

type rendering struct {
	what     string
	rendered string
}

func everyRendering(t *testing.T) []rendering {
	t.Helper()
	loud := loudCause()
	rendered := []rendering{}
	for _, entry := range declaredVocabulary() {
		rendered = append(rendered, rendering{entry.name, entry.err.Error()})
	}
	for value := range 256 {
		rendered = append(rendered,
			rendering{"an outcome", Outcome(value).String()},
			rendering{"a capability answer", Support(value).String()})
	}
	for _, door := range doors() {
		for _, outcome := range classifiedOutcomes() {
			rendered = append(rendered,
				rendering{"a " + outcome.name + " failure at " + door.name + " door", door.refuse(Failure(outcome.outcome, loud)).Error()},
				rendering{"a " + outcome.name + " classification", Failure(outcome.outcome, loud).Error()})
		}
	}
	rendered = append(rendered,
		rendering{"an upcaster's refusal", upcastRefusal(loud).Error()},
		rendering{"a transaction question's refusal", refuseTransaction(loud).Error()},
		rendering{"an over-a-bound refusal", tooLarge("the payload", 2<<20, 1<<20).Error()},
		rendering{"a backing", Backing{identity: "postgres://admin:hunter2@ledger/db"}.String()},
		rendering{"a backing no store stated", Backing{}.String()})

	held, err := NewAuthority(mustBack(t, "postgres://admin:hunter2@ledger/db"), &connection{dsn: "one open transaction"})
	if err != nil {
		t.Fatalf("an authority was refused: %v", err)
	}
	rendered = append(rendered,
		rendering{"an authority", held.String()},
		rendering{"an authority nobody minted", Authority{}.String()})
	for _, stream := range everyStreamRendering() {
		rendered = append(rendered, rendering{stream.what, stream.rendered})
	}
	return rendered
}

func mustBack(t *testing.T, identity any) Backing {
	t.Helper()
	backing, err := NewBacking(identity)
	if err != nil {
		t.Fatalf("a backing over %v was refused: %v", identity, err)
	}
	return backing
}

func everyStreamRendering() []rendering {
	key := Key("acme%2Fevil/A-17")
	return []rendering{
		{"a stream", Stream{Family: "accounts.account", Key: key}.String()},
		{"a stream whose family carries a newline", Stream{Family: "orders\n\tFAKE LOG LINE: admin logged in", Key: key}.String()},
		{"a stream whose family is over the name cap", Stream{Family: strings.Repeat("f", MaxNameBytes+1), Key: key}.String()},
		{"a stream whose family is not valid UTF-8", Stream{Family: "orders\xff\xfe", Key: key}.String()},
		{"a stream whose family carries a NUL", Stream{Family: "orders\x00admin", Key: key}.String()},
		{"a stream with no family", Stream{Key: key}.String()},
	}
}

func TestEveryRenderingNamesAClassAndNeverAValue(t *testing.T) {
	t.Run("no rendering carries a value the caller or the store supplied", func(t *testing.T) {
		for _, rendered := range everyRendering(t) {
			if rendered.rendered == "" {
				t.Fatalf("%s renders nothing", rendered.what)
			}
			for _, forbidden := range forbiddenValues() {
				if strings.Contains(rendered.rendered, forbidden.value) {
					t.Fatalf("%s renders %s: %q", rendered.what, forbidden.what, rendered.rendered)
				}
			}
			for _, character := range rendered.rendered {
				if unicode.IsControl(character) {
					t.Fatalf("%s renders the control character %q, so one log line becomes two: %q",
						rendered.what, character, rendered.rendered)
				}
			}
		}
	})

	t.Run("the values it must not render were there to be rendered", func(t *testing.T) {
		carried := CauseOf(refuseRead(Failure(NotWritten, loudCause())))
		if carried == nil {
			t.Fatal("the store's own error did not survive as a cause, so the renderings above had nothing to leak")
		}
		for _, forbidden := range forbiddenValues() {
			if !strings.Contains(carried.Error(), forbidden.value) {
				t.Fatalf("the cause the refusals were built from does not carry %s, so nothing above was tested", forbidden.what)
			}
		}
	})

	t.Run("a rendering says which kind of thing it is and which one of them", func(t *testing.T) {
		named := map[string]string{}
		for _, entry := range declaredVocabulary() {
			if already, seen := named[entry.err.Error()]; seen {
				t.Fatalf("%s and %s render the same message, so an operator cannot tell from a log line which refusal was raised: %q",
					already, entry.name, entry.err.Error())
			}
			named[entry.err.Error()] = entry.name
		}
		for _, door := range doors() {
			for _, outcome := range classifiedOutcomes() {
				refused := door.refuse(Failure(outcome.outcome, loudCause()))
				if refused.Error() != outcome.at(door).Error() {
					t.Fatalf("a %s failure at %s door renders %q where its own sentinel reads %q, so a refusal does not say which refusal it is",
						outcome.name, door.name, refused.Error(), outcome.at(door).Error())
				}
			}
		}
		for _, built := range []struct {
			what     string
			refusal  error
			sentinel error
		}{
			{"an upcaster's refusal", upcastRefusal(loudCause()), ErrUpcast},
			{"a transaction question's refusal", refuseTransaction(loudCause()), ErrAmbientNotTransaction},
			{"an over-a-bound refusal", tooLarge("the payload", 2<<20, 1<<20), ErrTooLarge},
		} {
			if built.refusal.Error() != built.sentinel.Error() {
				t.Fatalf("%s renders %q where its own sentinel reads %q", built.what, built.refusal.Error(), built.sentinel.Error())
			}
		}

		answered := map[string]bool{}
		for _, answer := range []Support{Unstated, Unsupported, Supported} {
			answered[answer.String()] = true
		}
		if len(answered) != 3 {
			t.Fatalf("the three capability answers render %d phrases, so a capability nobody stated reads as one a store denied", len(answered))
		}

		stated := mustBack(t, "postgres://admin:hunter2@ledger/db")
		if stated.String() == (Backing{}).String() {
			t.Fatalf("a backing a store stated and one it never stated both render %q, so a store that never said what it writes to reads as one that did", stated.String())
		}
		held, err := NewAuthority(stated, &connection{dsn: "one open transaction"})
		if err != nil {
			t.Fatalf("an authority was refused: %v", err)
		}
		kinds := []struct {
			what     string
			rendered []string
		}{
			{"a backing", []string{stated.String(), (Backing{}).String()}},
			{"an authority", []string{held.String(), (Authority{}).String()}},
			{"a capability answer", []string{Unstated.String(), Unsupported.String(), Supported.String()}},
			{"a classification", []string{Unclassified.String(), Conflict.String(), NotWritten.String(),
				Unconfirmed.String(), Closed.String(), BadCursor.String(), Refused.String()}},
		}
		for _, kind := range kinds {
			for _, other := range kinds {
				if kind.what == other.what {
					continue
				}
				for _, left := range kind.rendered {
					for _, right := range other.rendered {
						if left == right {
							t.Fatalf("%s and %s both render %q, so two values of two different kinds read alike in a log line", kind.what, other.what, left)
						}
					}
				}
			}
		}
	})

	t.Run("a family a store supplied is rendered only when it passed the kernel's own rule", func(t *testing.T) {
		named := Stream{Family: "accounts.account", Key: "acme/A-17"}.String()
		if !strings.Contains(named, "accounts.account") {
			t.Fatalf("a declared family is not named in %q, so every stream reads alike in a log line", named)
		}
		for _, rendered := range everyStreamRendering()[1:] {
			if rendered.rendered != "[stream unnameable]" {
				t.Fatalf("%s rendered %q rather than a classification", rendered.what, rendered.rendered)
			}
			if len(rendered.rendered) > MaxNameBytes {
				t.Fatalf("%s rendered %d bytes", rendered.what, len(rendered.rendered))
			}
		}
	})

	t.Run("a conflict names no version and no accessor reaches one", func(t *testing.T) {
		conflicted := refuseAppend(Failure(Conflict, errors.New("the stream is at version 42")))
		if strings.ContainsAny(conflicted.Error(), "0123456789") {
			t.Fatalf("a conflict renders %q, and a caller that read a version out of it would re-append a stale decision at a fresh one",
				conflicted.Error())
		}
		shape := reflect.TypeOf(conflicted)
		for index := range shape.NumMethod() {
			method := shape.Method(index)
			for result := range method.Type.NumOut() {
				switch method.Type.Out(result).Kind() {
				case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64:
					t.Fatalf("a refusal answers %s with a number, which is the one recovery a conflict must not offer", method.Name)
				}
			}
		}
	})
}
