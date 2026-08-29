package access

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
)

// A RuntimeSpec is everything this context needs from an application, once.
//
// The application fills in four fields and never assembles a Store, a resolver,
// a hasher or an authenticator itself. That is deliberate: the pieces have to
// be built in one order, over one source, with one config, and an application
// that wires them by hand gets one of those wrong in a way that compiles.
type RuntimeSpec struct {
	Source crud.Source
	Config Config
	Logger *slog.Logger

	// Hasher is the password hasher. nil means argon2id at RFC 9106's second
	// recommended cost, which is what a deployment should want; a test hands in
	// a cheap one so a suite that signs in fifty times does not take a minute.
	Hasher Hasher
}

// A Runtime is this context, built. It owns the stores every subject shares and
// the subjects registered against them.
type Runtime struct {
	store  *Store
	source crud.Source
	grants *GrantsService
	hasher Hasher
	config Config
	logger *slog.Logger

	subjects  []*MountedSubject
	declared  []ModuleGrants
	directory []Directory

	// revocations is what each subject's strategy asked to be told about a
	// session closing. Shared by pointer with every Deps built below, so a
	// subject mounted after a use case was assembled is still reachable from
	// it — see access.revocation.go.
	revocations *revocationSinks
}

// New builds the shared half.
//
// No subject exists yet: a directory is registered by [Mount], because the
// thing that owns an identity and the thing that decides how it holds a session
// are the same decision and are made in one place.
func New(spec RuntimeSpec) (*Runtime, error) {
	if spec.Source == nil {
		return nil, fmt.Errorf("access: a runtime needs a crud.Source")
	}
	if spec.Logger == nil {
		return nil, fmt.Errorf("access: a runtime needs a logger; the library never writes to a process-wide one")
	}
	hasher := spec.Hasher
	if hasher == nil {
		hasher = NewHasher()
	}
	return &Runtime{
		store:       NewStore(spec.Source),
		source:      spec.Source,
		hasher:      hasher,
		config:      spec.Config,
		logger:      spec.Logger,
		declared:    []ModuleGrants{OwnGrants()},
		revocations: newRevocationSinks(),
	}, nil
}

// Store answers the repositories, for an application that needs a query this
// module does not have. It is not how a subject is wired.
func (this *Runtime) Store() *Store { return this.store }

// Config answers the settings this runtime was built with.
func (this *Runtime) Config() Config { return this.config }

// Seeder answers the idempotent writes an application's seed command performs:
// the roles a product has, and which one a kind of caller is given on sign-up.
//
// Separate from [Runtime.Sync], which is the code's own facts and runs at every
// start. See [Seeder] for why the two are not one pass.
func (this *Runtime) Seeder() *Seeder { return NewSeeder(this.store, this.logger) }

// SetPassword answers the administrator's password reset, which is also what
// makes an account somebody provisioned able to sign in at all.
//
// Handed out assembled rather than as the pieces, for the reason [RuntimeSpec]
// gives: an application building this itself would pass the store, the hasher
// and the config in some order, and every wrong one of those compiles.
//
// It is a method and not a field because it needs the resolver, and the
// resolver does not exist until the last [Mount]. Calling this before then
// answers a use case whose directory lookup fails at run time, which is the one
// mistake a lazy constructor here removes.
func (this *Runtime) SetPassword() *SetPasswordUseCase {
	return NewSetPassword(newDeps(
		this.store, this.grants, this.hasher, this.config, this.logger, this.revocations))
}

// Declare adds a module's permissions and system roles to what [Runtime.Sync]
// will fold into the tables.
func (this *Runtime) Declare(grants ...ModuleGrants) {
	this.declared = append(this.declared, grants...)
}

