package errs_test

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
)

const corpusDir = "sqlerr/testdata/corpus"

// shimFault is the same shape with no MarshalJSON of its own. It is the control
// for every leak assertion below: it says what the default marshal does, which
// is not what ROADMAP-errors.md §5 assumed. The Driver field does not make the
// marshal fail — it succeeds, and emits the driver error's exported fields.
type shimFault struct {
	Kind       string
	Code       string
	Message    string
	Violations []shimViolation
	Detail     errs.Detail
}

// The Origin field is errs.Origin and not a bare uint8: Origin has a String
// method, so fmt prints it as "state" on the shim exactly as it would inside a
// real violation. Declared as a number the print control below could not show
// the field leaking at all.
type shimViolation struct {
	Path    errs.Path
	Code    string
	Origin  errs.Origin
	Message string
	Params  map[string]any
	Source  errs.Source
}

// corpusFault builds one fault from a captured violation, together with every
// string the capture says must not reach a client and a shim of the same shape
// with none of this package's methods on it. The three leak tests — marshal,
// print and Error() — need the same fixture and the same control, and a fixture
// invented rather than captured proves nothing about a real driver.
func corpusFault(t *testing.T, engine string) (*errs.Fault, []string, shimFault) {
	t.Helper()

	c, err := sqlerr.Load(corpusDir, engine)
	if err != nil {
		t.Fatalf("the corpus this test is built on will not load: %v", err)
	}
	cs, ok := c.Case("unique")
	if !ok || cs.Err == nil {
		t.Fatalf("%s has no captured unique violation to build a fault from", engine)
	}

	drv := &driverErr{
		Code:           cs.Err.SQLState,
		Message:        cs.Err.Message,
		Detail:         cs.Err.Fields["Detail"],
		ConstraintName: cs.Err.Fields["ConstraintName"],
		TableName:      cs.Err.Fields["TableName"],
		SchemaName:     cs.Err.Fields["SchemaName"],
	}
	detail := errs.Detail{
		Dialect:    engine,
		SQLState:   cs.Err.SQLState,
		Native:     int(cs.Err.Native),
		Constraint: drv.ConstraintName,
		Table:      drv.TableName,
		Value:      "anchor",
		Driver:     drv,
	}
	source := errs.Source{
		Table:      drv.TableName,
		Schema:     drv.SchemaName,
		Constraint: drv.ConstraintName,
	}

	// Everything the response must not name, taken from the capture rather
	// than invented. Empty fields are dropped: an engine that reports no
	// constraint name cannot prove anything about one.
	var secrets []string
	for _, s := range []string{
		cs.Err.SQLState, drv.ConstraintName, drv.TableName, drv.SchemaName,
		drv.Detail, cs.Err.Message, detail.Value,
	} {
		if s != "" {
			secrets = append(secrets, s)
		}
	}
	if cs.Err.Native != 0 {
		secrets = append(secrets, strconv.FormatUint(cs.Err.Native, 10))
	}
	if len(secrets) < 2 {
		t.Fatalf("%s contributed %d strings to hide, so finding them absent proves nothing", engine, len(secrets))
	}

	f := errs.Conflict().
		Op("Save").Entity("User").Code(errs.CodeUnique).
		Message("constraint " + drv.ConstraintName + " on " + drv.TableName).
		Detail(detail).
		Field("email").Code(errs.CodeUnique).Origin(errs.OriginState).
		Source(source).Message("this value is already taken").
		Fault()

	shim := shimFault{
		Kind:    f.Kind.String(),
		Code:    string(f.Code),
		Message: f.Message,
		Detail:  f.Detail,
		Violations: []shimViolation{{
			Path:    f.Violations[0].Path,
			Code:    string(f.Violations[0].Code),
			Origin:  f.Violations[0].Origin,
			Message: f.Violations[0].Message,
			Source:  source,
		}},
	}
	return f, secrets, shim
}

