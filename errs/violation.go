package errs

import (
	"encoding/json"
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
