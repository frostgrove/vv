package access

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/errs"
	"github.com/google/uuid"
)

func testRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, err := New(RuntimeSpec{
		Source: crudtest.Postgres(),
		Logger: slog.New(slog.DiscardHandler),
		Hasher: cheapHasher{},
	})
	if err != nil {
		t.Fatalf("a well-formed runtime was refused: %v", err)
	}
	return runtime
}

// cheapHasher keeps a suite that enrols several times from paying argon2's
// 60ms each. It is not a Hasher a deployment could use, which is the point of
// RuntimeSpec.Hasher being a field.
type cheapHasher struct{}

func (cheapHasher) Hash(password string) (string, error) { return "hashed:" + password, nil }

func (cheapHasher) Verify(password, encoded string) (bool, error) {
	return encoded == "hashed:"+password, nil
}

// A well-formed subject mounts. This is the control for the four refusals
// below: without it they would all pass even if Mount never accepted anything.
func TestMountRegistersAWellFormedSubject(t *testing.T) {
	runtime := testRuntime(t)
	mounted, signUp, err := Mount(runtime, SubjectSpec[struct{}]{
		Type:      testSubject,
		Directory: stubDirectory{active: true},
	})
	if err != nil {
		t.Fatalf("a well-formed subject was refused: %v", err)
	}
	if mounted.Subject().Type != testSubject {
		t.Fatalf("the mounted subject answers for %q", mounted.Subject().Type)
	}
	// No registrar, no sign-up: an invitation-only deployment mounts no route
	// rather than one that always refuses.
	if signUp != nil || mounted.Registers() {
		t.Fatal("a subject with no registrar was given a sign-up")
	}
	if mounted.Issuer() == nil || mounted.Authenticator() == nil {
		t.Fatal("the default strategy left the subject with no issuer or no verifier")
	}
}

// Two directories for one subject type is a composition mistake whose run-time
// symptom is a caller authenticated against the wrong store.
func TestMountRefusesTwoSubjectsOfOneType(t *testing.T) {
	runtime := testRuntime(t)
	spec := SubjectSpec[struct{}]{Type: testSubject, Directory: stubDirectory{}}
	if _, _, err := Mount(runtime, spec); err != nil {
		t.Fatalf("the first mount failed: %v", err)
	}
	if _, _, err := Mount(runtime, spec); err == nil {
		t.Fatal("a subject type was registered twice; whichever was mounted first would then decide")
	}
}

// Two subjects under one prefix collide on /auth/login, and the second one to
// mount wins silently.
func TestMountRefusesTwoSubjectsUnderOnePrefix(t *testing.T) {
	runtime := testRuntime(t)
	if _, _, err := Mount(runtime, SubjectSpec[struct{}]{
		Type:      testSubject,
		Directory: stubDirectory{},
	}); err != nil {
		t.Fatalf("the first mount failed: %v", err)
	}
	_, _, err := Mount(runtime, SubjectSpec[struct{}]{
		Type:      "service",
		Directory: stubDirectory{t: "service"},
		// The same (empty) prefix as the first.
	})
	if err == nil {
		t.Fatal("two subjects mounted under one prefix; the second would shadow the first's routes")
	}
}

// A spec whose type and directory disagree produces a subject that
// authenticates against a store that never claimed it.
func TestMountRefusesADirectoryThatAnswersForAnotherType(t *testing.T) {
	runtime := testRuntime(t)
	_, _, err := Mount(runtime, SubjectSpec[struct{}]{
		Type:      "service",
		Directory: stubDirectory{t: testSubject},
	})
	if err == nil {
		t.Fatal("a subject was mounted with a directory that answers for another type")
	}
}

func TestMountRefusesASpecWithNoTypeOrNoDirectory(t *testing.T) {
	runtime := testRuntime(t)
	if _, _, err := Mount(runtime, SubjectSpec[struct{}]{Directory: stubDirectory{}}); err == nil {
		t.Fatal("a subject with no type was mounted")
	}
	if _, _, err := Mount(runtime, SubjectSpec[struct{}]{Type: testSubject}); err == nil {
		t.Fatal("a subject with no directory was mounted")
	}
}