func TestAMarshalledFaultNamesNothingInternal(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql", "mariadb", "sqlite"} {
		t.Run(engine, func(t *testing.T) {
			f, secrets, shim := corpusFault(t, engine)

			// The control, and it is the load-bearing half: the same data
			// through the default marshal leaks, and does not error while
			// doing it.
			leaky, err := json.Marshal(shim)
			if err != nil {
				t.Fatalf("the default marshal failed, so the custom one is not what stops the leak: %v", err)
			}
			for _, s := range secrets {
				if !leaked(leaky, s) {
					t.Fatalf("the default marshal did not emit %q, so the assertions below prove nothing about what stops it: %s", s, leaky)
				}
			}

			for _, shape := range []struct {
				name string
				v    any
			}{
				{"the fault itself", f},
				{"a fault value", *f},
				{"a fault in a struct field", struct {
					Err errs.Fault `json:"err"`
				}{*f}},
			} {
				b, err := json.Marshal(shape.v)
				if err != nil {
					t.Fatalf("marshalling %s failed: %v", shape.name, err)
				}
				for _, s := range secrets {
					if leaked(b, s) {
						t.Fatalf("marshalling %s put %q on the wire: %s", shape.name, s, b)
					}
				}
				// The positive twin: "names nothing internal" is satisfied
				// perfectly by emitting {}.
				for _, want := range []string{`"kind":"conflict"`, `"error_code":"unique"`, `"violations":`, `"field":["email"]`} {
					if !strings.Contains(string(b), want) {
						t.Fatalf("marshalling %s produced %s, which does not carry %s", shape.name, b, want)
					}
				}
			}
		})
	}
}

func TestAViolationMarshalsOnlyFieldCodeAndMessage(t *testing.T) {
	v := errs.Violation{
		Path:        errs.Path{errs.Named("user"), errs.Named("email")},
		Code:        errs.CodeUnique,
		Origin:      errs.OriginState,
		Message:     "this value is already taken",
		Params:      errs.P{"max": 255, "min": 3, "column": "email", "table": "users", "a": 1, "b": 2, "c": 3, "d": 4},
		Source:      errs.Source{Table: "cp_parent", Schema: "public", Columns: []string{"slug"}, Constraint: "cp_parent_slug_key"},
		Approximate: true,
	}

	// The control: every field that must not be rendered is populated first,
	// with a token nothing else could produce. A fixture with them unset would
	// pass for a marshal that renders everything it is given.
	secrets := []string{"cp_parent", "public", "slug", "cp_parent_slug_key", "state", "255", "column", "true"}
	if v.Origin != errs.OriginState || !v.Approximate || len(v.Params) != 8 || v.Source.Constraint == "" {
		t.Fatalf("the fixture lost the fields this test exists to find absent")
	}

	for _, shape := range []struct {
		name string
		v    any
	}{
		{"a value", v},
		{"a pointer", &v},
		{"a slice element", []errs.Violation{v}},
		{"a map value", map[string]errs.Violation{"a": v}},
		{"a struct field", struct {
			V errs.Violation `json:"v"`
		}{v}},
	} {
		t.Run(shape.name, func(t *testing.T) {
			b, err := json.Marshal(shape.v)
			if err != nil {
				t.Fatalf("marshalling %s failed: %v", shape.name, err)
			}
			for _, s := range secrets {
				if leaked(b, s) {
					t.Fatalf("marshalling %s put %q on the wire: %s — with MarshalJSON on a pointer receiver three of these five shapes bypass it", shape.name, s, b)
				}
			}
			if !strings.Contains(string(b), `"field":["user","email"]`) ||
				!strings.Contains(string(b), `"error_code":"unique"`) ||
				!strings.Contains(string(b), `"message":"this value is already taken"`) {
				t.Fatalf("marshalling %s produced %s, which is missing one of the three public keys", shape.name, b)
			}
		})
	}

	t.Run("the key set is exactly the three", func(t *testing.T) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := keysOf(t, b), "error_code,field,message"; got != want {
			t.Fatalf("a violation marshalled to keys [%s], want [%s]", got, want)
		}
	})

	t.Run("an empty path omits the field", func(t *testing.T) {
		b, err := json.Marshal(errs.Violation{Code: errs.CodeInternal})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := keysOf(t, b), "error_code"; got != want {
			t.Fatalf("a violation with no path and no message marshalled to keys [%s], want [%s]", got, want)
		}
	})

	t.Run("a nil path is an empty array and never null", func(t *testing.T) {
		b, err := json.Marshal(errs.Path(nil))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "[]" {
			t.Fatalf("a nil path marshalled to %s — the envelope's field is an array a client may measure", b)
		}
	})

	t.Run("fifty marshals are byte-identical", func(t *testing.T) {
		first, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 50; i++ {
			again, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if string(again) != string(first) {
				t.Fatalf("run %d produced %s, run 0 produced %s", i, again, first)
			}
		}
	})
}

