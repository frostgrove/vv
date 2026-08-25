package crudhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/errs/sqlerr"
)

// render is what every test here measures: the bytes a client would read.
func render(t *testing.T, err error, opts ...RenderOption) (int, []byte, Envelope) {
	t.Helper()
	return renderCtx(t, context.Background(), err, opts...)
}

func renderCtx(t *testing.T, ctx context.Context, err error, opts ...RenderOption) (int, []byte, Envelope) {
	t.Helper()
	status, _, body := NewRenderer(opts...).Render(ctx, err)
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		t.Fatalf("the envelope does not marshal: %v", marshalErr)
	}
	env, _ := body.(Envelope)
	return status, raw, env
}

// ---------------------------------------------------------------------------
// the disclosure guard, over the whole corpus

// [[D-044]] on the wire, and the render test that decision has owed since phase
// 0: a body built from *every* corpus entry on all four engines names nothing
// internal.
//
// One hand-written violation would pass for a renderer that leaks a different
// field, which is exactly why the fixture is the corpus rather than an example.
func TestARenderedBodyNamesNothingInternal(t *testing.T) {
	walked := map[string]int{}
	searched := map[string]int{}

	for _, engine := range []string{"postgres", "mysql", "mariadb", "sqlite"} {
		c, err := sqlerr.Load("../../errs/sqlerr/testdata/corpus", engine)
		if err != nil {
			t.Fatalf("loading the %s corpus: %v", engine, err)
		}
		for _, tc := range c.Cases {
			if tc.Err == nil {
				continue
			}
			secrets := secretsOf(tc.Err)
			if len(secrets) == 0 {
				continue
			}
			t.Run(engine+"/"+tc.Name, func(t *testing.T) {
				walked[engine]++
				f := faultFromCorpus(t, engine, tc)

				_, raw, _ := render(t, f)
				body := string(raw)
				for what, secret := range secrets {
					searched[engine]++
					if strings.Contains(body, secret) {
						t.Fatalf("the body names %s (%q): %s", what, secret, body)
					}
				}
				// And it is not merely empty. A renderer emitting {} passes
				// "names nothing" perfectly, so the shape a client branches on
				// is asserted in the same breath.
				if !strings.Contains(body, `"type":"error"`) {
					t.Fatalf("the body is not the envelope: %s", body)
				}
				if !strings.Contains(body, `"error_code"`) {
					t.Fatalf("the body carries no error_code, so a client cannot branch: %s", body)
				}
			})
		}
	}

	// The controls. An emptied loop — a filter that later swallowed every case,
	// a corpus that stopped recording structured fields — is green and proves
	// nothing, so both counts are asserted per engine.
	for _, engine := range []string{"postgres", "mysql", "mariadb", "sqlite"} {
		if walked[engine] == 0 {
			t.Fatalf("no %s case was walked, so this test measured nothing on that engine", engine)
		}
		if searched[engine] == 0 {
			t.Fatalf("no %s secret was searched for, so the assertions above were empty", engine)
		}
	}
}

// secretsOf is everything one corpus entry knows that a response body must not
// carry. Each is asserted non-empty by construction: an empty needle is found
// in every haystack, so a blank field would make the search meaningless.
func secretsOf(e *sqlerr.Err) map[string]string {
	out := map[string]string{}
	add := func(what, s string) {
		// Two characters, because a one-character "secret" — a `t` from a
		// column name — is in every English sentence and would fail every
		// body including a correct one.
		if len(s) > 1 {
			out[what] = s
		}
	}
	add("the driver's message", e.Message)
	add("the SQLSTATE", e.SQLState)
	if e.Native != 0 {
		add("the engine's own number", strconv.FormatUint(e.Native, 10))
	}
	for name, v := range e.Fields {
		add("the driver's "+name, v)
	}
	return out
}

// faultFromCorpus replays one captured driver error through the classifier the
// adapters use, so what is rendered is what a caller would actually be handed.
func faultFromCorpus(t *testing.T, engine string, tc sqlerr.Case) error {
	t.Helper()
	code, src, ok := sqlerr.Classify(engine, tc.Err)
	if !ok {
		// An entry the parsers refuse still reaches a client, as the 500 that
		// says nothing. Rendering it is worth doing: the leak would be the
		// same one.
		return fmt.Errorf("%s", tc.Err.Message)
	}
	b := errs.New(kindOfCode(code)).Code(code).
		General().Code(code).Origin(errs.OriginState).Source(src).
		Detail(errs.Detail{
			Dialect:    engine,
			SQLState:   tc.Err.SQLState,
			Native:     int(tc.Err.Native),
			Constraint: src.Constraint,
			Table:      src.Table,
			Columns:    src.Columns,
			Value:      tc.Err.Fields["Detail"],
			Driver:     fmt.Errorf("%s", tc.Err.Message),
		}).
		// The two channels a renderer could copy into a body without ever
		// touching err.Error(), and the ones [[D-044]] owed an extension to.
		Params(errs.P{"constraint": src.Constraint, "table": src.Table}).
		Wrapping(fmt.Errorf("%s", tc.Err.Message))
	return b.Fault()
}

