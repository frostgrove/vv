package port

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func pipeline(t *testing.T, err error, o ViolationOptions) []errs.Violation {
	t.Helper()
	return pipelineCtx(t, context.Background(), err, o)
}

func pipelineCtx(t *testing.T, ctx context.Context, err error, o ViolationOptions) []errs.Violation {
	t.Helper()
	return Violations(ctx, FaultOf(err), &o)
}

func bytesOf(t *testing.T, vs []errs.Violation) string {
	t.Helper()
	raw, err := json.Marshal(vs)
	if err != nil {
		t.Fatalf("a violation list does not marshal: %v", err)
	}
	return string(raw)
}

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

	want := bytesOf(t, pipeline(t, forwards.Fault(), ViolationOptions{}))
	got := bytesOf(t, pipeline(t, backwards.Fault(), ViolationOptions{}))
	if got != want {
		t.Fatalf("the same eight violations built in reverse rendered differently:\n forwards %s\n backwards %s", want, got)
	}

	f := forwards.Fault()
	for i := 0; i < 50; i++ {
		if again := bytesOf(t, pipeline(t, f, ViolationOptions{})); again != want {
			t.Fatalf("run %d differed:\n first %s\n then  %s", i, want, again)
		}
	}

	other := errs.Validation().Code(errs.CodeCheck).Field("zzz").Code(errs.CodeCheck).Fault()
	if differs := bytesOf(t, pipeline(t, other, ViolationOptions{})); differs == want {
		t.Fatal("a different set of violations rendered identically, so byte equality above measures nothing")
	}
}

func TestAtOnePathTheInputViolationComesFirst(t *testing.T) {
	f := errs.Validation().Code(errs.CodeCheck).
		Field("email").Code(errs.CodeUnique).Origin(errs.OriginState).
		Field("email").Code(errs.CodeInvalidFormat).Origin(errs.OriginInput).
		Fault()

	vs := pipeline(t, f, ViolationOptions{})
	if len(vs) != 2 {
		t.Fatalf("two violations at one path came out as %d", len(vs))
	}
	if vs[0].Code != errs.CodeInvalidFormat {
		t.Fatalf("the order is %v then %v, want the input one first", vs[0].Code, vs[1].Code)
	}
}

func TestACappedListKeepsTheFrontOfTheOrder(t *testing.T) {
	b := errs.Validation().Code(errs.CodeCheck)
	for i := 9; i >= 0; i-- {
		b = b.At(errs.Path{errs.Named("f" + strconv.Itoa(i))}).Code(errs.CodeCheck)
	}
	f := b.Fault()

	vs := pipeline(t, f, ViolationOptions{Max: 3})
	if len(vs) != 3 {
		t.Fatalf("ten violations capped at three came out as %d", len(vs))
	}
	for i, want := range []string{"f0", "f1", "f2"} {
		if got := vs[i].Path.String(); got != want {
			t.Fatalf("the cap kept %q at position %d, want %q — it cut before the sort", got, i, want)
		}
	}

	if vs := pipeline(t, f, ViolationOptions{Max: 50}); len(vs) != 10 {
		t.Fatalf("ten violations under a cap of fifty came out as %d", len(vs))
	}

	if vs := pipeline(t, f, ViolationOptions{}); len(vs) != 10 {
		t.Fatalf("ten violations with no cap came out as %d", len(vs))
	}
}

func TestAFaultWithNoViolationsStillNamesItsCode(t *testing.T) {
	vs := pipeline(t, errs.NotFound().Code(errs.CodeNotFound).Fault(), ViolationOptions{})
	if len(vs) != 1 || vs[0].Code != errs.CodeNotFound {
		t.Fatalf("a fault with no violations came out as %+v", vs)
	}

	vs = pipeline(t, errs.Forbidden().Fault(), ViolationOptions{})
	if len(vs) != 1 || vs[0].Code == "" {
		t.Fatalf("a fault with neither violations nor a code came out as %+v", vs)
	}
}

