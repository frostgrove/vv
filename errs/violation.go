package errs

import (
	"encoding/json"
	"sort"
	"strings"
)

type Origin uint8

const (
	OriginInput Origin = iota

	OriginState
)

func (this Origin) String() string {
	if this == OriginState {
		return "state"
	}
	return "input"
}

type Source struct {
	Table, Schema string
	Columns       []string
	Constraint    string
}

type Violation struct {
	Path    Path
	Code    Code
	Origin  Origin
	Message string

	Params map[string]any

	Source Source

	Approximate bool
}

func (this Violation) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	if len(this.Path) > 0 {
		field, err := this.Path.MarshalJSON()
		if err != nil {
			return nil, err
		}
		b.WriteString(`"field":`)
		b.Write(field)
		b.WriteByte(',')
	}
	code, err := json.Marshal(string(this.Code))
	if err != nil {
		return nil, err
	}
	b.WriteString(`"error_code":`)
	b.Write(code)
	if this.Message != "" {
		message, err := json.Marshal(this.Message)
		if err != nil {
			return nil, err
		}
		b.WriteString(`,"message":`)
		b.Write(message)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

func (this Violation) String() string {
	var b strings.Builder
	if len(this.Path) > 0 {
		b.WriteString(this.Path.String())
		b.WriteString(": ")
	}
	b.WriteString(string(this.Code))
	if this.Message != "" {
		b.WriteString(": ")
		b.WriteString(this.Message)
	}
	return b.String()
}

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

func comparePaths(a, b Path) int {
	for i := range a {
		if i >= len(b) {
			return 1
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