func TestMountRejectsNilLikeDirectoryAndStrategyDeclarations(t *testing.T) {
	var (
		directory *mountTestDirectory
		strategy  mountStrategyFunc
	)
	for _, tc := range []struct {
		name string
		spec SubjectSpec[struct{}]
	}{
		{
			name: "typed-nil directory",
			spec: SubjectSpec[struct{}]{Type: testSubject, Directory: directory},
		},
		{
			name: "typed-nil strategy",
			spec: SubjectSpec[struct{}]{Type: testSubject, Directory: stubDirectory{}, Strategy: strategy},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := testRuntime(t)
			if _, _, err := Mount(runtime, tc.spec); err == nil {
				t.Fatal("Mount accepted a nil-like extension declaration")
			}
			assertNothingMounted(t, runtime)

			if _, _, err := Mount(runtime, SubjectSpec[struct{}]{
				Type: testSubject, Directory: stubDirectory{}, Strategy: validMountStrategy(),
			}); err != nil {
				t.Fatalf("a corrected retry after the rejected declaration failed: %v", err)
			}
		})
	}
}

func TestMountRejectsTypedNilRegistrarBeforeBuildingOrPublishing(t *testing.T) {
	runtime := testRuntime(t)
	var registrar *mountTestRegistrar
	builds := 0
	strategy := mountStrategyFunc(func(dependencies StrategyDeps) (Issued, error) {
		builds++
		return OpaqueToken().Build(dependencies)
	})

	if _, _, err := Mount(runtime, SubjectSpec[struct{}]{
		Type: testSubject, Directory: stubDirectory{}, Registrar: registrar, Strategy: strategy,
	}); err == nil {
		t.Fatal("Mount accepted a typed-nil registrar")
	}
	if builds != 0 {
		t.Fatalf("Mount called Strategy.Build %d time(s) before rejecting the registrar", builds)
	}
	assertNothingMounted(t, runtime)

	if _, _, err := Mount(runtime, SubjectSpec[struct{}]{
		Type: testSubject, Directory: stubDirectory{}, Strategy: strategy,
	}); err != nil {
		t.Fatalf("a corrected retry after the rejected registrar failed: %v", err)
	}
	if builds != 1 {
		t.Fatalf("the corrected retry called Strategy.Build %d time(s), want one", builds)
	}
}

func TestMountRejectsIncompleteOrTypedNilIssuedCapabilitiesWithoutPublishing(t *testing.T) {
	var (
		typedNilIssuer        *mountTestIssuer
		typedNilAuthenticator auth.AuthenticatorFunc
		typedNilRefresher     *mountTestRefresher
		typedNilRevocations   *mountTestRevocationSink
	)
	for _, tc := range []struct {
		name       string
		invalidate func(*Issued)
	}{
		{"nil issuer", func(built *Issued) { built.Issuer = nil }},
		{"typed-nil issuer", func(built *Issued) { built.Issuer = typedNilIssuer }},
		{"nil authenticator", func(built *Issued) { built.Authenticator = nil }},
		{"typed-nil authenticator", func(built *Issued) { built.Authenticator = typedNilAuthenticator }},
		{"typed-nil refresher", func(built *Issued) { built.Refresher = typedNilRefresher }},
		{"typed-nil revocation sink", func(built *Issued) { built.Revocations = typedNilRevocations }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := testRuntime(t)
			wouldBePublished := &mountTestRevocationSink{}
			strategy := mountStrategyFunc(func(dependencies StrategyDeps) (Issued, error) {
				built, err := OpaqueToken().Build(dependencies)
				if err != nil {
					return Issued{}, err
				}
				built.Revocations = wouldBePublished
				tc.invalidate(&built)
				return built, nil
			})

			if _, _, err := Mount(runtime, SubjectSpec[struct{}]{
				Type: testSubject, Directory: stubDirectory{}, Strategy: strategy,
			}); err == nil {
				t.Fatal("Mount accepted an unusable strategy capability")
			}
			assertNothingMounted(t, runtime)

			// Validation is build-on-copy: correcting the same declaration must not
			// encounter a directory, subject, grant resolver or sink left behind.
			if _, _, err := Mount(runtime, SubjectSpec[struct{}]{
				Type: testSubject, Directory: stubDirectory{}, Strategy: validMountStrategy(),
			}); err != nil {
				t.Fatalf("a corrected strategy could not retry after validation failed: %v", err)
			}
		})
	}
}