type catalogue map[string]string

func (this catalogue) Message(_ context.Context, v errs.Violation, _ string) (string, bool) {
	var last string
	for _, s := range v.Path {
		if !s.IsIndex {
			last = s.Name
		}
	}
	m, ok := this[last+"."+string(v.Code)]
	return m, ok
}

type renamer map[string]string

func (this renamer) Resolve(p errs.Path) (errs.Path, bool) {
	if len(p) != 1 || p[0].IsIndex {
		return p, true
	}
	to, ok := this[p[0].Name]
	if !ok {
		return p, false
	}
	return errs.Path{errs.Named(to)}, true
}

func TestTheMessageLadderSeesTheTranslatedPath(t *testing.T) {
	cat := catalogue{
		"email.unique": "that address is taken",
		"Email.unique": "the model field name won",
	}
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("Email").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()

	vs := pipeline(t, f, ViolationOptions{Messages: cat, Fallback: renamer{"Email": "email"}})
	if got := vs[0].Message; got != "that address is taken" {
		t.Fatalf("the message is %q; the ladder saw the untranslated path", got)
	}

	if _, ok := cat["Email.unique"]; !ok {
		t.Fatal("the pre-translation key is not in the catalogue, so it losing proves nothing")
	}
}

func TestAMessageFallsBackToTheCodesDefaultAndThenToTheCode(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).Field("email").Code(errs.CodeUnique).Fault()
	if got := pipeline(t, f, ViolationOptions{})[0].Message; got != "this value is already taken" {
		t.Fatalf("the message is %q, want the declared default", got)
	}

	novel := errs.Validation().Code("too_young").Field("age").Code("too_young").Fault()
	if got := pipeline(t, novel, ViolationOptions{})[0].Message; got != "too_young" {
		t.Fatalf("an undeclared code's message is %q, want the code itself", got)
	}
}

func TestADeclaredHopBeatsTheFallback(t *testing.T) {
	unique := func(field string) error {
		return errs.Conflict().Code(errs.CodeUnique).
			Field(field).Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	}
	declared := PathMap{"Email": At("contact", "email")}
	fallback := renamer{"Email": "guessed", "Nickname": "nickname"}

	vs := pipeline(t, unique("Email"), ViolationOptions{Resolvers: []errs.Resolver{declared}, Fallback: fallback})
	if got := vs[0].Path.String(); got != "contact.email" {
		t.Fatalf("field = %q, want the declared mapping", got)
	}
	if vs[0].Approximate {
		t.Fatal("a path the map declared was marked approximate")
	}

	if got := pipeline(t, unique("Email"), ViolationOptions{Fallback: fallback})[0].Path.String(); got != "guessed" {
		t.Fatalf("without a declared hop the field is %q, want the fallback's own answer", got)
	}

	declined := pipeline(t, unique("Nickname"), ViolationOptions{Resolvers: []errs.Resolver{declared}, Fallback: fallback})
	if got := declined[0].Path.String(); got != "Nickname" || !declined[0].Approximate {
		t.Fatalf("an undeclared field resolved to %q (approximate %v); the declining hop was not honoured",
			got, declined[0].Approximate)
	}
	if got := pipeline(t, unique("Nickname"), ViolationOptions{Fallback: fallback})[0].Path.String(); got != "nickname" {
		t.Fatalf("the fallback answered %q for that field, so the decline above proves nothing", got)
	}
}

func TestRenderingDoesNotWriteThroughToTheFault(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("Email").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()

	pipeline(t, f, ViolationOptions{Fallback: renamer{"Email": "email"}})

	if got := f.Violations[0].Path.String(); got != "Email" {
		t.Fatalf("the pipeline rewrote the fault's own path to %q", got)
	}
	if f.Violations[0].Message != "" {
		t.Fatalf("the pipeline wrote a message onto the fault: %q", f.Violations[0].Message)
	}
}
