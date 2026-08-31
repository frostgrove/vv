package porthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
	"github.com/frostgrove/vv/port"
)

func render(t *testing.T, err error, options ...RenderOption) (int, []byte, Envelope) {
	t.Helper()
	return renderCtx(t, context.Background(), err, options...)
}

func renderCtx(t *testing.T, ctx context.Context, err error, options ...RenderOption) (int, []byte, Envelope) {
	t.Helper()
	status, _, body := NewRenderer(options...).Render(ctx, err)
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		t.Fatalf("the envelope does not marshal: %v", marshalErr)
	}
	env, _ := body.(Envelope)
	return status, raw, env
}

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

				if !strings.Contains(body, `"type":"error"`) {
					t.Fatalf("the body is not the envelope: %s", body)
				}
				if !strings.Contains(body, `"error_code"`) {
					t.Fatalf("the body carries no error_code, so a client cannot branch: %s", body)
				}
			})
		}
	}

	for _, engine := range []string{"postgres", "mysql", "mariadb", "sqlite"} {
		if walked[engine] == 0 {
			t.Fatalf("no %s case was walked, so this test measured nothing on that engine", engine)
		}
		if searched[engine] == 0 {
			t.Fatalf("no %s secret was searched for, so the assertions above were empty", engine)
		}
	}
}

func secretsOf(e *sqlerr.Err) map[string]string {
	out := map[string]string{}
	add := func(what, s string) {
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

func faultFromCorpus(t *testing.T, engine string, tc sqlerr.Case) error {
	t.Helper()
	code, source, ok := sqlerr.Classify(engine, tc.Err)
	if !ok {
		return fmt.Errorf("%s", tc.Err.Message)
	}
	b := errs.New(kindOfCode(code)).Code(code).
		General().Code(code).Origin(errs.OriginState).Source(source).
		Detail(errs.Detail{
			Dialect:    engine,
			SQLState:   tc.Err.SQLState,
			Native:     int(tc.Err.Native),
			Constraint: source.Constraint,
			Table:      source.Table,
			Columns:    source.Columns,
			Value:      tc.Err.Fields["Detail"],
			Driver:     fmt.Errorf("%s", tc.Err.Message),
		}).
		Params(errs.P{"constraint": source.Constraint, "table": source.Table}).
		Wrapping(fmt.Errorf("%s", tc.Err.Message))
	return b.Fault()
}

func kindOfCode(c errs.Code) errs.Kind {
	k, _ := errs.StandardCodes().KindOf(c)
	return k
}

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

	_, raw, env = render(t, f, WithMaxViolations(50))
	if len(env.Errors.Validation) != 10 || env.Partial {
		t.Fatalf("ten violations under a cap of fifty rendered %s", raw)
	}
}

type catalogue map[string]string

func (this catalogue) Message(_ context.Context, v errs.Violation, _ string) (string, bool) {
	m, ok := this[v.Path.String()+"."+string(v.Code)]
	return m, ok
}

func TestTheCatalogueReachesTheRenderedBody(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("email").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()

	_, raw, env := render(t, f, WithMessages(catalogue{"email.unique": "that address is taken"}))
	if got := env.Errors.Validation[0].Message; got != "that address is taken" {
		t.Fatalf("the catalogue did not reach the body: message = %q: %s", got, raw)
	}

	if _, _, env := render(t, f); env.Errors.Validation[0].Message != "this value is already taken" {
		t.Fatalf("without a catalogue the message is %q, want the code's default", env.Errors.Validation[0].Message)
	}
}

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

	_, _, guess := renderCtx(t, ctx, unique("Email"))
	if got := guess.Errors.Validation[0].Path.String(); got != "contact.email" {
		t.Fatalf("without a map the field is %q, want the body index's own answer", got)
	}

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

	elsewhere := WithBody(context.Background(), []byte(`{"other":{"email":"x@y.z"}}`))
	_, _, kept := renderCtx(t, elsewhere, unique("Email"), WithResolvers(declared))
	if got := kept.Errors.Validation[0].Path.String(); got != "contact.email" {
		t.Fatalf("a declared path was rewritten to %q by the fallback behind it", got)
	}

	_, _, grabbed := renderCtx(t, elsewhere, unique("Email"))
	if got := grabbed.Errors.Validation[0].Path.String(); got != "other.email" {
		t.Fatalf("the index answered %q for the omitted column, so the guard above proves nothing", got)
	}

	_, _, declined := renderCtx(t, ctx, unique("Nickname"), WithResolvers(declared))
	if got := declined.Errors.Validation[0].Path.String(); got != "Nickname" {
		t.Fatalf("an undeclared field resolved to %q; the declining hop was not honoured", got)
	}
	if !declined.Errors.Validation[0].Approximate {
		t.Fatal("an undeclared field was not marked approximate, so the decline was silently a guess")
	}

	_, _, fell := renderCtx(t, ctx, unique("Nickname"))
	if got := fell.Errors.Validation[0].Path.String(); got != "nickname" {
		t.Fatalf("the fallback answered %q for a field nothing declared, so the decline above proves nothing", got)
	}
}
