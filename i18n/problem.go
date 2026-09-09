package i18n

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidCatalog = errors.New("i18n: invalid catalog")
	ErrInvalidLocale  = errors.New("i18n: invalid locale policy")
	ErrInvalidMessage = errors.New("i18n: invalid message")
	ErrLimitExceeded  = errors.New("i18n: limit exceeded")
	ErrNotFound       = errors.New("i18n: message not found")
)

type ProblemCode string

const (
	ProblemInvalid       ProblemCode = "invalid"
	ProblemDuplicate     ProblemCode = "duplicate"
	ProblemCollision     ProblemCode = "canonical_collision"
	ProblemMissing       ProblemCode = "missing"
	ProblemUnsupported   ProblemCode = "unsupported"
	ProblemStale         ProblemCode = "stale"
	ProblemCycle         ProblemCode = "cycle"
	ProblemLimit         ProblemCode = "limit"
	ProblemSchema        ProblemCode = "schema"
	ProblemSecurity      ProblemCode = "security"
	ProblemInvalidSyntax ProblemCode = "invalid_syntax"
)

func (c ProblemCode) String() string { return string(c) }

func (c ProblemCode) Valid() bool {
	switch c {
	case ProblemInvalid, ProblemDuplicate, ProblemCollision, ProblemMissing, ProblemUnsupported, ProblemStale, ProblemCycle, ProblemLimit, ProblemSchema, ProblemSecurity, ProblemInvalidSyntax:
		return true
	default:
		return false
	}
}

type Problem struct {
	Code   ProblemCode
	Path   string
	Detail string
}

type Problems struct {
	problems []Problem
}

func (p *Problems) Error() string {
	if p == nil || len(p.problems) == 0 {
		return ErrInvalidCatalog.Error()
	}
	var b strings.Builder
	b.WriteString(ErrInvalidCatalog.Error())
	for _, problem := range p.problems {
		b.WriteString("; ")
		b.WriteString(problem.Path)
		b.WriteString(": ")
		b.WriteString(string(problem.Code))
		if problem.Detail != "" {
			b.WriteString(" (")
			b.WriteString(problem.Detail)
			b.WriteByte(')')
		}
	}
	return b.String()
}

func (p *Problems) Unwrap() []error {
	causes := []error{ErrInvalidCatalog}
	if p != nil {
		for _, problem := range p.problems {
			if problem.Code == ProblemLimit {
				return append(causes, ErrLimitExceeded)
			}
		}
	}
	return causes
}

func (p *Problems) Items() []Problem {
	if p == nil {
		return nil
	}
	return slices.Clone(p.problems)
}

type problemSet struct {
	items []Problem
	full  bool
}

func (p *problemSet) add(code ProblemCode, path, detail string) {
	const maxProblems = 256
	if len(p.items) >= maxProblems {
		if !p.full {
			p.full = true
			p.items = append(p.items, Problem{Code: ProblemLimit, Path: "problems", Detail: "additional construction problems were omitted"})
		}
		return
	}
	p.items = append(p.items, Problem{Code: code, Path: boundedProblemText(path), Detail: boundedProblemText(detail)})
}

func (p *problemSet) addError(code ProblemCode, path string, err error) {
	if err != nil {
		p.add(code, path, err.Error())
	}
}

func (p *problemSet) err() error {
	if len(p.items) == 0 {
		return nil
	}
	slices.SortFunc(p.items, func(a, b Problem) int {
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		if a.Code != b.Code {
			return strings.Compare(string(a.Code), string(b.Code))
		}
		return strings.Compare(a.Detail, b.Detail)
	})
	return &Problems{problems: slices.Clone(p.items)}
}

func problemPath(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func boundedProblemText(value string) string {
	const maximum = 1024
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
