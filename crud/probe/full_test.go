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

// insert is one full row of the model, every column non-null. Individual tests
// take a copy and null out the one column they are about.
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

// fullInsertTerms is how many terms a full insert plans against the fixture:
// the three reproducible unique keys and the two foreign keys. Written out so a
// test that changes the plan has to say so rather than silently reading a
// shorter answer. dup_test.go names the constraints one by one.
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

// The control: the same probe, same everything, with the statement answering
// yes. Without it the test above passes for a probe that never runs at all.
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

// The control: the twin that succeeds sets no Partial, so the assertion above is
// about the failure and not about the probe running at all.
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

// The control. Without it, a probe that never builds a foreign-key term at all
// passes the test above.
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
	// The control: the plain twins of the same shape, on the same columns, are.
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

// PostgreSQL truncates an identifier at 63 bytes with a NOTICE no driver
// surfaces, so two constraints sharing their first 61 characters would alias
// onto one column. Catalog order carries the identity instead.
func TestResultsAreReadByPositionAndNotByAlias(t *testing.T) {
	long := strings.Repeat("x", 61)
	cat := fixture()
	docs, _ := cat.Table("docs")
	docs.Constraints = append(docs.Constraints,
		newUnique(long+"_a", "email"), newUnique(long+"_b", "code"))

	rec := crudtest.Postgres()
	// Six terms now: the five of a full insert plus the two long-named ones,
	// minus none. Only the last is true.
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

// The control for the test above: short names, same shape, same positions.
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
		// ON CONFLICT (pk) DO UPDATE swallows the primary key and nothing else,
		// so the three unique keys and the two foreign keys are all probed.
		{"postgres", crudtest.Postgres(), true, 5},
		// ON DUPLICATE KEY UPDATE swallows every unique key, so only the two
		// foreign keys are left.
		{"mysql", crudtest.MySQL(), false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cells := make([]any, tc.cells)
			for i := range cells {
				cells[i] = false
			}
			tc.rec.Push(crudtest.Rows(cells))
			f := declared(t, fixture())
			req := request(conflict("", ""), tc.rec, docMeta(t), idRow(int64(5), insert()))
			req.Upsert = true
			if _, err := f.Enrich(ctx, req); err != nil {
				t.Fatalf("the probe reported %v", err)
			}
			got := contains(lastSQL(tc.rec), tc.rec.Dialect().Quote("email"))
			if got != tc.wantsEmail {
				t.Fatalf("%s probed email = %v, want %v: %s", tc.name, got, tc.wantsEmail, lastSQL(tc.rec))
			}
		})
	}
}

// The control: a keyless Save is a plain INSERT with no conflict clause, so
// nothing is swallowed and every key is probed on both engines.
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
	// An update that changes the email and nothing else.
	req := request(conflict("", ""), rec, docMeta(t), idRow(int64(9), map[string]any{"email": "b@x.io"}))
	req.Op, req.Stored = "Update", true
	if _, err := f.Enrich(ctx, req); err != nil {
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
	req := request(conflict("", ""), rec, docMeta(t), idRow(int64(9), map[string]any{"email": "b@x.io"}))
	req.Op, req.Stored = "Update", true
	if _, err := f.Enrich(ctx, req); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if !contains(lastSQL(rec), `<>`) {
		t.Fatalf("an update did not exclude its own row, so a key it did not change collides with itself: %s", lastSQL(rec))
	}
}

// The control: an insert has no own row to exclude.
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

// The control: the same write with room to spare says nothing about being
// incomplete.
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
	req := request(conflict("", ""), rec, docMeta(t), row(insert()), row(insert()))
	req.Batch = true
	got, err := f.Enrich(ctx, req)
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
	req := request(conflict("", ""), rec, docMeta(t), row(insert()), row(insert()))
	req.Batch = true
	got, err := f.Enrich(ctx, req)
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

// The control: the same slow statement with a timeout that fits.
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

func (s *slowRecorder) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(s.delay):
	}
	return s.Recorder.Query(ctx, q, args...)
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
	// The control: everything else still is, so the opt-out took one thing out
	// rather than turning the probe off.
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

// The control: the default names the field.
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
		name string
		opts []Option
		want any
	}{
		{"default", nil, nil},
		{"with values", []Option{WithValues()}, "a@x.io"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres()
			rec.Push(answer(true, false, false, false, false))
			f := declared(t, fixture(), tc.opts...)
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
	// The parent table's own subquery names no scope: the model's predicate is
	// over the model's own fields and would not compile there.
	if contains(q[i:], `"tenant_id"`) {
		t.Fatalf("the scope was pushed into a subquery over another table: %s", q)
	}
}

// The control: without the option, the unique term carries no narrowing at all.
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
	req := request(conflict("", ""), rec, docMeta(t), idRow(int64(9), map[string]any{"code": "C2"}))
	req.Op, req.Stored = "Update", true
	if _, err := f.Enrich(ctx, req); err != nil {
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
	// MySQL, MariaDB and SQLite carry no constraint name in their structured
	// error, so the driver's violation arrives with nothing on it but a code.
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

// Control one: with two unique violations of the same code there is no way to
// tell which of them the engine stopped at, so the driver's stays as it is.
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

// Control two: where the driver names a constraint, the fold is by name and not
// by code, so a probe result for a different constraint does not absorb it.
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
