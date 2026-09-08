package event

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The verb each argument is rendered by, aligned with the call's own arguments
// so index nought is the format string itself. Two shapes shift that alignment
// and both may sit behind flags: an argument index (`%[2]s`, `%+[2]v`) renames
// which argument a verb reaches, and a star (`%*d`, `%-*d`, `%.*f`) consumes an
// argument for the width or the precision. A format carrying either is refused
// rather than read wrongly, because a mis-read mapping hands the wrong verb to
// the check beside this file — which either excuses a rendered key or reports a
// wire type name that was never one.
//
// Everything else here is `fmt`'s own reading of a verb, in `fmt`'s own order:
// flags, then an argument index or a star width, then the width, then a
// precision that only exists when a byte follows the dot, then the verb. That
// order is what the fuzz target beside this differentially checks against
// `fmt`, and getting it approximately right is how the mapping goes silently
// off by one.
func verbsIn(format string) ([]byte, bool) {
	verbs := []byte{0}
	for index := 0; index < len(format); index++ {
		if format[index] != '%' {
			continue
		}
		index++
		for index < len(format) && strings.IndexByte("+-# 0", format[index]) >= 0 {
			index++
		}
		if shifted(format, index) {
			return nil, false
		}
		index = digitsFrom(format, index)
		if index+1 < len(format) && format[index] == '.' {
			index++
			if shifted(format, index) {
				return nil, false
			}
			index = digitsFrom(format, index)
		}
		if index < len(format) && format[index] != '%' {
			verbs = append(verbs, format[index])
		}
	}
	return verbs, true
}

func shifted(format string, index int) bool {
	return index < len(format) && (format[index] == '[' || format[index] == '*')
}

func digitsFrom(format string, index int) int {
	for index < len(format) && format[index] >= '0' && format[index] <= '9' {
		index++
	}
	return index
}

func verbsPerArgument(call *ast.CallExpr) ([]byte, bool) {
	format, isText := firstArgument(call)
	if !isText {
		return nil, true
	}
	return verbsIn(format)
}

func firstArgument(call *ast.CallExpr) (string, bool) {
	if len(call.Args) == 0 {
		return "", false
	}
	literal, isLiteral := call.Args[0].(*ast.BasicLit)
	if !isLiteral || literal.Kind != token.STRING {
		return "", false
	}
	format, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return format, true
}

func TestAFormatThatShiftsItsOwnArgumentsIsRefusedRatherThanReadWrongly(t *testing.T) {
	for _, spoken := range []struct {
		format   string
		verbs    string
		readable bool
	}{
		{format: "%w: %s", verbs: "ws", readable: true},
		{format: "%w: the sample is a %T", verbs: "wT", readable: true},
		{format: "%w: %q at revision %d", verbs: "wqd", readable: true},
		{format: "%w: %8T", verbs: "wT", readable: true},
		{format: "%w: %-12.4f", verbs: "wf", readable: true},
		{format: "a literal %% and no verb", verbs: "", readable: true},
		{format: "a trailing percent %", verbs: "", readable: true},
		{format: "%w: %[2]s", readable: false},
		{format: "%w: %+[2]v", readable: false},
		{format: "%w: %*d", readable: false},
		{format: "%w: %-*d", readable: false},
		{format: "%w: %.*f", readable: false},
		{format: "%w: %6.*f", readable: false},
	} {
		verbs, readable := verbsIn(spoken.format)
		if readable != spoken.readable {
			t.Errorf("%s was %s and the mapping from an argument to the verb that renders it is only sound the other way",
				spoken.format, readableAs(readable))
			continue
		}
		if !readable {
			continue
		}
		if rendered := strings.TrimLeft(string(verbs), "\x00"); rendered != spoken.verbs {
			t.Errorf("%s renders its arguments with %q and the check beside this one was told %q", spoken.format, spoken.verbs, rendered)
		}
	}
}

func readableAs(readable bool) string {
	if readable {
		return "read verb by verb"
	}
	return "refused whole"
}

// The property every judgement rests on: the reader consumes exactly the
// arguments `fmt` does, so the verb it hands to the check beside this file is
// the verb that renders that argument. The oracle is `fmt` itself — hand it one
// argument per verb the reader counted and it reports a surplus as `%!(EXTRA`
// and a shortfall as `(MISSING)`. A format `fmt` cannot read at all is skipped:
// there is no alignment to hold when the standard library gives up on the verb.
var fmtGaveUp = regexp.MustCompile(`%!\((NOVERB|BADWIDTH|BADPREC|BADINDEX)\)`)

func FuzzAFormatIsMappedVerbByVerbOrRefusedWhole(f *testing.F) {
	for _, seed := range []string{
		"%w: %s", "%w: %[2]s", "%w: %+[2]v", "%w: %*d", "%w: %.*f",
		"%w: %-*d", "%%", "%", "%w: %8T", "%w: %q at revision %d",
		"", "ровно %s раз", "%\xff", "%[1]", "% s", "%#v",
		"%+d%%%[3]s", "%ÿ%s", "%.9999999999d", "%!", "%%*", "%0%*",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, format string) {
		verbs, readable := verbsIn(format)
		if !readable {
			if verbs != nil {
				t.Fatalf("%q was refused and %q came back with it — a refusal maps nothing at all", format, verbs)
			}
			return
		}
		if len(verbs) == 0 || verbs[0] != 0 {
			t.Fatalf("%q left the format string itself carrying %v, and index nought is the format and never an argument", format, verbs)
		}
		for _, verb := range verbs[1:] {
			if verb == '%' {
				t.Fatalf("%q rendered an argument with an escaped percent, which is literal text and never a verb", format)
			}
		}

		arguments := make([]any, len(verbs)-1)
		for index := range arguments {
			arguments[index] = index
		}
		rendered := fmt.Sprintf(format, arguments...)
		if fmtGaveUp.MatchString(rendered) {
			return
		}
		if strings.Contains(rendered, "%!(EXTRA") {
			t.Fatalf("%q renders fewer arguments than the %d this reader mapped, so a verb was attributed to an argument nothing renders: %q", format, len(arguments), rendered)
		}
		if strings.Contains(rendered, "(MISSING)") {
			t.Fatalf("%q renders more arguments than the %d this reader mapped, so every verb after the first is off by one: %q", format, len(arguments), rendered)
		}
	})
}
