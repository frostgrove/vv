package access

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/google/uuid"
)

func testSeeder(recorder *crudtest.Recorder) *Seeder {
	return NewSeeder(NewStore(recorder), slog.New(slog.DiscardHandler))
}

// roleRow and defaultRoleRow are the canned result sets these tests replay, in
// model field order — which is the order a repository selects its columns in.
// Written once here because getting the order wrong is a scan error that reads
// like a bug in the code under test.
//
// The ids go over as text because that is what a driver hands back: uuid.UUID's
// Scan takes a string, and pushing the value itself fails to scan into its own
// type.
func roleRow(id uuid.UUID, slug string) []any {
	return []any{id.String(), slug, slug, true, time.Now()}
}

func defaultRoleRow(id, roleID uuid.UUID, subjectType string) []any {
	return []any{id.String(), subjectType, roleID.String(), time.Now()}
}

// wrote reports whether anything recorded changed a row.
//
// The verb and not Statement.Query: an UPDATE carrying RETURNING is issued as a
// query, so "it did not run a Query" is not the same statement as "it wrote
// nothing" — and a test that used the second would pass while the row changed.
func wrote(statements []crudtest.Statement) (string, bool) {
	for _, statement := range statements {
		switch verb := strings.ToUpper(strings.Fields(strings.TrimSpace(statement.SQL))[0]); verb {
		case "INSERT", "UPDATE", "DELETE":
			return statement.SQL, true
		}
	}
	return "", false
}

// The default role is read from the table and nowhere else. This is the whole
// of [[D-070]] from the reading side: what a sign-up grants is a row, and the
// slug on it is what reaches the enrolment.
func TestTheDefaultRoleIsWhateverTheTableSays(t *testing.T) {
	recorder := crudtest.Postgres()
	roleID := uuid.New()
	recorder.Push(
		crudtest.Rows(defaultRoleRow(uuid.New(), roleID, string(testSubject))),
		crudtest.Rows(roleRow(roleID, "client")),
	)

	dependencies := newDeps(NewStore(recorder), nil, nil, Config{}, slog.New(slog.DiscardHandler))
	role, err := dependencies.DefaultRole(context.Background(), testSubject)
	if err != nil {
		t.Fatalf("reading the default role: %v", err)
	}
	if role == nil || role.Slug != "client" {
		t.Fatalf("the default role is %+v, want the role the table pointed at", role)
	}
	// The whole row, so the enrolment grants it without a second lookup.
	if role.ID != roleID {
		t.Fatalf("the default role came back with id %s, want %s", role.ID, roleID)
	}

	// The lookup is keyed on the subject type. Without it in the predicate, one
	// kind of caller's default is whichever row the engine reached first.
	if !strings.Contains(recorder.Statements()[0].SQL, "subject_default_roles") {
		t.Fatalf("the default role was not read from its table: %v", recorder.SQL())
	}
	if args := recorder.Statements()[0].Args; len(args) == 0 || args[0] != string(testSubject) {
		t.Fatalf("the lookup does not carry the subject type: %v", args)
	}
}

// The control for the test above: with no row, the sign-up grants nothing
// rather than guessing. A deployment where an administrator does the granting
// is a supported state, not a misconfiguration.
func TestASubjectTypeWithNoDefaultRoleGrantsNothing(t *testing.T) {
	recorder := crudtest.Postgres()
	dependencies := newDeps(NewStore(recorder), nil, nil, Config{}, slog.New(slog.DiscardHandler))

	role, err := dependencies.DefaultRole(context.Background(), testSubject)
	if err != nil {
		t.Fatalf("an absent default is a state, not a failure: %v", err)
	}
	if role != nil {
		t.Fatalf("the default role is %+v, want nothing at all", role)
	}
}

