package crudgrpc

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
)

// render is what most tests here measure: the status a client would read.
func render(t *testing.T, err error, options ...RenderOption) *status.Status {
	t.Helper()
	return renderCtx(t, context.Background(), err, options...)
}

func renderCtx(t *testing.T, ctx context.Context, err error, options ...RenderOption) *status.Status {
	t.Helper()
	st := NewRenderer(options...).Render(ctx, err)
	if st == nil {
		t.Fatal("the renderer answered no status for an error")
	}
	return st
}

// fieldViolations reads the one BadRequest detail, or reports there was none.
func fieldViolations(t *testing.T, st *status.Status) []*errdetails.BadRequest_FieldViolation {
	t.Helper()
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			return br.GetFieldViolations()
		}
	}
	return nil
}

func errorInfo(t *testing.T, st *status.Status) *errdetails.ErrorInfo {
	t.Helper()
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	return nil
}

func retryInfo(t *testing.T, st *status.Status) *errdetails.RetryInfo {
	t.Helper()
	for _, d := range st.Details() {
		if ri, ok := d.(*errdetails.RetryInfo); ok {
			return ri
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// the code table

// Every kind answers the code it promises to, and the table is total.
func TestKindMapsToTheCodeItPromisesTo(t *testing.T) {
	want := map[errs.Kind]codes.Code{
		errs.KindInternal:     codes.Internal,
		errs.KindNotFound:     codes.NotFound,
		errs.KindUnauthorized: codes.Unauthenticated,
		errs.KindForbidden:    codes.PermissionDenied,
		errs.KindRetryable:    codes.Unavailable,
		errs.KindConflict:     codes.AlreadyExists,
		errs.KindValidation:   codes.InvalidArgument,
		errs.KindBadRequest:   codes.InvalidArgument,
		errs.KindTooLarge:     codes.ResourceExhausted,

		errs.KindMethodNotAllowed: codes.Unimplemented,
	}
	for k, c := range want {
		if got := CodeFor(k); got != c {
			t.Fatalf("kind %s answered %s, want %s", k, got, c)
		}
	}

	// The control on the table's size. errs.Kind declares ten, and an eleventh
	// added later would otherwise be silently mapped to Internal — a 500 for a
	// class somebody meant a client to act on. Kind.String is total the same
	// way, so a kind outside the table renders as "internal" and is how a new
	// one is spotted here.
	declared := 0
	for k := errs.Kind(0); k < errs.Kind(64); k++ {
		if k != errs.KindInternal && k.String() == "internal" {
			continue
		}
		declared++
		if _, listed := want[k]; !listed {
			t.Fatalf("errs declares the kind %s and this table has no row for it", k)
		}
	}
	if declared != len(want) {
		t.Fatalf("errs declares %d kinds and the table has %d rows", declared, len(want))
	}
}

// Code is the unwired path: an application answering its own calls gets the
// same codes without building a renderer.
func TestCodeIsTheSameAnswerWithoutARenderer(t *testing.T) {
	taken := errs.Conflict().Code(errs.CodeUnique).Fault()
	if got, want := Code(taken), NewRenderer().Code(taken); got != want {
		t.Fatalf("Code answered %s and the renderer answered %s", got, want)
	}
	if Code(nil) != codes.OK {
		t.Fatalf("no error answered %s, want OK", Code(nil))
	}
}

// ---------------------------------------------------------------------------
// what an internal failure says

// leaky is the fixture the disclosure tests share: everything a status must not
// carry, in every channel a renderer could reach for it.
func leaky(kind errs.Kind, code errs.Code) *errs.Fault {
	return errs.New(kind).Code(code).Op("Save").Entity("users").
		General().Code(code).Origin(errs.OriginState).
		Source(errs.Source{Table: "users", Columns: []string{"email"}, Constraint: "users_email_key"}).
		Detail(errs.Detail{
			Dialect: "postgres", SQLState: "23505", Native: 0,
			Constraint: "users_email_key", Table: "users", Columns: []string{"email"},
			Value:  "Key (email)=(a@b.c) already exists.",
			Driver: fmt.Errorf("pq: duplicate key value violates unique constraint \"users_email_key\""),
		}).
		Params(errs.P{"constraint": "users_email_key", "table": "users"}).
		Wrapping(fmt.Errorf("pq: duplicate key value violates unique constraint \"users_email_key\"")).
		Fault()
}

// An internal failure says the one word it is allowed to and carries nothing
// else. [[D-044]] on a fourth transport.
func TestAnInternalStatusSaysNothing(t *testing.T) {
	st := render(t, leaky(errs.KindInternal, errs.CodeInternal))
	if st.Code() != codes.Internal {
		t.Fatalf("an internal fault answered %s", st.Code())
	}
	if st.Message() != string(errs.CodeInternal) {
		t.Fatalf("the message is %q, want %q", st.Message(), errs.CodeInternal)
	}
	if len(st.Details()) != 0 {
		t.Fatalf("an internal status carries %d details, want none: %v", len(st.Details()), st.Details())
	}

	// The control: the same fixture classified as a conflict *does* carry a
	// BadRequest detail. Without it a renderer that never attached details at
	// all would pass the leg above.
	conflict := render(t, leaky(errs.KindConflict, errs.CodeUnique))
	if len(fieldViolations(t, conflict)) == 0 {
		t.Fatal("the same fixture as a conflict carries no field violation, so the silence above proves nothing")
	}
}

// The strongest single assertion in this package. status.New(code, err.Error())
// is the natural wrong answer and it ships the table name: a fault's own text
// is "errs: Save users: conflict: unique".
func TestAStatusMessageNamesNoEntityAndNoDriverText(t *testing.T) {
	f := leaky(errs.KindConflict, errs.CodeUnique)
	secrets := map[string]string{
		"the entity":          "users",
		"the constraint":      "users_email_key",
		"the SQLSTATE":        "23505",
		"the driver's text":   "duplicate key value",
		"the offending value": "a@b.c",
	}

	st := render(t, f)
	haystack := []string{st.Message()}
	for _, fv := range fieldViolations(t, st) {
		haystack = append(haystack, fv.GetField(), fv.GetDescription(), fv.GetReason(),
			fv.GetLocalizedMessage().GetMessage(), fv.GetLocalizedMessage().GetLocale())
	}
	if info := errorInfo(t, st); info != nil {
		haystack = append(haystack, info.GetReason(), info.GetDomain())
		for k, v := range info.GetMetadata() {
			haystack = append(haystack, k, v)
		}
	}
	for what, secret := range secrets {
		for _, s := range haystack {
			if strings.Contains(s, secret) {
				t.Fatalf("the status names %s (%q) in %q", what, secret, s)
			}
		}
	}

	// And it is not merely empty. A renderer answering a blank status passes
	// "names nothing" perfectly.
	if st.Message() == "" {
		t.Fatal("the status carries no message, so a client is told nothing at all")
	}
	if len(fieldViolations(t, st)) == 0 {
		t.Fatal("the status carries no field violation, so the search above had almost nothing to search")
	}

	// The control on the fixture: the obvious wrong implementation really does
	// leak. Without this the test would pass for a fault whose Error() happened
	// to be harmless.
	if !strings.Contains(f.Error(), "users") {
		t.Fatal("the fault's own Error() does not name the entity, so avoiding it proves nothing")
	}
}

// ---------------------------------------------------------------------------
// the details

// Every violation becomes one field violation, in the pipeline's order.
func TestEveryViolationBecomesAFieldViolationInTheSameOrder(t *testing.T) {
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
	build := func(order []errs.Path) error {
		b := errs.Validation().Code(errs.CodeCheck)
		for _, p := range order {
			b = b.At(p).Code(errs.CodeCheck)
		}
		return b.Fault()
	}
	fields := func(st *status.Status) []string {
		var out []string
		for _, fv := range fieldViolations(t, st) {
			out = append(out, fv.GetField())
		}
		return out
	}

	forwards := fields(render(t, build(paths)))
	if len(forwards) != len(paths) {
		t.Fatalf("%d violations became %d field violations", len(paths), len(forwards))
	}

	// The control: built in reverse, the rendered list is identical. Eight and
	// not two, because two would agree by luck half the time ([[D-014]]).
	reversed := make([]errs.Path, len(paths))
	for i := range paths {
		reversed[i] = paths[len(paths)-1-i]
	}
	backwards := fields(render(t, build(reversed)))
	if strings.Join(forwards, "|") != strings.Join(backwards, "|") {
		t.Fatalf("the same eight violations built in reverse rendered differently:\n forwards %v\n backwards %v", forwards, backwards)
	}

	// And the fixture control: a different set has to come out different, or
	// equality above measures nothing.
	other := fields(render(t, errs.Validation().Code(errs.CodeCheck).Field("zzz").Code(errs.CodeCheck).Fault()))
	if strings.Join(other, "|") == strings.Join(forwards, "|") {
		t.Fatal("a different set of violations rendered identically")
	}
}

// gRPC has no counterpart of the envelope's `general` group, and
// `ROADMAP-errors.md` §16 settled that the two origins are never split into two
// lists. A violation that names no field is therefore a field violation with an
// empty Field — lossless, and in the one list.
func TestAViolationWithNoPathIsStillInTheOneList(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("email").Code(errs.CodeUnique).Origin(errs.OriginState).
		General().Code(errs.CodeConflict).
		Fault()

	fvs := fieldViolations(t, render(t, f))
	if len(fvs) != 2 {
		t.Fatalf("a pathed and a pathless violation became %d field violations, want both", len(fvs))
	}
	// The pathless one comes first: a shorter path sorts before a longer one,
	// which is the pipeline's order and not this package's choice.
	if fvs[0].GetField() != "" {
		t.Fatalf("the pathless violation names %q, want an empty field", fvs[0].GetField())
	}
	if fvs[0].GetReason() != string(errs.CodeConflict) {
		t.Fatalf("the pathless violation's reason is %q, want the code it carried", fvs[0].GetReason())
	}
	// The control on the pair: the pathed one is unaffected. A renderer that
	// dropped every path would answer two empty fields and pass a test that
	// only counted them.
	if fvs[1].GetField() != "email" {
		t.Fatalf("the pathed violation names %q, want email", fvs[1].GetField())
	}
}

// The code keeps the spelling it has everywhere else. AIP asks for
// UPPER_SNAKE_CASE in Reason; a code spelled two ways is not a stable machine
// code, which is what §11 asked for ([[D-052]]).
func TestTheReasonIsTheCodeSpelledTheUsualWay(t *testing.T) {
	f := errs.Conflict().Code(errs.CodeUnique).
		Field("email").Code(errs.CodeUnique).Origin(errs.OriginState).Fault()
	if got := fieldViolations(t, render(t, f))[0].GetReason(); got != "unique" {
		t.Fatalf("the reason is %q, want the literal code %q", got, "unique")
	}
}

// A retryable failure says so, and says how long to wait. The framework itself
// does not retry ([[D-040]]).
func TestARetryableStatusCarriesRetryInfo(t *testing.T) {
	f := errs.Retryable().Code(errs.CodeDeadlock).Fault()
	st := render(t, f)
	if st.Code() != codes.Unavailable {
		t.Fatalf("a retryable fault answered %s", st.Code())
	}
	ri := retryInfo(t, st)
	if ri == nil {
		t.Fatal("an Unavailable status carries no RetryInfo")
	}
	if got := ri.GetRetryDelay().AsDuration(); got != DefaultRetryDelay {
		t.Fatalf("the retry delay is %s, want %s", got, DefaultRetryDelay)
	}
	if got := retryInfo(t, render(t, f, WithRetryDelay(5*time.Second))).GetRetryDelay().AsDuration(); got != 5*time.Second {
		t.Fatalf("WithRetryDelay(5s) advertised %s", got)
	}

	// The control: a conflict carries none, so the hint is about this class
	// rather than something every status gets.
	if retryInfo(t, render(t, errs.Conflict().Code(errs.CodeUnique).Fault())) != nil {
		t.Fatal("a conflict carries a RetryInfo, so the assertion above says nothing about retryable")
	}
}

// A retryable answer is advice and never an attempt: the repository is called
// once, whatever the hint says.
func TestTheFrameworkDoesNotRetryOnTheCallersBehalf(t *testing.T) {
	c, f := mount(t)
	f.err = errs.Retryable().Code(errs.CodeDeadlock).Fault()

	if st := c.fails("Count", doc(t, `{}`)); st.Code() != codes.Unavailable {
		t.Fatalf("a deadlock answered %s", st.Code())
	}
	if calls := len(f.calls); calls != 1 {
		t.Fatalf("the repository was called %d times for one request; the framework retried", calls)
	}
}

// Past the cap the status says the list is incomplete, and `partial` is the
// only thing the metadata ever carries — anything else is both a determinism
// hazard in a proto map and the obvious place an internal name would end up.
func TestPartialIsTheOnlyMetadataKey(t *testing.T) {
	b := errs.Validation().Code(errs.CodeCheck)
	for i := 0; i < 10; i++ {
		b = b.At(errs.Path{errs.Named("f" + strconv.Itoa(i))}).Code(errs.CodeCheck)
	}
	f := b.Fault()

	capped := render(t, f, WithMaxViolations(3))
	if got := len(fieldViolations(t, capped)); got != 3 {
		t.Fatalf("ten violations capped at three rendered %d", got)
	}
	info := errorInfo(t, capped)
	if info == nil {
		t.Fatal("a capped status carries no ErrorInfo, so nothing says the list is short")
	}
	if len(info.GetMetadata()) != 1 || info.GetMetadata()[PartialKey] != "true" {
		t.Fatalf("the metadata is %v, want exactly {%s: true}", info.GetMetadata(), PartialKey)
	}

	// The control: under the cap nothing is marked partial. A renderer that
	// always said partial would pass the leg above.
	under := errorInfo(t, render(t, f, WithMaxViolations(50)))
	if len(under.GetMetadata()) != 0 {
		t.Fatalf("ten violations under a cap of fifty carry the metadata %v", under.GetMetadata())
	}
	if got := len(fieldViolations(t, render(t, f, WithMaxViolations(50)))); got != 10 {
		t.Fatalf("under the cap %d of ten violations were rendered", got)
	}
}

// The reason on the ErrorInfo is the fault's own code — the one thing a client
// branches on when the violations are about fields it does not know.
func TestTheErrorInfoNamesTheFaultsOwnCode(t *testing.T) {
	info := errorInfo(t, render(t, errs.NotFound().Fault()))
	if info == nil {
		t.Fatal("a not-found status carries no ErrorInfo")
	}
	if info.GetReason() != string(errs.CodeNotFound) {
		t.Fatalf("a fault with no code of its own reported the reason %q, want %q", info.GetReason(), errs.CodeNotFound)
	}
	if info.GetDomain() != ErrorDomain {
		t.Fatalf("the domain is %q, want %q", info.GetDomain(), ErrorDomain)
	}
}

// statusOf reads the status off an error a renderer produced, insisting there
// is one — an error with no status is the failure a renderer exists to prevent.
func statusOf(t *testing.T, err error) *status.Status {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("the error carries no status: %v", err)
	}
	return st
}

// ---------------------------------------------------------------------------
// the disclosure guard, over the whole corpus

// [[D-044]] on the wire of a fourth transport: a status built from *every*
// corpus entry on all four engines names nothing internal.
//
// One hand-written violation would pass for a renderer that leaks a different
// field, which is exactly why the fixture is the corpus rather than an example.
// It is the same fixture crudhttp renders bodies from, so the two transports
// are held to one standard rather than to two that can drift.
func TestAClassifiedConflictReachesAGrpcClientWithNothingInternal(t *testing.T) {
	walked := map[string]int{}
	searched := map[string]int{}

	for _, engine := range []string{"postgres", "mysql", "mariadb", "sqlite"} {
		c, err := sqlerr.Load("../../../errs/sqlerr/testdata/corpus", engine)
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
				st := render(t, faultFromCorpus(t, engine, tc))

				said := []string{st.Message()}
				for _, fv := range fieldViolations(t, st) {
					said = append(said, fv.GetField(), fv.GetDescription(), fv.GetReason(),
						fv.GetLocalizedMessage().GetMessage())
				}
				if info := errorInfo(t, st); info != nil {
					said = append(said, info.GetReason(), info.GetDomain())
					for k, v := range info.GetMetadata() {
						said = append(said, k, v)
					}
				}
				for what, secret := range secrets {
					searched[engine]++
					for _, s := range said {
						if strings.Contains(s, secret) {
							t.Fatalf("the status names %s (%q) in %q", what, secret, s)
						}
					}
				}
				// And it is not merely empty. A renderer answering a blank
				// status passes "names nothing" perfectly, so what a client
				// branches on is asserted in the same breath.
				if st.Message() == "" {
					t.Fatalf("the status carries no message: %v", st)
				}
				if st.Code() != codes.Internal && errorInfo(t, st).GetReason() == "" {
					t.Fatalf("the status carries no reason, so a client cannot branch: %v", st.Details())
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

// secretsOf is everything one corpus entry knows that a status must not carry.
// Each is asserted non-empty by construction: an empty needle is found in every
// haystack, so a blank field would make the search meaningless.
func secretsOf(e *sqlerr.Err) map[string]string {
	out := map[string]string{}
	add := func(what, s string) {
		// Two characters, because a one-character "secret" — a `t` from a
		// column name — is in every English sentence and would fail every
		// status including a correct one.
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
	code, source, ok := sqlerr.Classify(engine, tc.Err)
	if !ok {
		// An entry the parsers refuse still reaches a client, as the Internal
		// status that says nothing. Rendering it is worth doing: the leak would
		// be the same one.
		return fmt.Errorf("%s", tc.Err.Message)
	}
	kind, _ := errs.StandardCodes().KindOf(code)
	return errs.New(kind).Code(code).Op("Save").Entity(source.Table).
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
		// The two channels a renderer could copy into a status without ever
		// touching err.Error(), and the ones [[D-044]] owed an extension to.
		Params(errs.P{"constraint": source.Constraint, "table": source.Table}).
		Wrapping(fmt.Errorf("%s", tc.Err.Message)).
		Fault()
}
