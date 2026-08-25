package errs

import (
	"encoding/json"
	"sort"
	"strings"
)

// An Origin says where a violation came from, and three rules key off it.
//
// Status: an input rule is 422, a collision with stored state is 409. The
// oracle controls: only OriginState reveals something the caller could not
// already see, so the never-echo-the-value default keys off it completely
// rather than per constraint. And the probe: an input violation means the
// payload is already known bad, so nothing is probed with values that have
// failed validation.
type Origin uint8

const (
	// OriginInput means the payload alone was wrong. No stored state was read.
	OriginInput Origin = iota
	// OriginState means it collided with what is stored, and Source is populated.
	OriginState
)

// String names the origin, for a log line.
func (o Origin) String() string {
	if o == OriginState {
		return "state"
	}
	return "input"
}

// Source is storage-side provenance: which table, which columns, which
// constraint. It is populated only when [Origin] is [OriginState], and it is
// internal — nothing here ever reaches a response body ([[D-044]]).
//
// Not to be confused with crud.Source, which is a datasource.
type Source struct {
	Table, Schema string
	Columns       []string
	Constraint    string
}

// A Violation is one thing that was wrong.
//
// A constraint the database refused to break and a rule a validator refused are
// the same type in the same list, told apart by [Origin]. Merging them is the
// entire point: a payload with a malformed email and a taken email is two
// violations at one path, and a client making two round trips to learn that is
// the problem this subsystem exists to remove.
//
// A Violation carries no [Kind]. A kind is one per [Fault]; a violation's is
// derived from its [Code] through the wired [Codes] value.
type Violation struct {
	Path    Path
	Code    Code
	Origin  Origin
	Message string
	// Params feeds a message template: {"max": 255}. It stays server-side.
	// Rendering it would put an internal name one interpolation away from a
	// response body, which is the quiet half of [[D-044]].
	Params map[string]any
	// Source is populated only when Origin is OriginState.
	Source Source
	// Approximate marks a path that could not be resolved and was not invented.
	// A layer that would have to guess a hop it does not own must say so
	// instead of guessing ([[D-043]]); [Chain] reports the declined hop that
	// sets this.
	Approximate bool
}

// MarshalJSON emits the public shape and nothing else: field, error_code,
// message. No origin, no params, no source, no approximate — a marker for a
// consumer's own logic is not a thing to hand a client, and every other field
// here names something internal.
//
// The receiver is the value one, and that is load-bearing rather than
// stylistic. With a pointer receiver, json.Marshal of a Violation value, of a
// map entry and of a struct field all bypass this method and emit Source in
// full — three of the five ways a violation is ever marshalled. That is
// [[D-044]] carried by the type instead of remembered by a renderer.
func (v Violation) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	if len(v.Path) > 0 {
		field, err := v.Path.MarshalJSON()
		if err != nil {
			return nil, err
		}
		b.WriteString(`"field":`)
		b.Write(field)
		b.WriteByte(',')
	}
	code, err := json.Marshal(string(v.Code))
	if err != nil {
		return nil, err
	}
	b.WriteString(`"error_code":`)
	b.Write(code)
	if v.Message != "" {
		msg, err := json.Marshal(v.Message)
		if err != nil {
			return nil, err
		}
		b.WriteString(`,"message":`)
		b.Write(msg)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// String carries the same three things the public shape does and never
// [Source]. A violation printed into a log with %v must not be the thing that
// ships a constraint name.
func (v Violation) String() string {
	var b strings.Builder
	if len(v.Path) > 0 {
		b.WriteString(v.Path.String())
		b.WriteString(": ")
	}
	b.WriteString(string(v.Code))
	if v.Message != "" {
		b.WriteString(": ")
		b.WriteString(v.Message)
	}
	return b.String()
}

// SortViolations puts a list into the total order the envelope renders in, so
// the same failing request twice produces byte-identical output — the
// violation-order analogue of [[D-014]], and what makes a response body
// testable at all.
//
// The order is Path, then Origin, then Code, then Message:
//
//   - Path first, because a client renders a form by field. Grouping by
//     anything else scatters one field's two violations, which is exactly what
//     a form cannot use. Names sort before indices at the same depth and a
//     shorter path sorts first.
//   - Origin second: at one path an input violation comes before a collision. A
//     malformed value explains a failed lookup, and the reverse reads as
//     nonsense.
//   - Code, then Message, to make it total.
//
// Two keys `ROADMAP-errors.md` §8 named are deliberately absent, and this is
// the phase that owed the resolution. **Kind** is not a key: it is what the
// status is chosen from, one per response, and sorting by it would put a
// path's `unique` before the same path's `invalid_format` — the reverse of the
// rule above. **The constraint name** is not the last tiebreaker: it exists
// only on [Source] and only for [OriginState], so it is not total across two
// input violations, and it would let an internal name decide a public order.
//
// The sort is stable, so two violations equal on all four keys keep the order
// they were produced in.
func SortViolations(vs []Violation) {
	sort.SliceStable(vs, func(i, j int) bool { return less(vs[i], vs[j]) })
}

func less(a, b Violation) bool {
	if c := comparePaths(a.Path, b.Path); c != 0 {
		return c < 0
	}
	if a.Origin != b.Origin {
		return a.Origin < b.Origin
	}
	if a.Code != b.Code {
		return a.Code < b.Code
	}
	return a.Message < b.Message
}

// comparePaths orders two paths step by step. A name outranks an index at the
// same depth because a client reading `["items", 0]` beside `["items","name"]`
// is reading one object's field list, and the named half is the half it has a
// label for.
func comparePaths(a, b Path) int {
	for i := range a {
		if i >= len(b) {
			return 1 // b is a prefix of a, so b is shorter and sorts first
		}
		x, y := a[i], b[i]
		switch {
		case !x.IsIndex && y.IsIndex:
			return -1
		case x.IsIndex && !y.IsIndex:
			return 1
		case x.IsIndex && y.IsIndex:
			if x.Index != y.Index {
				return cmp(x.Index, y.Index)
			}
		default:
			if x.Name != y.Name {
				return strings.Compare(x.Name, y.Name)
			}
		}
	}
	if len(b) > len(a) {
		return -1
	}
	return 0
}

func cmp(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
