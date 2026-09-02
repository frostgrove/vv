package probe

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/errs"
)

func insert() map[string]any {
	return map[string]any{
		"tenant_id": int64(1), "slug": "s", "email": "a@x.io",
		"org_id": int64(7), "region_id": int64(3), "zone": "eu", "code": "C1",
	}
}

func nulled(m map[string]any, cols ...string) map[string]any {
	for _, c := range cols {
		m[c] = nil
	}
	return m
}

const fullInsertTerms = 5

func TestAProbeThatFindsNothingKeepsTheDriversViolation(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false, false))
	f := declared(t, fixture())

	got, err := f.Enrich(ctx, request(conflict("docs_email_uk", "email"), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if len(got.Violations) != 1 {
		t.Fatalf("a probe that found nothing changed the answer: %v", codesAt(got))
	}
	if got.Partial {
		t.Fatal("a complete probe that found nothing marked the answer incomplete")
	}
}

func TestAProbeThatFindsSomethingAddsIt(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, true, false))
	f := declared(t, fixture())

	got, err := f.Enrich(ctx, request(conflict("docs_email_uk", "email"), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if !has(codesAt(got), "foreign_key@OrgID") {
		t.Fatalf("the probe found a missing parent row and it is not in the answer: %v", codesAt(got))
	}
}

func TestAProbeThatErrorsKeepsTheDriversViolationAndSaysItIsPartial(t *testing.T) {
	boom := errors.New("the connection went away")
	rec := crudtest.Postgres()
	rec.Push(crudtest.Result{Err: boom})
	f := declared(t, fixture())

	got, err := f.Enrich(ctx, request(conflict("docs_email_uk", "email"), rec, docMeta(t), row(insert())))
	if !errors.Is(err, boom) {
		t.Fatalf("the probe swallowed its own failure: %v", err)
	}
	if got == nil || len(got.Violations) != 1 {
		t.Fatalf("a failed probe lost the driver's violation: %+v", got)
	}
	if !got.Partial {
		t.Fatal("a failed probe presented an incomplete answer as complete")
	}
}

func TestAProbeThatSucceedsSetsNoPartial(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(true, false, false, false, false))
	f := declared(t, fixture())

	got, err := f.Enrich(ctx, request(conflict("docs_email_uk", "email"), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if got.Partial {
		t.Fatal("a probe that ran to completion marked the answer incomplete")
	}
}

func TestAForeignKeyTermIsNotBuiltForANullValue(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false))
	f := declared(t, fixture())
	_, err := f.Enrich(ctx, request(conflict("docs_email_uk", "email"), rec, docMeta(t),
		row(nulled(insert(), "org_id"))))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if contains(lastSQL(rec), `"orgs"`) {
		t.Fatalf("a NULL foreign key was probed anyway, and a bare NOT EXISTS over NULL is true: %s", lastSQL(rec))
	}
}

func TestAForeignKeyTermIsBuiltForAValue(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false, false))
	f := declared(t, fixture())
	if _, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert()))); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if !contains(lastSQL(rec), `"orgs"`) {
		t.Fatalf("a foreign key with a value was not probed: %s", lastSQL(rec))
	}
}

func TestACompositeForeignKeyIsSkippedWhenAnyColumnIsNull(t *testing.T) {
	for _, half := range []string{"region_id", "zone"} {
		t.Run(half, func(t *testing.T) {
			rec := crudtest.Postgres()
			rec.Push(answer(false, false, false, false))
			f := declared(t, fixture())
			if _, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t),
				row(nulled(insert(), half)))); err != nil {
				t.Fatalf("the probe reported %v", err)
			}
			if contains(lastSQL(rec), `"regions"`) {
				t.Fatalf("one NULL column disables the whole constraint, and it was probed anyway: %s", lastSQL(rec))
			}
		})
	}
}

func TestAUniqueTermIsSkippedWhenAnyKeyPartIsNull(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false))
	f := declared(t, fixture())
	if _, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t),
		row(nulled(insert(), "slug")))); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if contains(lastSQL(rec), `"tenant_id"`) {
		t.Fatalf("a composite unique key with a NULL half was probed, and NULLS DISTINCT means it matches nothing: %s", lastSQL(rec))
	}
}