func kindOfCode(c errs.Code) errs.Kind {
	k, _ := errs.StandardCodes().KindOf(c)
	return k
}

// ---------------------------------------------------------------------------
// determinism

// [[D-014]] one layer up: the same failing request twice produces byte-identical
// output, so a response body can be asserted on at all.
//
// Eight violations spanning names, indices and equal-prefix paths, built in
// reverse. Two would pass by luck half the time.
func TestTheViolationOrderIsTotalAndByteIdentical(t *testing.T) {
	paths := []errs.Path{
		{errs.Named("email")},
		{errs.Named("items")},
		{errs.Named("items"), errs.Indexed(0)},
		{errs.Named("items"), errs.Indexed(2)},
		{errs.Named("items"), errs.Named("total")},
		{errs.Named("user"), errs.Named("email")},
		{errs.Named("user"), errs.Named("emailAddress")},
		{errs.Named("user"), errs.Named("name")},
	}

	// The control on the fixture itself. Without it the eight could be eight
	// copies of one path, and a sort that did nothing would render identically
	// every time and pass.
	seen := map[string]bool{}
	var equalPrefix, indexed bool
	for _, p := range paths {
		if seen[p.String()] {
			t.Fatalf("the fixture repeats %s, so the order below is not being exercised", p)
		}
		seen[p.String()] = true
		for _, s := range p {
			indexed = indexed || s.IsIndex
		}
	}
	for i := range paths {
		for j := range paths {
			if i != j && strings.HasPrefix(paths[j].String(), paths[i].String()) {
				equalPrefix = true
			}
		}
	}
	if !equalPrefix || !indexed {
		t.Fatalf("the fixture has no equal-prefix pair (%v) or no index step (%v); it is not the fixture this test needs", equalPrefix, indexed)
	}

	forwards := errs.Validation().Code(errs.CodeCheck)
	for _, p := range paths {
		forwards = forwards.At(p).Code(errs.CodeCheck)
	}
	backwards := errs.Validation().Code(errs.CodeCheck)
	for i := len(paths) - 1; i >= 0; i-- {
		backwards = backwards.At(paths[i]).Code(errs.CodeCheck)
	}

	_, want, _ := render(t, forwards.Fault())
	_, got, _ := render(t, backwards.Fault())
	if string(got) != string(want) {
		t.Fatalf("the same eight violations built in reverse rendered differently:\n forwards %s\n backwards %s", want, got)
	}

	// Fifty renders of one fault, byte for byte. A map iterated anywhere in the
	// pipeline shows up here and nowhere else.
	f := forwards.Fault()
	for i := 0; i < 50; i++ {
		_, again, _ := render(t, f)
		if string(again) != string(want) {
			t.Fatalf("render %d differed:\n first %s\n then  %s", i, want, again)
		}
	}

	// The control on the comparison: a deliberately different set has to come
	// out different, or the two bodies above agreeing means nothing.
	other := errs.Validation().Code(errs.CodeCheck).Field("zzz").Code(errs.CodeCheck).Fault()
	if _, differs, _ := render(t, other); string(differs) == string(want) {
		t.Fatal("a different set of violations rendered identically, so byte equality above measures nothing")
	}
}

// Within one path an input violation comes before a collision: a malformed
// value explains a failed lookup, and the reverse reads as nonsense. This is
// the half `ROADMAP-errors.md` §5 states and §8 omitted.
func TestAtOnePathTheInputViolationComesFirst(t *testing.T) {
	f := errs.Validation().Code(errs.CodeCheck).
		Field("email").Code(errs.CodeUnique).Origin(errs.OriginState).
		Field("email").Code(errs.CodeInvalidFormat).Origin(errs.OriginInput).
		Fault()

	_, _, env := render(t, f)
	if len(env.Errors.Validation) != 2 {
		t.Fatalf("two violations at one path rendered as %d", len(env.Errors.Validation))
	}
	if env.Errors.Validation[0].Code != errs.CodeInvalidFormat {
		t.Fatalf("the order is %v then %v, want the input one first",
			env.Errors.Validation[0].Code, env.Errors.Validation[1].Code)
	}
}

// ---------------------------------------------------------------------------
// the envelope's own shape

// The group describes what the client can act on, not where the failure came
// from — which is why a unique conflict appears under validation.
func TestAViolationWithAFieldIsValidationAndOneWithoutIsGeneral(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("email").Code(errs.CodeUnique).Origin(errs.OriginState).
		General().Code(errs.CodeConflict).
		Wrapping(nil).Fault()

	_, raw, env := render(t, f)
	if len(env.Errors.Validation) != 1 || len(env.Errors.General) != 1 {
		t.Fatalf("the groups are %+v, want one of each: %s", env.Errors, raw)
	}
	if !strings.Contains(string(raw), `"field":["email"]`) {
		t.Fatalf("the field is not the array a client reads: %s", raw)
	}
}

