package errs

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// A Step is one hop into a payload: a named member, or a position in a list.
//
// Index and IsIndex are two fields rather than one negative sentinel because
// zero is a real position: an index step is the first element of a list far
// more often than any other.
type Step struct {
	Name    string
	Index   int
	IsIndex bool
}

// Named is a member step.
func Named(name string) Step { return Step{Name: name} }

// Indexed is a position step.
func Indexed(i int) Step { return Step{Index: i, IsIndex: true} }

// A Path is where a violation happened, in the shape the client sent — names
// and positions, never columns. It is the one place a public payload and the
// schema's vocabulary touch, and the translation between them belongs to the
// layer that performed the mapping ([[D-043]]).
//
// Three renderings, one value: the JSON array for the envelope, the dotted
// form for a log, the RFC 6901 pointer for a problem document.
type Path []Step

// MarshalJSON renders ["items",3,"email"] — an index as a number, a name as a
// string. An empty path renders as [] and never as null: the envelope's field
// is an array a client may measure.
//
// The receiver is the value one. With a pointer receiver, marshalling a Path
// held in a struct field, a map entry, or on its own bypasses this method
// entirely and emits the struct.
func (p Path) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range p {
		if i > 0 {
			b.WriteByte(',')
		}
		if s.IsIndex {
			b.WriteString(strconv.Itoa(s.Index))
			continue
		}
		name, err := json.Marshal(s.Name)
		if err != nil {
			return nil, err
		}
		b.Write(name)
	}
	b.WriteByte(']')
	return []byte(b.String()), nil
}

// UnmarshalJSON reads back what [Path.MarshalJSON] wrote: a JSON array whose
// members are strings and numbers. A member of any other type is refused rather
// than skipped — a path with a step missing addresses a different field, and a
// client marking a form would mark the wrong one.
//
// This exists and [Violation.UnmarshalJSON] deliberately does not. A path is
// the whole of itself on the wire, so reading one back loses nothing; a
// violation is three public fields out of seven ([[D-044]]), so a method that
// looked like the inverse of its marshaller would hand back a value whose
// Origin, Params and Source are the zero value and say so nowhere. Whoever
// reads a violation off a wire has to see that they are getting three fields,
// which is why each transport spells its own shape out.
func (p *Path) UnmarshalJSON(b []byte) error {
	var steps []json.RawMessage
	if err := json.Unmarshal(b, &steps); err != nil {
		return fmt.Errorf("errs: a path is a JSON array: %w", err)
	}
	out := make(Path, 0, len(steps))
	for i, raw := range steps {
		switch {
		case len(raw) > 0 && raw[0] == '"':
			var name string
			if err := json.Unmarshal(raw, &name); err != nil {
				return fmt.Errorf("errs: path step %d: %w", i, err)
			}
			out = append(out, Named(name))
		default:
			var idx int
			if err := json.Unmarshal(raw, &idx); err != nil {
				return fmt.Errorf("errs: path step %d is neither a name nor an index: %w", i, err)
			}
			out = append(out, Indexed(idx))
		}
	}
	if len(out) == 0 {
		// An empty array is a violation that names no field, and nil is how
		// that is spelled everywhere else here — len(v.Path) > 0 is what sorts
		// it into the envelope's general group.
		*p = nil
		return nil
	}
	*p = out
	return nil
}

// String renders items[3].email, for a log line.
//
// An index step never takes a leading dot, which is the half a naive rendering
// gets wrong: joining names alone yields items.email, and appending a bracket
// after an unconditional dot yields items.[3]. Neither is a path any framework
// recognises.
//
// It is lossy on purpose. A member whose name contains a dot or a bracket
// renders as if it were two steps, and nothing escapes it, because a log line
// is read by a person. [ParsePath] round-trips everything else; that name is
// the documented exception.
func (p Path) String() string {
	var b strings.Builder
	for i, s := range p {
		if s.IsIndex {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(s.Index))
			b.WriteByte(']')
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.Name)
	}
	return b.String()
}

// Pointer renders /items/3/email, per RFC 6901.
//
// The escaping is the reason this is a method and not a strings.Join: a member
// named a/b would otherwise produce a pointer that silently addresses a
// different node, and nothing downstream would catch it. It applies here and in
// neither other rendering — the envelope's field is an array, where a slash is
// an ordinary character.
func (p Path) Pointer() string {
	var b strings.Builder
	for _, s := range p {
		b.WriteByte('/')
		if s.IsIndex {
			b.WriteString(strconv.Itoa(s.Index))
			continue
		}
		b.WriteString(escapePointer(s.Name))
	}
	return b.String()
}

// The order matters: escaping the slash first would then have its tilde
// escaped again, turning /a/b into /a~01b.
func escapePointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

// ParsePath reads the dotted-and-bracketed form back into a Path.
//
// It parses two things and nothing else: this package's own [Path.String]
// output, and the Namespace() a validation library reports. It is never pointed
// at a driver's message — a path read out of engine text is exactly what
// [[D-039]] refuses, and the text is localised besides.
//
// Bracketed digits are a position; anything else in brackets is part of the
// name, so Items3 stays a name and Items[3] does not.
func ParsePath(s string) Path {
	if s == "" {
		return nil
	}
	var (
		p    Path
		name strings.Builder
	)
	flush := func() {
		if name.Len() > 0 {
			p = append(p, Named(name.String()))
			name.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch s[i] {
		case '.':
			flush()
			i++
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				name.WriteByte(s[i])
				i++
				continue
			}
			end += i
			n, err := strconv.Atoi(s[i+1 : end])
			if err != nil || n < 0 {
				name.WriteString(s[i : end+1])
				i = end + 1
				continue
			}
			flush()
			p = append(p, Indexed(n))
			i = end + 1
		default:
			name.WriteByte(s[i])
			i++
		}
	}
	flush()
	return p
}
