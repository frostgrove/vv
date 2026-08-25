package port_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/port"
)

// slug is a key with parsing rules of its own, which is the case FormatID and
// CoerceID exist for: an int would survive fmt.Sprint and strconv on its own.
type slug struct{ v string }

func (s slug) MarshalText() ([]byte, error) { return []byte("s-" + s.v), nil }

func (s *slug) UnmarshalText(b []byte) error {
	t, ok := strings.CutPrefix(string(b), "s-")
	if !ok {
		return errors.New("a slug key starts with s-")
	}
	s.v = t
	return nil
}

// A key crosses a transport as text and has to come back the same key. Both
// halves are needed and neither is enough: a client that formats a uuid with
// fmt.Sprint sends [16]byte's Go syntax, and a server that coerced it would
// answer 400 on a key it had itself handed out.
func TestAKeyThatWentOutAsTextComesBackTheSameKey(t *testing.T) {
	t.Run("int64", func(t *testing.T) { roundTripKey(t, int64(42), "42") })
	t.Run("int", func(t *testing.T) { roundTripKey(t, 7, "7") })
	t.Run("uint32", func(t *testing.T) { roundTripKey(t, uint32(9), "9") })
	t.Run("string", func(t *testing.T) { roundTripKey(t, "hello-world", "hello-world") })
	t.Run("own rules", func(t *testing.T) { roundTripKey(t, slug{"go"}, "s-go") })

	// The control. Without it every assertion above would also pass for a
	// CoerceID that ignored its argument and answered the zero value.
	if _, err := port.CoerceID[slug]("go"); err == nil {
		t.Fatal("a slug without its prefix was accepted, so the round trip above proves nothing")
	}
}

func roundTripKey[ID comparable](t *testing.T, id ID, wantText string) {
	t.Helper()
	text := port.FormatID(id)
	if text != wantText {
		t.Fatalf("%v went out as %q, and the other end reads %q", id, text, wantText)
	}
	back, err := port.CoerceID[ID](text)
	if err != nil {
		t.Fatalf("%q did not come back as a key: %v", text, err)
	}
	if back != id {
		t.Fatalf("%v came back as %v", id, back)
	}
}

// This is the property the whole client exists for. A caller who branched on
// errors.Is against a repository in this process keeps that branch when the
// repository moves to another one — and nothing else in the library arranges
// it, because the wire carries a kind and a code and no Go error at all.
func TestADecodedFaultStillMatchesTheSentinelItLeftWith(t *testing.T) {
	cases := []struct {
		name string
		kind errs.Kind
		code errs.Code
		want error
		// not is a sentinel this fault must not match, so an implementation
		// that wrapped every sentinel at once fails rather than passing.
		not error
	}{
		{"not found", errs.KindNotFound, errs.CodeNotFound, crud.ErrNotFound, crud.ErrConflict},
		{"forbidden", errs.KindForbidden, errs.CodeForbidden, crud.ErrForbidden, crud.ErrNotFound},
		{"conflict", errs.KindConflict, errs.CodeUnique, crud.ErrConflict, crud.ErrNotFound},
		{"stale version", errs.KindConflict, errs.CodeStaleVersion, crud.ErrStaleVersion, crud.ErrNotFound},
		{"bad request", errs.KindBadRequest, errs.CodeBadQuery, port.ErrBadRequest, crud.ErrNotFound},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := port.FaultFrom(c.kind, c.code, nil, false)
			if !errors.Is(f, c.want) {
				t.Fatalf("a decoded %s does not match the sentinel a local one does", c.name)
			}
			if errors.Is(f, c.not) {
				t.Fatalf("a decoded %s also matches %v, so the branch a caller wrote is wrong", c.name, c.not)
			}
			// The classification has to survive too, or a gateway re-rendering
			// this fault would answer a different status than the service did.
			if got := port.KindOf(f); got != c.kind {
				t.Fatalf("a decoded %s classifies as %v", c.name, got)
			}
		})
	}
}

// A stale write is a conflict as well as a stale write. Wrapping the coarse
// sentinel would keep the status right and lose the branch a caller re-reads
// the row from.
func TestADecodedStaleVersionKeepsBothBranches(t *testing.T) {
	f := port.FaultFrom(errs.KindConflict, errs.CodeStaleVersion, nil, false)
	if !errors.Is(f, crud.ErrStaleVersion) {
		t.Fatal("the finer branch is gone")
	}
	if !errors.Is(f, crud.ErrConflict) {
		t.Fatal("the coarse branch is gone")
	}
}

// Origin is not on the wire, and its zero value is the wrong answer for the one
// kind that has a right one: a conflict collided with stored state, and calling
// it OriginInput blames the payload for something the payload could not have
// known.
func TestADecodedConflictSaysItCollidedWithStoredState(t *testing.T) {
	vs := []errs.Violation{{Path: errs.Path{errs.Named("email")}, Code: errs.CodeUnique}}

	conflict := port.FaultFrom(errs.KindConflict, errs.CodeUnique, vs, false)
	if got := conflict.Violations[0].Origin; got != errs.OriginState {
		t.Fatalf("a decoded conflict reports %v", got)
	}

	// The control: a validation failure really is the payload's fault, so this
	// must be the other answer or the derivation is a constant.
	invalid := port.FaultFrom(errs.KindValidation, errs.CodeCheck, vs, false)
	if got := invalid.Violations[0].Origin; got != errs.OriginInput {
		t.Fatalf("a decoded validation failure reports %v", got)
	}
}

// A set the sender cut short arrives saying so. Losing the marker turns "here
// are four of them" into "there were four".
func TestADecodedPartialSetSaysItIsIncomplete(t *testing.T) {
	vs := []errs.Violation{{Code: errs.CodeUnique}}
	if !port.FaultFrom(errs.KindConflict, errs.CodeUnique, vs, true).Partial {
		t.Fatal("the partial marker did not survive the wire")
	}
	if port.FaultFrom(errs.KindConflict, errs.CodeUnique, vs, false).Partial {
		t.Fatal("a complete set arrived marked partial")
	}
}

// Every violation has to arrive, with its path and its code. One that carries a
// path lands as a pathed violation and one that does not stays general, because
// that is what decides which half of the envelope a gateway re-renders it into.
func TestEveryDecodedViolationKeepsItsPathAndItsCode(t *testing.T) {
	f := port.FaultFrom(errs.KindValidation, errs.CodeCheck, []errs.Violation{
		{Path: errs.Path{errs.Named("items"), errs.Indexed(2), errs.Named("email")}, Code: errs.CodeUnique, Message: "taken"},
		{Code: errs.CodeCheck},
	}, false)

	if len(f.Violations) != 2 {
		t.Fatalf("%d of 2 violations survived", len(f.Violations))
	}
	first := f.Violations[0]
	if got := first.Path.String(); got != "items[2].email" {
		t.Fatalf("the path arrived as %q", got)
	}
	if first.Code != errs.CodeUnique || first.Message != "taken" {
		t.Fatalf("the violation arrived as %+v", first)
	}
	if len(f.Violations[1].Path) != 0 {
		t.Fatalf("a general violation was given the path %v", f.Violations[1].Path)
	}
}