// A 404 carries no violation at all, and a status alone is not something a
// client can branch on.
func TestAFaultWithNoViolationsStillNamesItsCode(t *testing.T) {
	_, raw, env := render(t, errs.NotFound().Code(errs.CodeNotFound).Fault())
	if len(env.Errors.General) != 1 || env.Errors.General[0].Code != errs.CodeNotFound {
		t.Fatalf("a fault with no violations rendered %s", raw)
	}

	// The control: a fault with no code either still names something rather
	// than emitting an empty error_code.
	_, raw, env = render(t, errs.Forbidden().Fault())
	if len(env.Errors.General) != 1 || env.Errors.General[0].Code == "" {
		t.Fatalf("a fault with neither violations nor a code rendered %s", raw)
	}
}

// A response body is not a log. Past the cap the answer says so rather than
// listing N violations in a way that implies there were N.
func TestACappedListSaysItIsPartial(t *testing.T) {
	b := errs.Validation().Code(errs.CodeCheck)
	for i := 0; i < 10; i++ {
		b = b.At(errs.Path{errs.Named("f" + strconv.Itoa(i))}).Code(errs.CodeCheck)
	}
	f := b.Fault()

	_, raw, env := render(t, f, WithMaxViolations(3))
	if len(env.Errors.Validation) != 3 || !env.Partial {
		t.Fatalf("ten violations capped at three rendered %s", raw)
	}

	// The control: under the cap nothing is marked partial. A renderer that
	// always said partial would pass the leg above.
	_, raw, env = render(t, f, WithMaxViolations(50))
	if len(env.Errors.Validation) != 10 || env.Partial {
		t.Fatalf("ten violations under a cap of fifty rendered %s", raw)
	}
}

// ---------------------------------------------------------------------------
// the message ladder

// catalogue is a message source keyed the way errs.Messages is, so the test can
// say which key was consulted.
type catalogue map[string]string

func (c catalogue) Message(_ context.Context, v errs.Violation, _ string) (string, bool) {
	var last string
	for _, s := range v.Path {
		if !s.IsIndex {
			last = s.Name
		}
	}
	m, ok := c[last+"."+string(v.Code)]
	return m, ok
}

// Messages are expanded after the path is translated, because the ladder is
// derived from the path. Expanding first would key a catalogue entry on the
// model's field name on one deployment and on the client's on another, for the
// same violation.
func TestTheMessageLadderSeesTheTranslatedPath(t *testing.T) {
	cat := catalogue{
		"email.unique": "that address is taken",
		"Email.unique": "the model field name won",
	}
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("Email").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()

	ctx := WithBody(context.Background(), []byte(`{"user":{"email":"a@b.c"}}`))
	_, raw, env := renderCtx(t, ctx, f, WithMessages(cat))

	if got := env.Errors.Validation[0].Message; got != "that address is taken" {
		t.Fatalf("the message is %q; the ladder saw the untranslated path: %s", got, raw)
	}

	// The control: the pre-translation key is in the catalogue and must not
	// win. Without it the assertion above passes for a catalogue with one
	// entry.
	if _, ok := cat["Email.unique"]; !ok {
		t.Fatal("the pre-translation key is not in the catalogue, so it losing proves nothing")
	}
}

// No catalogue entry falls back to the code's declared default, and then to the
// code itself. Never to the driver's text — there is nowhere left for it to
// come from.
func TestAMessageFallsBackToTheCodesDefaultAndThenToTheCode(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).Field("email").Code(errs.CodeUnique).Fault()
	_, _, env := render(t, f)
	if got := env.Errors.Validation[0].Message; got != "this value is already taken" {
		t.Fatalf("the message is %q, want the declared default", got)
	}

	novel := errs.Validation().Code("too_young").Field("age").Code("too_young").Fault()
	_, _, env = render(t, novel)
	if got := env.Errors.Validation[0].Message; got != "too_young" {
		t.Fatalf("an undeclared code's message is %q, want the code itself", got)
	}
}

// A renderer holds no per-request state, so rendering the same fault twice must
// not change it. A resolved path or an expanded message written through to the
// fault would make the second render depend on the first ([[D-042]]).
func TestRenderingDoesNotWriteThroughToTheFault(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("Email").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()

	ctx := WithBody(context.Background(), []byte(`{"user":{"email":"a@b.c"}}`))
	renderCtx(t, ctx, f)

	if got := f.Violations[0].Path.String(); got != "Email" {
		t.Fatalf("rendering rewrote the fault's own path to %q", got)
	}
	if f.Violations[0].Message != "" {
		t.Fatalf("rendering wrote a message onto the fault: %q", f.Violations[0].Message)
	}
}
