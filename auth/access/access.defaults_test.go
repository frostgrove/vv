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

func roleRow(id uuid.UUID, slug string) []any {
	return []any{id.String(), slug, slug, true, time.Now()}
}

func defaultRoleRow(id, roleID uuid.UUID, subjectType string) []any {
	return []any{id.String(), subjectType, roleID.String(), time.Now()}
}

func wrote(statements []crudtest.Statement) (string, bool) {
	for _, statement := range statements {
		switch verb := strings.ToUpper(strings.Fields(strings.TrimSpace(statement.SQL))[0]); verb {
		case "INSERT", "UPDATE", "DELETE":
			return statement.SQL, true
		}
	}
	return "", false
}

func TestTheDefaultRoleIsWhateverTheTableSays(t *testing.T) {
	recorder := crudtest.Postgres()
	roleID := uuid.New()
	recorder.Push(
		crudtest.Rows(defaultRoleRow(uuid.New(), roleID, string(testSubject))),
		crudtest.Rows(roleRow(roleID, "client")),
	)

	dependencies := newDeps(NewStore(recorder), nil, nil, Config{}, slog.New(slog.DiscardHandler), nil)
	role, err := dependencies.DefaultRole(context.Background(), testSubject)
	if err != nil {
		t.Fatalf("reading the default role: %v", err)
	}
	if role == nil || role.Slug != "client" {
		t.Fatalf("the default role is %+v, want the role the table pointed at", role)
	}

	if role.ID != roleID {
		t.Fatalf("the default role came back with id %s, want %s", role.ID, roleID)
	}

	if !strings.Contains(recorder.Statements()[0].SQL, "subject_default_roles") {
		t.Fatalf("the default role was not read from its table: %v", recorder.SQL())
	}
	if args := recorder.Statements()[0].Args; len(args) == 0 || args[0] != string(testSubject) {
		t.Fatalf("the lookup does not carry the subject type: %v", args)
	}
}

func TestASubjectTypeWithNoDefaultRoleGrantsNothing(t *testing.T) {
	recorder := crudtest.Postgres()
	dependencies := newDeps(NewStore(recorder), nil, nil, Config{}, slog.New(slog.DiscardHandler), nil)

	role, err := dependencies.DefaultRole(context.Background(), testSubject)
	if err != nil {
		t.Fatalf("an absent default is a state, not a failure: %v", err)
	}
	if role != nil {
		t.Fatalf("the default role is %+v, want nothing at all", role)
	}
}

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

	_, _ = signUp.Execute(context.Background(), testForm{Email: "ann@example.com"}, Agent{})

	statements := recorder.Statements()
	if len(statements) == 0 {
		t.Fatal("the sign-up read no default role at all")
	}
	if !strings.Contains(statements[0].SQL, "subject_default_roles") {
		t.Fatalf("the first statement a sign-up runs is %q, want the default-role lookup", statements[0].SQL)
	}
}

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

func TestSettingTheDefaultRoleToADifferentRoleWrites(t *testing.T) {
	recorder := crudtest.Postgres()
	bindingID, wasPointingAt := uuid.New(), uuid.New()
	recorder.Push(
		crudtest.Rows(roleRow(uuid.New(), "lawyer")),
		crudtest.Rows(defaultRoleRow(bindingID, wasPointingAt, string(testSubject))),

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

func TestSeedingARoleRefusesAPermissionNobodyDeclared(t *testing.T) {
	recorder := crudtest.Postgres()
	roleID := uuid.New()
	recorder.Push(
		crudtest.Rows(roleRow(roleID, "lawyer")),
		crudtest.Rows(),
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
	enrol := NewEnroll(newDeps(runtime.store, nil, runtime.hasher, runtime.config, runtime.logger, runtime.revocations))

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

	recorder.Reset()
	recorder.Push(crudtest.Rows(roleRow(uuid.New(), "client")))
	if err := enrol.execute(context.Background(), command, nil); err != nil {
		t.Fatalf("enrolling without a resolved role: %v", err)
	}
	if !reads(recorder, `FROM "roles"`) {
		t.Fatalf("an unresolved role was granted without being looked up: %v", recorder.SQL())
	}
}

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
	enrol := NewEnroll(newDeps(runtime.store, nil, runtime.hasher, runtime.config, runtime.logger, runtime.revocations))

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

func wroteInto(recorder *crudtest.Recorder, table string) bool {
	for _, statement := range recorder.SQL() {
		if strings.HasPrefix(strings.TrimSpace(statement), "INSERT") && strings.Contains(statement, table) {
			return true
		}
	}
	return false
}