func TestMountCanRetryAfterStrategyBuildFailure(t *testing.T) {
	runtime := testRuntime(t)
	if _, _, err := Mount(runtime, SubjectSpec[struct{}]{
		Type: testSubject, Prefix: "/users", Directory: stubDirectory{}, Strategy: validMountStrategy(),
	}); err != nil {
		t.Fatalf("mounting the control subject: %v", err)
	}
	beforeGrants := runtime.grants
	beforeDirectory := append([]Directory(nil), runtime.directory...)
	beforeSubjects := append([]*MountedSubject(nil), runtime.subjects...)

	buildFailure := errors.New("custom strategy cannot build")
	failing := mountStrategyFunc(func(StrategyDeps) (Issued, error) {
		return Issued{}, buildFailure
	})
	service := SubjectSpec[struct{}]{
		Type: "service", Prefix: "/services", Directory: stubDirectory{t: "service"}, Strategy: failing,
	}
	if _, _, err := Mount(runtime, service); !errors.Is(err, buildFailure) {
		t.Fatalf("Mount answered %v, want the strategy's build failure", err)
	}
	if runtime.grants != beforeGrants {
		t.Fatal("a failed Strategy.Build replaced the published grants resolver")
	}
	if len(runtime.directory) != len(beforeDirectory) ||
		runtime.directory[0].SubjectType() != beforeDirectory[0].SubjectType() {
		t.Fatalf("a failed Strategy.Build changed the directory registry: %#v", runtime.directory)
	}
	if len(runtime.subjects) != len(beforeSubjects) || runtime.subjects[0] != beforeSubjects[0] {
		t.Fatalf("a failed Strategy.Build changed the mounted subjects: %#v", runtime.subjects)
	}
	if !runtime.revocations.empty() {
		t.Fatalf("a failed Strategy.Build registered a revocation sink: %#v", runtime.revocations.byType)
	}

	service.Strategy = validMountStrategy()
	if _, _, err := Mount(runtime, service); err != nil {
		t.Fatalf("a corrected strategy could not retry after Build failed: %v", err)
	}
	if len(runtime.directory) != 2 || len(runtime.subjects) != 2 || runtime.grants == beforeGrants {
		t.Fatal("the successful retry did not publish the complete second subject")
	}
}

func assertNothingMounted(t *testing.T, runtime *Runtime) {
	t.Helper()
	if len(runtime.directory) != 0 {
		t.Fatalf("a rejected mount published directories: %#v", runtime.directory)
	}
	if runtime.grants != nil {
		t.Fatal("a rejected mount published a grants resolver")
	}
	if len(runtime.subjects) != 0 {
		t.Fatalf("a rejected mount published subjects: %#v", runtime.subjects)
	}
	if !runtime.revocations.empty() {
		t.Fatalf("a rejected mount published revocation sinks: %#v", runtime.revocations.byType)
	}
}

type mountStrategyFunc func(StrategyDeps) (Issued, error)

func (this mountStrategyFunc) Build(dependencies StrategyDeps) (Issued, error) {
	return this(dependencies)
}

func validMountStrategy() Strategy {
	return mountStrategyFunc(func(dependencies StrategyDeps) (Issued, error) {
		return OpaqueToken().Build(dependencies)
	})
}

type mountTestDirectory struct{}

func (*mountTestDirectory) SubjectType() SubjectType { panic("typed-nil directory was called") }
func (*mountTestDirectory) Active(context.Context, uuid.UUID) (bool, error) {
	panic("typed-nil directory was called")
}
func (*mountTestDirectory) Describe(context.Context, uuid.UUID) (Profile, error) {
	panic("typed-nil directory was called")
}
func (*mountTestDirectory) Touch(context.Context, uuid.UUID) error {
	panic("typed-nil directory was called")
}

type mountTestIssuer struct{}

func (*mountTestIssuer) Issue(context.Context, SubjectRef, Agent) (AuthResponse, error) {
	panic("typed-nil issuer was called")
}

type mountTestRefresher struct{}

func (*mountTestRefresher) Refresh(context.Context, string, Agent) (AuthResponse, error) {
	panic("typed-nil refresher was called")
}

type mountTestRevocationSink struct{}

func (*mountTestRevocationSink) SessionsRevoked(context.Context, []uuid.UUID) error {
	panic("typed-nil revocation sink was called")
}

type mountTestRegistrar struct{}

func (*mountTestRegistrar) Create(context.Context, struct{}) (uuid.UUID, string, error) {
	panic("typed-nil registrar was called")
}

func (*mountTestRegistrar) Password(struct{}) string {
	panic("typed-nil registrar was called")
}