func keysOf(t *testing.T, b []byte) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("what was marshalled is not an object: %s: %v", b, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// leaked answers whether the marshalled bytes carry the string, escaped or
// not. A corpus message contains quotes, and json.Marshal escapes them, so a
// plain strings.Contains would report a real leak as absent.
func leaked(b []byte, secret string) bool {
	if strings.Contains(string(b), secret) {
		return true
	}
	enc, err := json.Marshal(secret)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), string(enc[1:len(enc)-1]))
}

// printReachable is what fmt's struct printer can actually put in a line, taken
// off the built fault rather than invented.
//
// The driver error's own Detail field is deliberately not in it. fmt renders an
// error-typed field through Error(), so a *pgconn.PgError's Detail never
// reaches a printed line the way it reaches a marshalled one — asserting its
// absence would be an assertion the shape makes true for free.
func printReachable(t *testing.T, f *errs.Fault) []string {
	t.Helper()

	out := []string{}
	for _, s := range []string{
		f.Detail.SQLState, f.Detail.Constraint, f.Detail.Table, f.Detail.Value,
		f.Detail.Driver.Error(), f.Violations[0].Source.Schema,
	} {
		if s != "" {
			out = append(out, s)
		}
	}
	if f.Detail.Native != 0 {
		out = append(out, strconv.Itoa(f.Detail.Native))
	}
	if len(out) < 2 {
		t.Fatalf("the fixture contributed %d strings to hide, so finding them absent proves nothing", len(out))
	}
	return out
}

// The third projection. Error() and MarshalJSON each have a leak test; the one
// a log line actually reaches — %v on a value, in a struct field, in a map
// entry — had none, and Fault.String could be deleted with the whole root
// module green.
func TestAPrintedFaultNamesNothingInternal(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql", "mariadb", "sqlite"} {
		t.Run(engine, func(t *testing.T) {
			f, _, shim := corpusFault(t, engine)
			secrets := printReachable(t, f)

			// The control, and it is the load-bearing half: the same fields
			// through fmt's struct printer emit every one of them. Without it
			// this test passes for a fixture with nothing to hide, and nothing
			// records that the struct printer is what Fault.String stops.
			for _, verb := range []string{"%v", "%+v"} {
				got := fmt.Sprintf(verb, shim)
				for _, secret := range secrets {
					if !strings.Contains(got, secret) {
						t.Fatalf("the default print (%s) did not emit %q, so the assertions below prove nothing about what stops it: %s", verb, secret, got)
					}
				}
			}

			// Every shape fmt reaches a fault through. The pointer goes to
			// Error(); the four value shapes fall through to the struct printer
			// unless Fault.String catches them.
			for _, shape := range []struct {
				name string
				got  string
			}{
				{"%v on the pointer", fmt.Sprintf("%v", f)},
				{"%+v on the pointer", fmt.Sprintf("%+v", f)},
				{"%v on a value", fmt.Sprintf("%v", *f)},
				{"%+v on a value", fmt.Sprintf("%+v", *f)},
				{"a value in a struct field", fmt.Sprintf("%+v", struct{ Err errs.Fault }{*f})},
				{"a value in a map", fmt.Sprintf("%+v", map[string]errs.Fault{"err": *f})},
			} {
				for _, secret := range secrets {
					if strings.Contains(shape.got, secret) {
						t.Fatalf("printing the fault as %s put %q into the line: %s", shape.name, secret, shape.got)
					}
				}
				// The positive twin: "names nothing internal" is satisfied
				// perfectly by printing the empty string.
				for _, want := range []string{"conflict", string(errs.CodeUnique), "1 violation"} {
					if !strings.Contains(shape.got, want) {
						t.Fatalf("printing the fault as %s produced %q, which does not say %q", shape.name, shape.got, want)
					}
				}
			}
		})
	}
}