func TestAnUnreproducibleConstraintIsNeverProbed(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false, false))
	f := declared(t, fixture())
	if _, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert()))); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	plan := f.planFor(request(conflict("", ""), rec, docMeta(t), row(insert())))
	for _, name := range []string{"docs_slug_partial", "docs_email_prefix", "docs_lower_email", "docs_zone_def_uk"} {
		for _, tm := range plan.terms {
			if tm.cand.name == name {
				t.Errorf("%s cannot be replayed from a value and was planned anyway", name)
			}
		}
	}

	planned := map[string]bool{}
	for _, tm := range plan.terms {
		planned[tm.cand.name] = true
	}
	for _, name := range []string{"docs_email_uk", "docs_tenant_slug_uk"} {
		if !planned[name] {
			t.Errorf("%s is a plain key over the same columns and it was not planned either, "+
				"so the skipping above says nothing", name)
		}
	}
}

func TestResultsAreReadByPositionAndNotByAlias(t *testing.T) {
	long := strings.Repeat("x", 61)
	cat := fixture()
	docs, _ := cat.Table("docs")
	docs.Constraints = append(docs.Constraints,
		newUnique(long+"_a", "email"), newUnique(long+"_b", "code"))

	rec := crudtest.Postgres()

	rec.Push(answer(false, false, false, false, false, false, true))
	f := declared(t, cat)
	got, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@Code")
	if v := got.Violations[0]; v.Source.Constraint != long+"_b" {
		t.Fatalf("the answer was attributed to %q: two names sharing 61 characters were read as one",
			v.Source.Constraint)
	}
}

func TestResultsAreReadByPositionWithShortNamesToo(t *testing.T) {
	cat := fixture()
	docs, _ := cat.Table("docs")
	docs.Constraints = append(docs.Constraints, newUnique("a_uk", "email"), newUnique("b_uk", "code"))

	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false, false, false, true))
	f := declared(t, cat)
	got, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if got.Violations[0].Source.Constraint != "b_uk" {
		t.Fatalf("even with short names the last term was read as %q", got.Violations[0].Source.Constraint)
	}
}

func TestThePlaceholdersAreTheDialectsOwn(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  *crudtest.Recorder
		want string
	}{
		{"postgres", crudtest.Postgres(), "$1"},
		{"mysql", crudtest.MySQL(), "?"},
		{"sqlite", crudtest.New(crud.SQLite{}), "?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.rec.Push(answer(false, false, false, false, false))
			f := declared(t, fixture())
			if _, err := f.Enrich(ctx, request(conflict("", ""), tc.rec, docMeta(t), row(insert()))); err != nil {
				t.Fatalf("the probe reported %v", err)
			}
			if !contains(lastSQL(tc.rec), tc.want) {
				t.Fatalf("%s got no %s in %s", tc.name, tc.want, lastSQL(tc.rec))
			}
		})
	}
}

func TestAnUpsertSkipsTheConflictsItsOwnTargetSwallows(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rec        *crudtest.Recorder
		wantsEmail bool
		cells      int
	}{
		{"postgres", crudtest.Postgres(), true, 5},

		{"mysql", crudtest.MySQL(), true, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cells := make([]any, tc.cells)
			for i := range cells {
				cells[i] = false
			}
			tc.rec.Push(crudtest.Rows(cells))
			f := declared(t, fixture())
			request := request(conflict("", ""), tc.rec, docMeta(t), idRow(int64(5), insert()))
			request.Upsert = true
			if _, err := f.Enrich(ctx, request); err != nil {
				t.Fatalf("the probe reported %v", err)
			}
			got := contains(lastSQL(tc.rec), tc.rec.Dialect().Quote("email"))
			if got != tc.wantsEmail {
				t.Fatalf("%s probed email = %v, want %v: a statement only absorbs the conflict it targets, and a dialect that cannot target the key gets no statement to absorb anything: %s",
					tc.name, got, tc.wantsEmail, lastSQL(tc.rec))
			}
		})
	}
}

func TestAKeylessSaveProbesEveryKeyOnEveryEngine(t *testing.T) {
	for _, rec := range []*crudtest.Recorder{crudtest.Postgres(), crudtest.MySQL()} {
		rec.Push(answer(false, false, false, false, false))
		f := declared(t, fixture())
		if _, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert()))); err != nil {
			t.Fatalf("the probe reported %v", err)
		}
		if !contains(lastSQL(rec), rec.Dialect().Quote("email")) {
			t.Fatalf("%s: a plain INSERT swallows nothing and email was not probed: %s",
				rec.Dialect().Name(), lastSQL(rec))
		}
	}
}