// The sign-up asks the table before it creates anything. Reading it afterwards
// would let a registration write an account and then fail on a default nobody
// had configured, which is the half-registered state the transaction exists to
// prevent.
func TestASignUpReadsTheDefaultRoleBeforeItCreatesAnAccount(t *testing.T) {
	runtime := testRuntime(t)
	recorder := runtime.source.(*crudtest.Recorder)
	recorder.Reset()

	_, signUp, err := Mount(runtime, SubjectSpec[testForm]{
		Type:      testSubject,
		Directory: stubDirectory{active: true},
		Registrar: refusingRegistrar{},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The registrar refuses, so nothing past it runs — which is exactly what
	// makes the first statement the assertion.
	_, _ = signUp.Execute(context.Background(), testForm{Email: "ann@example.com"}, Agent{})

	statements := recorder.Statements()
	if len(statements) == 0 {
		t.Fatal("the sign-up read no default role at all")
	}
	if !strings.Contains(statements[0].SQL, "subject_default_roles") {
		t.Fatalf("the first statement a sign-up runs is %q, want the default-role lookup", statements[0].SQL)
	}
}

// A default naming a role nobody created is refused by the command an operator
// is watching, rather than at the first registration weeks later. That is the
// whole reason this is not a configuration key.
func TestSettingADefaultRoleThatDoesNotExistIsRefusedAndWritesNothing(t *testing.T) {
	recorder := crudtest.Postgres()
	seeder := testSeeder(recorder)

	_, err := seeder.SetDefaultRole(context.Background(), testSubject, "lawyer")
	if err == nil {
		t.Fatal("a default pointing at a role that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "lawyer") {
		t.Fatalf("the refusal does not name the role: %v", err)
	}
	if statement, did := wrote(recorder.Statements()); did {
		t.Fatalf("a refused default still wrote: %s", statement)
	}
}

// Running the seed twice writes once. A command that inserts a second row on
// the second run is a command nobody dares re-run, and re-running it after a
// migration is the whole point of having one.
func TestSettingTheDefaultRoleToWhatItAlreadyIsWritesNothing(t *testing.T) {
	recorder := crudtest.Postgres()
	roleID := uuid.New()
	recorder.Push(
		crudtest.Rows(roleRow(roleID, "client")),
		crudtest.Rows(defaultRoleRow(uuid.New(), roleID, string(testSubject))),
	)

	if _, err := testSeeder(recorder).SetDefaultRole(context.Background(), testSubject, "client"); err != nil {
		t.Fatalf("re-binding the default role failed: %v", err)
	}
	if statement, did := wrote(recorder.Statements()); did {
		t.Fatalf("a no-op re-seed wrote %s; updated_at is what somebody reads to date the last change", statement)
	}
}

// The control for the test above: a default that has actually changed is
// written. Without this, a SetDefaultRole that never wrote anything at all
// would pass the idempotence test.
func TestSettingTheDefaultRoleToADifferentRoleWrites(t *testing.T) {
	recorder := crudtest.Postgres()
	bindingID, wasPointingAt := uuid.New(), uuid.New()
	recorder.Push(
		crudtest.Rows(roleRow(uuid.New(), "lawyer")),
		crudtest.Rows(defaultRoleRow(bindingID, wasPointingAt, string(testSubject))),
		// Update is load-diff-write: it locks and reads the row it is about to
		// change, then reads it back through RETURNING.
		crudtest.Rows(defaultRoleRow(bindingID, wasPointingAt, string(testSubject))),
		crudtest.Rows(defaultRoleRow(bindingID, wasPointingAt, string(testSubject))),
	)

	if _, err := testSeeder(recorder).SetDefaultRole(context.Background(), testSubject, "lawyer"); err != nil {
		t.Fatalf("re-pointing the default role failed: %v", err)
	}
	if _, did := wrote(recorder.Statements()); !did {
		t.Fatalf("changing the default role wrote nothing: %v", recorder.SQL())
	}
}

// A role wanting a permission no module declared is refused rather than
// skipped. Attaching it would produce a row that reads like a grant and decides
// nothing, and the usual cause is a typo in the seed.
func TestSeedingARoleRefusesAPermissionNobodyDeclared(t *testing.T) {
	recorder := crudtest.Postgres()
	roleID := uuid.New()
	recorder.Push(
		crudtest.Rows(roleRow(roleID, "lawyer")),
		crudtest.Rows(), // the permission lookup finds nothing
	)

	_, err := testSeeder(recorder).EnsureRole(context.Background(), RoleSpec{
		Slug:        "lawyer",
		Permissions: []auth.Permission{"contract.redline"},
	})
	if err == nil {
		t.Fatal("a role was seeded with a permission nothing enforces")
	}
	if !strings.Contains(err.Error(), "contract.redline") {
		t.Fatalf("the refusal does not name the permission: %v", err)
	}
}

// A role the caller already resolved is granted without looking its slug up
// again. That is the whole reason the sign-up reads the binding with the role
// preloaded: it is holding the row a second statement would fetch.
func TestAResolvedRoleIsGrantedWithoutASecondLookup(t *testing.T) {
	recorder := crudtest.Postgres()
	runtime, err := New(RuntimeSpec{
		Source: recorder,
		Logger: slog.New(slog.DiscardHandler),
		Hasher: cheapHasher{},
		Config: Config{Password: PasswordConfig{MinLength: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	enrol := NewEnroll(newDeps(runtime.store, nil, runtime.hasher, runtime.config, runtime.logger))

	resolved := &Role{ID: uuid.New(), Slug: "client"}
	command := EnrollCommand{
		Subject:    SubjectRef{Type: testSubject, ID: uuid.New()},
		Identifier: "ann@example.com",
		Password:   "0123456789",
		Role:       "client",
	}

	if err := enrol.execute(context.Background(), command, resolved); err != nil {
		t.Fatalf("enrolling with a resolved role: %v", err)
	}
	if reads(recorder, `FROM "roles"`) {
		t.Fatalf("the role was looked up again: %v", recorder.SQL())
	}
	if !wroteInto(recorder, "subject_roles") {
		t.Fatalf("no role was granted at all: %v", recorder.SQL())
	}

	// The control, and it is the one that matters: without a resolved role the
	// lookup still happens. Otherwise the test above would pass on an enrolment
	// that had stopped granting roles entirely.
	recorder.Reset()
	recorder.Push(crudtest.Rows(roleRow(uuid.New(), "client")))
	if err := enrol.execute(context.Background(), command, nil); err != nil {
		t.Fatalf("enrolling without a resolved role: %v", err)
	}
	if !reads(recorder, `FROM "roles"`) {
		t.Fatalf("an unresolved role was granted without being looked up: %v", recorder.SQL())
	}
}

// A resolved role belonging to another slug is not trusted. Granting it would
// give the subject one role while the command named a different one, and
// nothing anywhere would report the difference.
func TestAResolvedRoleForAnotherSlugIsLookedUpAnyway(t *testing.T) {
	recorder := crudtest.Postgres()
	runtime, err := New(RuntimeSpec{
		Source: recorder,
		Logger: slog.New(slog.DiscardHandler),
		Hasher: cheapHasher{},
		Config: Config{Password: PasswordConfig{MinLength: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	enrol := NewEnroll(newDeps(runtime.store, nil, runtime.hasher, runtime.config, runtime.logger))

	wanted := uuid.New()
	recorder.Push(crudtest.Rows(roleRow(wanted, "client")))

	err = enrol.execute(context.Background(), EnrollCommand{
		Subject:    SubjectRef{Type: testSubject, ID: uuid.New()},
		Identifier: "ann@example.com",
		Password:   "0123456789",
		Role:       "client",
	}, &Role{ID: uuid.New(), Slug: "lawyer"})
	if err != nil {
		t.Fatalf("enrolling: %v", err)
	}
	if !reads(recorder, `FROM "roles"`) {
		t.Fatalf("a role resolved for another slug was granted as-is: %v", recorder.SQL())
	}
}

func reads(recorder *crudtest.Recorder, fragment string) bool {
	for _, statement := range recorder.SQL() {
		if strings.Contains(statement, fragment) && strings.HasPrefix(strings.TrimSpace(statement), "SELECT") {
			return true
		}
	}
	return false
}

// wroteInto looks at every statement, not only the first write: an enrolment
// writes the credential before it writes the grant, so "the first write
// mentioned this table" is a different question from the one being asked.
func wroteInto(recorder *crudtest.Recorder, table string) bool {
	for _, statement := range recorder.SQL() {
		if strings.HasPrefix(strings.TrimSpace(statement), "INSERT") && strings.Contains(statement, table) {
			return true
		}
	}
	return false
}