// A SubjectSpec is everything one kind of caller has to supply.
//
// The compiler is the documentation: a field left out is a build failure rather
// than a behaviour somebody has to know about. Every one of these was a step a
// consumer previously had to know to perform, and the two that were forgotten
// most — the identifier rule and the subject type on a sign-in — are the two
// that fail silently.
type SubjectSpec[P any] struct {
	// Type is the morph key. Required.
	Type SubjectType
	// Prefix goes in front of /auth. Empty mounts at /auth/login, which is what
	// a single-subject application wants; a second subject passes its own so
	// the two surfaces do not collide.
	Prefix string
	// Directory is the identity store behind Type. Required.
	Directory Directory
	// Normalize folds an identifier into the one spelling stored and looked up.
	// nil compares verbatim, which is right for an opaque external subject id
	// and wrong for an email address.
	Normalize func(identifier string) string
	// Registrar creates the account behind a self-service sign-up. nil mounts
	// no sign-up route at all, which is what an invitation-only deployment
	// wants — a mounted route that always refuses says one exists.
	Registrar Registrar[P]
	// Strategy is how this caller holds a session. nil means [OpaqueToken].
	Strategy Strategy
}

// A MountedSubject is one registered kind of caller: its identity store, its
// strategy, and the endpoints a binding will mount for it.
//
// A binding reads it and mounts; nothing else needs its internals, which is why
// the fields behind these methods are not exported. An application that could
// reach the raw authenticator could mount it on the wrong group.
type MountedSubject struct {
	subject Subject
	prefix  string

	issuer        SessionIssuer
	authenticator auth.Authenticator
	refresher     SessionRefresher
	endpoints     Endpoints
	registers     bool
}

// Subject answers the registered subject.
func (this *MountedSubject) Subject() Subject { return this.subject }

// Prefix answers where this subject's endpoints are mounted.
func (this *MountedSubject) Prefix() string { return this.prefix }

// Guard is the middleware a binding puts in front of this subject's routes.
//
// One guard per subject and not one for the whole API, which is what lets two
// kinds of caller hold sessions in two formats: the group a request arrived on
// selects the verifier before anything is parsed, so a deployment that issues
// only JWTs never runs an opaque lookup and answers "that is not a JWT" rather
// than a 401 after several failed attempts.
//
// It is Optional, and that is not a hole: an anonymous request reaches the
// handler without a principal — which is what a sign-in route needs — and every
// handler that needs a caller asks for one. A *bad* credential is still a 401
// at the door.
//
// The options are appended to that one, so a deployment can say where the
// credential is read from without restating what a guard here is for. A browser
// holding its access token in a cookie is the reason this is not fixed:
// `subject.Guard(authhttp.Cookie(table.AccessCookie()))`.
func (this *MountedSubject) Guard(options ...auth.Option) *auth.Guard {
	return auth.NewGuard(this.authenticator, append([]auth.Option{auth.Optional()}, options...)...)
}

// Issuer answers what mints a session for this subject.
func (this *MountedSubject) Issuer() SessionIssuer { return this.issuer }

// Authenticator answers what verifies one. A binding wants [MountedSubject.Guard].
func (this *MountedSubject) Authenticator() auth.Authenticator { return this.authenticator }

// Endpoints is the transport-neutral operation set a binding mounts.
//
// A plain type and not an `any`: registering is the one operation whose payload
// is the application's, and it comes back from [Mount] on its own rather than
// dragging a type parameter through everything else.
func (this *MountedSubject) Endpoints() Endpoints { return this.endpoints }

// Registers reports whether this subject has a sign-up route to mount.
func (this *MountedSubject) Registers() bool { return this.registers }

// Refreshes reports whether this subject's strategy rotates, and therefore
// whether a refresh route exists to mount.
func (this *MountedSubject) Refreshes() bool { return this.refresher != nil }