func TestOnlyConstraintsTheWriteCouldHaveBrokenAreProbed(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false))
	f := declared(t, fixture())

	request := request(conflict("", ""), rec, docMeta(t), idRow(int64(9), map[string]any{"email": "b@x.io"}))
	request.Op, request.Stored = "Update", true
	if _, err := f.Enrich(ctx, request); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	q := lastSQL(rec)
	if !contains(q, `"email"`) {
		t.Fatalf("the column the update wrote was not probed: %s", q)
	}
	if contains(q, `"orgs"`) || contains(q, `"notes"`) {
		t.Fatalf("a constraint over columns the update never touched was probed: %s", q)
	}
}

func TestAnUpdateExcludesItsOwnRow(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false))
	f := declared(t, fixture())
	request := request(conflict("", ""), rec, docMeta(t), idRow(int64(9), map[string]any{"email": "b@x.io"}))
	request.Op, request.Stored = "Update", true
	if _, err := f.Enrich(ctx, request); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if !contains(lastSQL(rec), `<>`) {
		t.Fatalf("an update did not exclude its own row, so a key it did not change collides with itself: %s", lastSQL(rec))
	}
}

func TestAnInsertExcludesNothing(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false, false))
	f := declared(t, fixture())
	if _, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert()))); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if contains(lastSQL(rec), `<>`) {
		t.Fatalf("an insert excluded a row that does not exist yet: %s", lastSQL(rec))
	}
}

func TestPastTheConstraintCapTheAnswerIsPartial(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false))
	f := declared(t, fixture(), WithMaxConstraints(2))
	got, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if !got.Partial {
		t.Fatal("a capped probe presented an incomplete answer as complete")
	}
}

func TestUnderTheConstraintCapTheAnswerIsComplete(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false, false))
	f := declared(t, fixture(), WithMaxConstraints(fullInsertTerms))
	got, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if got.Partial {
		t.Fatal("a probe that fitted inside its cap said it was incomplete")
	}
}

func TestPastTheRowCapTheAnswerIsPartial(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(flatAnswer(5, 1))
	f := declared(t, fixture(), WithMaxRows(1))
	request := request(conflict("", ""), rec, docMeta(t), row(insert()), row(insert()))
	request.Batch = true
	got, err := f.Enrich(ctx, request)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if !got.Partial {
		t.Fatal("a batch cut off at the row cap said nothing about it")
	}
}

func TestUnderTheRowCapTheAnswerIsComplete(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(flatAnswer(5, 2))
	f := declared(t, fixture(), WithMaxRows(2))
	request := request(conflict("", ""), rec, docMeta(t), row(insert()), row(insert()))
	request.Batch = true
	got, err := f.Enrich(ctx, request)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if got.Partial {
		t.Fatal("a batch that fitted inside its cap said it was incomplete")
	}
}

func TestTheProbeStopsAtItsOwnTimeout(t *testing.T) {
	rec := &slowRecorder{Recorder: crudtest.Postgres(), delay: 100 * time.Millisecond}
	rec.Push(answer(false, false, false, false, false))
	f := declared(t, fixture(), WithTimeout(time.Millisecond))
	got, err := f.Enrich(ctx, request(conflict("docs_email_uk", "email"), rec, docMeta(t), row(insert())))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the probe waited past its own timeout: %v", err)
	}
	if got == nil || len(got.Violations) != 1 || !got.Partial {
		t.Fatalf("a timed-out probe did not keep the driver's violation and mark the answer partial: %+v", got)
	}
}

func TestUnderItsTimeoutTheProbeAnswers(t *testing.T) {
	rec := &slowRecorder{Recorder: crudtest.Postgres(), delay: time.Millisecond}
	rec.Push(answer(true, false, false, false, false))
	f := declared(t, fixture(), WithTimeout(2*time.Second))
	got, err := f.Enrich(ctx, request(conflict("docs_email_uk", "email"), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if got.Partial {
		t.Fatal("a probe that finished in time said it was incomplete")
	}
}

type slowRecorder struct {
	*crudtest.Recorder
	delay time.Duration
}

func (this *slowRecorder) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(this.delay):
	}
	return this.Recorder.Query(ctx, q, args...)
}

func TestAConstraintOptedOutIsNeverProbed(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false))
	f := declared(t, fixture(), Skip("docs_email_uk"))
	if _, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert()))); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if contains(lastSQL(rec), `"email"`) {
		t.Fatalf("a constraint the caller opted out of was probed anyway: %s", lastSQL(rec))
	}

	if !contains(lastSQL(rec), `"code"`) {
		t.Fatalf("opting out of one constraint took the others with it: %s", lastSQL(rec))
	}
}

func TestCodeOnlyModeDropsThePathAndKeepsTheCode(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(true, false, false, false, false))
	f := declared(t, fixture(), CodeOnly())
	got, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@")
	if got.Violations[0].Code != errs.CodeUnique {
		t.Fatalf("code-only mode dropped the code as well: %v", got.Violations[0])
	}
}