// A runtime with no subject mounted resolves nothing and signs nobody in. The
// resolver has to exist by then, because the admin guard and the start-up sync
// both read it.
func TestARuntimeWithoutASubjectHasNoResolver(t *testing.T) {
	runtime := testRuntime(t)
	if runtime.Grants() != nil {
		t.Fatal("a resolver existed before any directory was registered")
	}
	if _, _, err := Mount(runtime, SubjectSpec[struct{}]{
		Type:      testSubject,
		Directory: stubDirectory{},
	}); err != nil {
		t.Fatalf("mount failed: %v", err)
	}
	if runtime.Grants() == nil {
		t.Fatal("mounting a subject left the runtime with no resolver")
	}
}

// A runtime needs a source and a logger. The library never writes to a
// process-wide logger, so an absent one is a misconfiguration and not a
// default.
func TestARuntimeRefusesToBuildWithoutASourceOrALogger(t *testing.T) {
	if _, err := New(RuntimeSpec{Logger: slog.New(slog.DiscardHandler)}); err == nil {
		t.Fatal("a runtime was built with no source")
	}
	if _, err := New(RuntimeSpec{Source: crudtest.Postgres()}); err == nil {
		t.Fatal("a runtime was built with no logger")
	}
}

// Everything an enrolment can refuse runs before anything is written, so a
// rejected password is not a half-enrolled subject.
func TestEnrolmentRefusesBeforeItWritesAnything(t *testing.T) {
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
	subject := SubjectRef{Type: testSubject, ID: uuid.New()}

	cases := map[string]EnrollCommand{
		"an empty subject":    {Identifier: "ann@example.com", Password: "0123456789"},
		"an empty identifier": {Subject: subject, Password: "0123456789"},
		"a short password":    {Subject: subject, Identifier: "ann@example.com", Password: "short"},
	}
	for name, command := range cases {
		recorder.Reset()
		if err := enrol.Execute(context.Background(), command); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if statements := recorder.Statements(); len(statements) != 0 {
			t.Errorf("%s was refused after %d statement(s): %v", name, len(statements), statements)
		}
	}
}

// The password rule is length and nothing else, and the minimum travels with
// the violation so a message catalogue can say what it is.
func TestAWeakPasswordNamesTheFieldAndCarriesTheMinimum(t *testing.T) {
	runtime := testRuntime(t)
	runtime.config = Config{Password: PasswordConfig{MinLength: 10}}
	enrol := NewEnroll(newDeps(runtime.store, nil, runtime.hasher, runtime.config, runtime.logger, runtime.revocations))

	err := enrol.Execute(context.Background(), EnrollCommand{
		Subject:    SubjectRef{Type: testSubject, ID: uuid.New()},
		Identifier: "ann@example.com",
		Password:   "012345678",
	})
	fault, ok := errs.AsFault(err)
	if !ok || len(fault.Violations) != 1 {
		t.Fatalf("the refusal is %v, which no transport can turn into a 422 naming a field", err)
	}
	if fault.Violations[0].Path[0].Name != "Password" {
		t.Fatalf("the refusal does not name the password field: %v", fault.Violations[0].Path)
	}
	if fault.Violations[0].Params["min"] != 10 {
		t.Fatalf("params = %v, want the configured minimum", fault.Violations[0].Params)
	}
}

// A registrar that refuses stops the sign-up before a session is opened. The
// caller sees the registrar's own error — a closed deployment, a malformed
// address — rather than a generic failure from further down.
func TestASignUpThatTheRegistrarRefusesOpensNoSession(t *testing.T) {
	runtime := testRuntime(t)
	mounted, signUp, err := Mount(runtime, SubjectSpec[testForm]{
		Type:      testSubject,
		Directory: stubDirectory{active: true},
		Normalize: strings.ToLower,
		Registrar: refusingRegistrar{},
	})
	if err != nil {
		t.Fatalf("mount failed: %v", err)
	}
	if signUp == nil || !mounted.Registers() {
		t.Fatal("a subject with a registrar was given no sign-up")
	}

	_, err = signUp.Execute(context.Background(), testForm{Email: "Ann@Example.com"}, Agent{})
	if !errors.Is(err, errRegistrarRefused) {
		t.Fatalf("the sign-up answered %v, want the registrar's own refusal", err)
	}
}

type testForm struct {
	Email    string
	Password string
}

var errRegistrarRefused = errors.New("registration is closed")

type refusingRegistrar struct{}

func (refusingRegistrar) Create(context.Context, testForm) (uuid.UUID, string, error) {
	return uuid.Nil, "", errRegistrarRefused
}

func (refusingRegistrar) Password(form testForm) string { return form.Password }