// The violation half, and it is a written-out fixture rather than a corpus one
// for the same reason TestAViolationMarshalsOnlyFieldCodeAndMessage is: only
// PostgreSQL reports a constraint, a table and a schema as fields, so a
// corpus-driven Source would be empty on three engines of four and the test
// would prove nothing there.
func TestAPrintedViolationNamesNothingInternal(t *testing.T) {
	v := errs.Violation{
		Path:        errs.Path{errs.Named("user"), errs.Named("email")},
		Code:        errs.CodeUnique,
		Origin:      errs.OriginState,
		Message:     "this value is already taken",
		Params:      errs.P{"max": 255, "column": "email", "table": "users"},
		Source:      errs.Source{Table: "cp_parent", Schema: "public", Columns: []string{"slug"}, Constraint: "cp_parent_slug_key"},
		Approximate: true,
	}
	// The origin is in the list because fmt renders it through Origin.String,
	// as "state" — which is how it would reach a log line.
	secrets := []string{"cp_parent", "public", "slug", "cp_parent_slug_key", "state", "255", "column"}

	// The control: the same fields with no String method print in full. The
	// slice shape is the realistic one — a fault's violations are logged
	// together — and it is the one a method on a pointer receiver would miss.
	shim := shimViolation{
		Path: v.Path, Code: string(v.Code), Origin: v.Origin,
		Message: v.Message, Params: v.Params, Source: v.Source,
	}
	for _, verb := range []string{"%v", "%+v"} {
		got := fmt.Sprintf(verb, []shimViolation{shim})
		for _, secret := range secrets {
			if !strings.Contains(got, secret) {
				t.Fatalf("the default print (%s) of a violation slice did not emit %q, so the assertions below prove nothing about what stops it: %s", verb, secret, got)
			}
		}
	}

	for _, shape := range []struct {
		name string
		got  string
	}{
		{"%v on a violation", fmt.Sprintf("%v", v)},
		{"%+v on a violation", fmt.Sprintf("%+v", v)},
		{"a whole violation slice", fmt.Sprintf("%+v", []errs.Violation{v})},
		{"a violation in a struct field", fmt.Sprintf("%+v", struct{ V errs.Violation }{v})},
		{"a violation in a map", fmt.Sprintf("%+v", map[string]errs.Violation{"v": v})},
	} {
		for _, secret := range secrets {
			if strings.Contains(shape.got, secret) {
				t.Fatalf("printing %s put %q into the line: %s", shape.name, secret, shape.got)
			}
		}
		for _, want := range []string{"user.email", string(errs.CodeUnique), v.Message} {
			if !strings.Contains(shape.got, want) {
				t.Fatalf("printing %s produced %q, which does not say %q", shape.name, shape.got, want)
			}
		}
	}
}

func TestAFaultWithNoViolationsRendersAnEmptyArray(t *testing.T) {
	f := errs.NotFound().Op("Find").Entity("User").Code(errs.CodeNotFound).Fault()
	if len(f.Violations) != 0 {
		t.Fatalf("the fixture carries %d violations, so it cannot say what an empty list renders as", len(f.Violations))
	}

	// The control: the same field through the default path is null, which is
	// what a client iterating it breaks on. Every other marshal test here
	// builds a fault that carries a violation, so nothing else measures this.
	leaky, err := json.Marshal(struct {
		Violations []errs.Violation `json:"violations"`
	}{})
	if err != nil {
		t.Fatalf("the default marshal failed, so the custom one is not what stops the null: %v", err)
	}
	if !strings.Contains(string(leaky), `"violations":null`) {
		t.Fatalf("the default marshal produced %s, so the assertions below prove nothing", leaky)
	}

	for _, shape := range []struct {
		name string
		v    any
	}{
		{"the fault itself", f},
		{"a fault value", *f},
		{"a fault in a struct field", struct {
			Err errs.Fault `json:"err"`
		}{*f}},
	} {
		b, err := json.Marshal(shape.v)
		if err != nil {
			t.Fatalf("marshalling %s failed: %v", shape.name, err)
		}
		if !strings.Contains(string(b), `"violations":[]`) {
			t.Fatalf("marshalling %s produced %s — the envelope's field is an array a client iterates, and a 404 is where it is least likely to have been tried", shape.name, b)
		}
	}
}
