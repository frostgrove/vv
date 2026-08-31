package errs

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Step struct {
	Name    string
	Index   int
	IsIndex bool
}

func Named(name string) Step { return Step{Name: name} }

func Indexed(i int) Step { return Step{Index: i, IsIndex: true} }

type Path []Step

func (this Path) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range this {
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

func (this *Path) UnmarshalJSON(b []byte) error {
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
		*this = nil
		return nil
	}
	*this = out
	return nil
}

func (this Path) String() string {
	var b strings.Builder
	for i, s := range this {
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

func (this Path) Pointer() string {
	var b strings.Builder
	for _, s := range this {
		b.WriteByte('/')
		if s.IsIndex {
			b.WriteString(strconv.Itoa(s.Index))
			continue
		}
		b.WriteString(escapePointer(s.Name))
	}
	return b.String()
}

func escapePointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

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
