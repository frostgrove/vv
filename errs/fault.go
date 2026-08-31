package errs

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

type Detail struct {
	Dialect    string
	SQLState   string
	Native     int
	Constraint string
	Table      string
	Columns    []string
	Value      string
	RefTable   string
	RefColumns []string
	Driver     error
}

type Fault struct {
	Kind       Kind
	Code       Code
	Message    string
	Violations []Violation
	Op         string
	Entity     string
	Partial    bool
	Detail     Detail

	wrapped []error
}

func (this *Fault) Error() string {
	var b strings.Builder
	b.WriteString("errs: ")
	switch {
	case this.Op != "" && this.Entity != "":
		b.WriteString(this.Op)
		b.WriteByte(' ')
		b.WriteString(this.Entity)
		b.WriteString(": ")
	case this.Op != "":
		b.WriteString(this.Op)
		b.WriteString(": ")
	case this.Entity != "":
		b.WriteString(this.Entity)
		b.WriteString(": ")
	}
	b.WriteString(this.Kind.String())
	if this.Code != "" {
		b.WriteString(": ")
		b.WriteString(string(this.Code))
	}
	if n := len(this.Violations); n > 0 {
		b.WriteString(" (")
		b.WriteString(strconv.Itoa(n))
		b.WriteString(" violation")
		if n != 1 {
			b.WriteByte('s')
		}
		b.WriteByte(')')
	}
	return b.String()
}

func (this *Fault) Unwrap() []error { return this.wrapped }

func (this Fault) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteString(`{"kind":`)
	kind, err := this.Kind.MarshalJSON()
	if err != nil {
		return nil, err
	}
	b.Write(kind)
	if this.Code != "" {
		code, err := json.Marshal(string(this.Code))
		if err != nil {
			return nil, err
		}
		b.WriteString(`,"error_code":`)
		b.Write(code)
	}
	b.WriteString(`,"violations":`)
	if len(this.Violations) == 0 {
		b.WriteString("[]")
	} else {
		vs, err := json.Marshal(this.Violations)
		if err != nil {
			return nil, err
		}
		b.Write(vs)
	}
	if this.Partial {
		b.WriteString(`,"partial":true`)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

func (this Fault) String() string { return this.Error() }

func AsFault(err error) (*Fault, bool) {
	var f *Fault
	if errors.As(err, &f) {
		return f, true
	}
	return nil, false
}