// Mount registers one kind of caller against a runtime.
//
// It answers two values because the subject has two halves with different
// shapes. The [MountedSubject] is what every binding needs and is not generic;
// the [SignUpUseCase] carries the application's own sign-up payload and is
// handed back typed, so nothing has to erase it and assert it back.
//
// The sign-up is nil when the spec has no registrar, which is what an
// invitation-only deployment passes.
//
// A free function rather than a method because Go has no generic methods, and
// the payload type belongs to the spec rather than to the runtime.
func Mount[P any](runtime *Runtime, spec SubjectSpec[P]) (*MountedSubject, *SignUpUseCase[P], error) {
	if runtime == nil {
		return nil, nil, fmt.Errorf("access: mounting a subject on a nil runtime")
	}
	if spec.Type == "" {
		return nil, nil, fmt.Errorf("access: a subject spec needs a type")
	}
	if spec.Directory == nil {
		return nil, nil, fmt.Errorf("access: subject %q has no directory", spec.Type)
	}
	if declared := spec.Directory.SubjectType(); declared != spec.Type {
		return nil, nil, fmt.Errorf("access: subject %q was given a directory that answers for %q", spec.Type, declared)
	}
	for _, mounted := range runtime.subjects {
		if mounted.subject.Type == spec.Type {
			return nil, nil, fmt.Errorf("access: subject type %q is registered twice", spec.Type)
		}
		if mounted.prefix == spec.Prefix {
			return nil, nil, fmt.Errorf("access: subjects %q and %q both mount under the prefix %q",
				mounted.subject.Type, spec.Type, spec.Prefix)
		}
	}

	subject := Subject{Type: spec.Type, Directory: spec.Directory, Normalize: spec.Normalize}

	// The resolver is built on first use rather than in New, because it needs
	// every directory and the last one is not registered until now.
	runtime.directory = append(runtime.directory, spec.Directory)
	directories, err := NewDirectories(runtime.directory...)
	if err != nil {
		return nil, nil, err
	}
	runtime.grants = NewGrants(runtime.store, directories)

	strategy := spec.Strategy
	if strategy == nil {
		strategy = OpaqueToken()
	}
	built, err := strategy.Build(StrategyDeps{
		Subject: subject,
		Store:   runtime.store,
		Source:  runtime.source,
		Grants:  runtime.grants,
		Config:  runtime.config,
		Logger:  runtime.logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("access: building the strategy for subject %q: %w", spec.Type, err)
	}

	// Registered before the use cases are assembled, and keyed by this subject:
	// what closes a session has to reach the strategy that issued it, and a
	// strategy that verifies without reading the row has no other way to find
	// out. See [[D-072]].
	runtime.revocations.register(spec.Type, built.Revocations)

	dependencies := newDeps(
		runtime.store, runtime.grants, runtime.hasher, runtime.config, runtime.logger, runtime.revocations)

	var signUp *SignUpUseCase[P]
	if spec.Registrar != nil {
		signUp = NewSignUp(dependencies, subject, built.Issuer, spec.Registrar)
	}

	mounted := &MountedSubject{
		subject:       subject,
		prefix:        spec.Prefix,
		issuer:        built.Issuer,
		authenticator: built.Authenticator,
		refresher:     built.Refresher,
		endpoints:     newEndpoints(dependencies, subject, built.Issuer, built.Refresher),
		registers:     signUp != nil,
	}
	runtime.subjects = append(runtime.subjects, mounted)
	return mounted, signUp, nil
}

// Subjects answers everything registered, in registration order.
func (this *Runtime) Subjects() []*MountedSubject { return this.subjects }

// Grants answers the resolver, which exists once every subject is registered.
func (this *Runtime) Grants() *GrantsService { return this.grants }

// AdminGuard is the middleware for routes that are not under any subject's
// prefix — the roles, permissions and grants endpoints, which any registered
// caller may reach and a permission decides the rest.
//
// A chain over the *declared* strategies and not over every format this library
// can verify: a deployment that issues one kind of token runs one verifier, and
// adding a second is a decision somebody made in a SubjectSpec.
//
// The options are [MountedSubject.Guard]'s: whatever says where the credential
// is read from has to be said in both places, or the routes under a prefix and
// the routes above it disagree about what a caller presented.
func (this *Runtime) AdminGuard(options ...auth.Option) *auth.Guard {
	authenticators := make([]auth.Authenticator, 0, len(this.subjects))
	for _, mounted := range this.subjects {
		authenticators = append(authenticators, mounted.authenticator)
	}
	return auth.NewGuard(auth.Chain(authenticators...), append([]auth.Option{auth.Optional()}, options...)...)
}

// Sync folds every declaration into the tables. It runs once, before the server
// accepts a request: a policy evaluated against a half-populated permissions
// table refuses a caller who does hold the permission, and the symptom appears
// once, on the first request after a deploy.
func (this *Runtime) Sync(ctx context.Context) error {
	return Sync(ctx, this.store, this.declared, this.logger)
}