func TestTheDefaultModeNamesTheField(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(true, false, false, false, false))
	f := declared(t, fixture())
	got, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@Email")
}

func TestTheValueReachesTheAnswerOnlyWhenAsked(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []Option
		want    any
	}{
		{"default", nil, nil},
		{"with values", []Option{WithValues()}, "a@x.io"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres()
			rec.Push(answer(true, false, false, false, false))
			f := declared(t, fixture(), tc.options...)
			got, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert())))
			if err != nil {
				t.Fatalf("the probe reported %v", err)
			}
			v := got.Violations[0]
			if tc.want == nil {
				if v.Params["value"] != nil {
					t.Fatalf("the offending value reached the answer by default: %v", v.Params)
				}
				return
			}
			if v.Params["value"] != tc.want {
				t.Fatalf("value = %v, want %v", v.Params["value"], tc.want)
			}
		})
	}
}

func TestTheScopePredicateNarrowsAUniqueTermAndNotAForeignKeyOne(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false, false))
	f := declared(t, fixture(), WithScope(func(context.Context) (crud.Predicate, error) {
		return crud.Eq("TenantID", int64(42)), nil
	}))
	if _, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert()))); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	q := lastSQL(rec)
	i := strings.Index(q, `"orgs"`)
	if i < 0 {
		t.Fatalf("the foreign-key term is missing altogether: %s", q)
	}
	if !contains(q[:i], aliasThis+`."tenant_id"`) {
		t.Fatalf("the scope did not narrow the unique terms: %s", q)
	}

	if contains(q[i:], `"tenant_id"`) {
		t.Fatalf("the scope was pushed into a subquery over another table: %s", q)
	}
}

func TestWithoutAScopeTheUniqueTermIsUnnarrowed(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false, false))
	f := declared(t, fixture())
	if _, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert()))); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	q := lastSQL(rec)
	if contains(q, aliasThis+`."tenant_id" = $`) && !contains(q, aliasThis+`."slug"`) {
		t.Fatalf("a narrowing turned up with no policy asking for one: %s", q)
	}
}

func TestAnInboundRestrictIsProbedAndACascadeIsNot(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false))
	f := declared(t, fixture())
	request := request(conflict("", ""), rec, docMeta(t), idRow(int64(9), map[string]any{"code": "C2"}))
	request.Op, request.Stored = "Update", true
	if _, err := f.Enrich(ctx, request); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	q := lastSQL(rec)
	if !contains(q, `"notes"`) {
		t.Fatalf("an inbound foreign key that refuses was not probed: %s", q)
	}
	if contains(q, `"audits"`) {
		t.Fatalf("an inbound foreign key that cascades was probed, and a cascade breaks nothing: %s", q)
	}
}

