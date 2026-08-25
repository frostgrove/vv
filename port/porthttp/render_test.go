package porthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/errs/sqlerr"
	"github.com/shardit-io/vv/port"
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
	m, ok := c[v.Path.String()+"."+string(v.Code)]
	return m, ok
}

// The ladder itself is port's and is measured there. What this pins is the
// wiring: WithMessages has to reach the pipeline, and a renderer that dropped
// the catalogue on the floor would still answer a well-formed body.
func TestTheCatalogueReachesTheRenderedBody(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("email").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()

	_, raw, env := render(t, f, WithMessages(catalogue{"email.unique": "that address is taken"}))
	if got := env.Errors.Validation[0].Message; got != "that address is taken" {
		t.Fatalf("the catalogue did not reach the body: message = %q: %s", got, raw)
	}

	// The control: with no catalogue the same violation carries the code's
	// declared default, so the assertion above measures the wiring rather than
	// a body that says that sentence whatever it is handed.
	if _, _, env := render(t, f); env.Errors.Validation[0].Message != "this value is already taken" {
		t.Fatalf("without a catalogue the message is %q, want the code's default", env.Errors.Validation[0].Message)
	}
}

// A declared mapping beats a guess, which is the whole reason a generated map
// is wired ahead of the raw-body fallback ([[D-043]], [[D-050]]).
//
// It is also the first hop in the tree that *declines*, so this is where
// WithResolvers-before-fallback is measured with one: a declining hop stops the
// chain and the fallback behind it never runs.
func TestADeclaredMapBeatsTheRawBodyGuess(t *testing.T) {
	body := []byte(`{"contact":{"email":"a@b.c"},"nickname":"ann"}`)
	ctx := WithBody(context.Background(), body)
	declared := port.PathMap{"Email": port.At("contact", "email")}

	unique := func(field string) error {
		return errs.Conflict().Code(errs.CodeUnique).
			Field(field).Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	}

	_, raw, env := renderCtx(t, ctx, unique("Email"), WithResolvers(declared))
	if got := env.Errors.Validation[0].Path.String(); got != "contact.email" {
		t.Fatalf("field = %q, want the declared mapping: %s", got, raw)
	}
	if env.Errors.Validation[0].Approximate {
		t.Fatal("a path the map declared was marked approximate")
	}

	// The control. With no map the same render falls to the body index, which
	// matches on the folded key name and answers the nested path it found — so
	// the assertion above measures the declared mapping rather than a renderer
	// that ignores WithResolvers entirely.
	_, _, guess := renderCtx(t, ctx, unique("Email"))
	if got := guess.Errors.Validation[0].Path.String(); got != "contact.email" {
		t.Fatalf("without a map the field is %q, want the body index's own answer", got)
	}

	// And the half that says which of the two ran: a body the index cannot
	// disambiguate. Two keys fold to the same name, so the fallback declines
	// and marks the path approximate, while the declared map still answers.
	twice := WithBody(context.Background(), []byte(`{"contact":{"email":"a@b.c"},"backup":{"email":"b@c.d"}}`))
	_, _, mapped := renderCtx(t, twice, unique("Email"), WithResolvers(declared))
	if got := mapped.Errors.Validation[0].Path.String(); got != "contact.email" || mapped.Errors.Validation[0].Approximate {
		t.Fatalf("with two candidates the declared map answered %q (approximate %v), want contact.email exactly",
			got, mapped.Errors.Validation[0].Approximate)
	}
	_, _, ambiguous := renderCtx(t, twice, unique("Email"))
	if !ambiguous.Errors.Validation[0].Approximate {
		t.Fatal("the body index resolved an ambiguous name, so the map answering it proves nothing")
	}

	// A declared path the client did not send. The index would otherwise fold
	// the last step and match a same-named key somewhere else in the payload,
	// which is a guess overturning a declaration — the case a NOT NULL
	// violation on an omitted column produces.
	elsewhere := WithBody(context.Background(), []byte(`{"other":{"email":"x@y.z"}}`))
	_, _, kept := renderCtx(t, elsewhere, unique("Email"), WithResolvers(declared))
	if got := kept.Errors.Validation[0].Path.String(); got != "contact.email" {
		t.Fatalf("a declared path was rewritten to %q by the fallback behind it", got)
	}
	// Its control: without the map, that same body is exactly what the index
	// does match — so the arm above measures the guard rather than an index
	// that had nothing to say.
	_, _, grabbed := renderCtx(t, elsewhere, unique("Email"))
	if got := grabbed.Errors.Validation[0].Path.String(); got != "other.email" {
		t.Fatalf("the index answered %q for the omitted column, so the guard above proves nothing", got)
	}

	// And the other half of being total: a field the map does not declare
	// declines, so the violation is marked approximate rather than taking the
	// fallback's guess. The body carries a matching key, so the fallback could
	// have answered — which is what makes the decline visible.
	_, _, declined := renderCtx(t, ctx, unique("Nickname"), WithResolvers(declared))
	if got := declined.Errors.Validation[0].Path.String(); got != "Nickname" {
		t.Fatalf("an undeclared field resolved to %q; the declining hop was not honoured", got)
	}
	if !declined.Errors.Validation[0].Approximate {
		t.Fatal("an undeclared field was not marked approximate, so the decline was silently a guess")
	}
	// Its own control: without the map that same field reaches the body index.
	_, _, fell := renderCtx(t, ctx, unique("Nickname"))
	if got := fell.Errors.Validation[0].Path.String(); got != "nickname" {
		t.Fatalf("the fallback answered %q for a field nothing declared, so the decline above proves nothing", got)
	}
}