func TestTheDriversUnnamedViolationIsNotDoubledWhenTheProbeCoveredIt(t *testing.T) {
	rec := crudtest.MySQL()
	rec.Push(answer(true, false, false, false, false))
	f := declared(t, fixture())
	got, err := f.Enrich(ctx, request(conflict(""), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@Email")
	if got.Violations[0].Source.Constraint != "docs_email_uk" {
		t.Fatalf("the folded violation did not pick up the constraint the probe named: %+v", got.Violations[0])
	}
}

func TestAnUnnamedViolationIsNotFoldedIntoOneOfTwoCandidates(t *testing.T) {
	rec := crudtest.MySQL()
	rec.Push(answer(true, false, true, false, false))
	f := declared(t, fixture())
	got, err := f.Enrich(ctx, request(conflict(""), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if len(got.Violations) != 3 {
		t.Fatalf("with two candidates the unnamed violation was folded into one of them anyway: %v", codesAt(got))
	}
}

func TestANamedViolationFoldsOnlyIntoItsOwnConstraint(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, true, false, false))
	f := declared(t, fixture())
	got, err := f.Enrich(ctx, request(conflict("docs_slug_partial"), rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if len(got.Violations) != 2 {
		t.Fatalf("a violation naming a constraint the probe never ran was folded into an unrelated one: %v",
			codesAt(got))
	}
}

func TestANamedQualifiedViolationRequiresTheExactSourceIdentity(t *testing.T) {
	meta, err := crud.NewMetaInSchema[Doc]("tenant", "docs")
	if err != nil {
		t.Fatal(err)
	}
	f := &full{meta: meta}
	probed := errs.Violation{
		Code: errs.CodeUnique, Origin: errs.OriginState,
		Path:   errs.Path{errs.Named("Email")},
		Source: errs.Source{Schema: "tenant", Table: "docs", Constraint: "docs_email_uk", Columns: []string{"email"}},
	}
	mine, keep := []errs.Violation{probed}, []bool{true}

	for _, tc := range []struct {
		name          string
		schema, table string
		want          bool
	}{
		{"exact", "tenant", "docs", true},
		{"missing schema", "", "docs", false},
		{"other schema, same table and constraint", "shadow", "docs", false},
		{"same schema, other table and constraint name", "tenant", "shadow_docs", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := errs.Violation{
				Code: errs.CodeUnique, Origin: errs.OriginState,
				Source: errs.Source{Schema: tc.schema, Table: tc.table, Constraint: "docs_email_uk"},
			}
			_, got := f.same(&driver, mine, keep)
			if got != tc.want {
				t.Fatalf("same = %v, want %v for driver source %+v", got, tc.want, driver.Source)
			}
		})
	}

	legacy := &full{meta: docMeta(t)}
	missingSchema := errs.Violation{
		Code: errs.CodeUnique, Origin: errs.OriginState,
		Source: errs.Source{Table: "docs", Constraint: "docs_email_uk"},
	}
	if _, ok := legacy.same(&missingSchema, mine, keep); !ok {
		t.Fatal("legacy unqualified named violation no longer folds without a schema")
	}
	wrongSchema := missingSchema
	wrongSchema.Source.Schema = "shadow"
	if _, ok := legacy.same(&wrongSchema, mine, keep); ok {
		t.Fatal("legacy matching ignored a contradictory non-empty schema")
	}
}

func TestAnUnambiguousCodeOnlyViolationMayAcquireAnExactQualifiedSource(t *testing.T) {
	meta, err := crud.NewMetaInSchema[Doc]("tenant", "docs")
	if err != nil {
		t.Fatal(err)
	}
	f := &full{meta: meta}
	probed := errs.Violation{
		Code: errs.CodeUnique, Origin: errs.OriginState,
		Source: errs.Source{Schema: "tenant", Table: "docs", Constraint: "docs_email_uk", Columns: []string{"email"}},
	}
	mine, keep := []errs.Violation{probed}, []bool{true}

	driver := errs.Violation{Code: errs.CodeUnique, Origin: errs.OriginState}
	if _, ok := f.same(&driver, mine, keep); !ok {
		t.Fatal("one exact probe result did not match an empty code-only driver source")
	}
	driver.Source.Table = "shadow_docs"
	if _, ok := f.same(&driver, mine, keep); ok {
		t.Fatal("code-only matching overwrote a contradictory non-empty table")
	}
}

func TestFoldPreservesTheExactProbeSchema(t *testing.T) {
	driver := errs.Violation{Code: errs.CodeUnique, Origin: errs.OriginState}
	probed := errs.Violation{
		Code: errs.CodeUnique, Origin: errs.OriginState,
		Path:   errs.Path{errs.Named("Email")},
		Source: errs.Source{Schema: "tenant", Table: "docs", Constraint: "docs_email_uk", Columns: []string{"email"}},
	}
	fold(&driver, probed, false)
	if driver.Source.Schema != "tenant" || driver.Source.Table != "docs" ||
		driver.Source.Constraint != "docs_email_uk" || driver.Path.String() != "Email" {
		t.Fatalf("folded violation lost exact source/path: %+v", driver)
	}

	driver = errs.Violation{
		Code: errs.CodeUnique, Origin: errs.OriginState,
		Source: errs.Source{Schema: "driver_schema"},
	}
	fold(&driver, probed, false)
	if driver.Source.Schema != "driver_schema" {
		t.Fatalf("fold overwrote the driver's schema with %q", driver.Source.Schema)
	}
}

func TestTheAnswerIsSortedSoTheSameFailureRendersTheSameWay(t *testing.T) {
	first := ""
	for i := 0; i < 20; i++ {
		rec := crudtest.Postgres()
		rec.Push(answer(true, true, true, true, true))
		f := declared(t, fixture())
		got, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert())))
		if err != nil {
			t.Fatalf("the probe reported %v", err)
		}
		s := strings.Join(codesAt(got), "|")
		if i == 0 {
			first = s
			continue
		}
		if s != first {
			t.Fatalf("run %d produced %s, run 0 produced %s", i, s, first)
		}
	}
	if !contains(first, "restrict") && !contains(first, "unique") {
		t.Fatalf("the run produced nothing to order: %s", first)
	}
}
