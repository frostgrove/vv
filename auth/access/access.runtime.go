package access

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/internal/nilvalue"
)

type RuntimeSpec struct {
	Source crud.Source
	Config Config
	Logger *slog.Logger

	Hasher Hasher

	Protection Protection
}

type Runtime struct {
	store  *Store
	source crud.Source
	grants *GrantsService
	hasher Hasher
	config Config
	logger *slog.Logger

	protection Protection

	subjects  []*MountedSubject
	declared  []ModuleGrants
	directory []Directory

	revocations *revocationSinks
}

func New(spec RuntimeSpec) (*Runtime, error) {
	if spec.Source == nil {
		return nil, fmt.Errorf("access: a runtime needs a crud.Source")
	}
	if spec.Logger == nil {
		return nil, fmt.Errorf("access: a runtime needs a logger; the library never writes to a process-wide one")
	}
	hasher := spec.Hasher
	if hasher == nil {
		hasher = Bulkhead(NewHasher())
	}
	return &Runtime{
		store:       NewStore(spec.Source),
		source:      spec.Source,
		hasher:      hasher,
		config:      spec.Config,
		logger:      spec.Logger,
		protection:  spec.Protection,
		declared:    []ModuleGrants{OwnGrants()},
		revocations: newRevocationSinks(),
	}, nil
}

func (this *Runtime) Store() *Store { return this.store }

func (this *Runtime) Config() Config { return this.config }

func (this *Runtime) Seeder() *Seeder { return NewSeeder(this.store, this.logger) }

func (this *Runtime) SetPassword() *SetPasswordUseCase {
	return NewSetPassword(this.deps())
}

func (this *Runtime) ReannounceRevocations(ctx context.Context, since time.Time) error {
	return this.deps().ReannounceRevocations(ctx, since)
}

func (this *Runtime) deps() *Deps {
	return newDeps(
		this.store, this.grants, this.hasher, this.config, this.logger, this.revocations, this.protection)
}

func (this *Runtime) Declare(grants ...ModuleGrants) {
	this.declared = append(this.declared, grants...)
}

type SubjectSpec[P any] struct {
	Type SubjectType

	Prefix string

	Directory Directory

	Normalize func(identifier string) string

	Registrar Registrar[P]

	Strategy Strategy
}

type MountedSubject struct {
	subject Subject
	prefix  string

	issuer        SessionIssuer
	authenticator auth.Authenticator
	refresher     SessionRefresher
	endpoints     Endpoints
	registers     bool
}

func (this *MountedSubject) Subject() Subject { return this.subject }

func (this *MountedSubject) Prefix() string { return this.prefix }

func (this *MountedSubject) Guard(options ...auth.Option) *auth.Guard {
	return auth.NewGuard(this.authenticator, append([]auth.Option{auth.Optional()}, options...)...)
}

func (this *MountedSubject) Issuer() SessionIssuer { return this.issuer }

func (this *MountedSubject) Authenticator() auth.Authenticator { return this.authenticator }

func (this *MountedSubject) Endpoints() Endpoints { return this.endpoints }

func (this *MountedSubject) Registers() bool { return this.registers }

func (this *MountedSubject) Refreshes() bool { return this.refresher != nil }

func Mount[P any](runtime *Runtime, spec SubjectSpec[P]) (*MountedSubject, *SignUpUseCase[P], error) {
	if runtime == nil {
		return nil, nil, fmt.Errorf("access: mounting a subject on a nil runtime")
	}
	if spec.Type == "" {
		return nil, nil, fmt.Errorf("access: a subject spec needs a type")
	}
	if nilvalue.Is(spec.Directory) {
		return nil, nil, fmt.Errorf("access: subject %q has no directory", spec.Type)
	}
	if spec.Registrar != nil && nilvalue.Is(spec.Registrar) {
		return nil, nil, fmt.Errorf("access: subject %q has a typed-nil registrar; omit it when sign-up is unsupported", spec.Type)
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

	candidateDirectories := make([]Directory, len(runtime.directory)+1)
	copy(candidateDirectories, runtime.directory)
	candidateDirectories[len(runtime.directory)] = spec.Directory
	directories, err := NewDirectories(candidateDirectories...)
	if err != nil {
		return nil, nil, err
	}
	candidateGrants := NewGrants(runtime.store, directories)

	strategy := spec.Strategy
	if strategy == nil {
		strategy = OpaqueToken()
	} else if nilvalue.Is(strategy) {
		return nil, nil, fmt.Errorf("access: subject %q has a typed-nil strategy; omit it to use OpaqueToken", spec.Type)
	}
	built, err := strategy.Build(StrategyDeps{
		Subject: subject,
		Store:   runtime.store,
		Source:  runtime.source,
		Grants:  candidateGrants,
		Config:  runtime.config,
		Logger:  runtime.logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("access: building the strategy for subject %q: %w", spec.Type, err)
	}
	if err := validateIssued(built); err != nil {
		return nil, nil, fmt.Errorf("access: strategy for subject %q: %w", spec.Type, err)
	}

	dependencies := newDeps(
		runtime.store, candidateGrants, runtime.hasher, runtime.config, runtime.logger,
		runtime.revocations, runtime.protection)

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

	runtime.directory = candidateDirectories
	runtime.grants = candidateGrants
	runtime.revocations.register(spec.Type, built.Revocations)
	runtime.subjects = append(runtime.subjects, mounted)
	return mounted, signUp, nil
}

func validateIssued(built Issued) error {
	if nilvalue.Is(built.Issuer) {
		return fmt.Errorf("build returned no usable SessionIssuer")
	}
	if nilvalue.Is(built.Authenticator) {
		return fmt.Errorf("build returned no usable auth.Authenticator")
	}
	if built.Refresher != nil && nilvalue.Is(built.Refresher) {
		return fmt.Errorf("build returned a typed-nil SessionRefresher; return nil when refresh is unsupported")
	}
	if built.Revocations != nil && nilvalue.Is(built.Revocations) {
		return fmt.Errorf("build returned a typed-nil RevocationSink; return nil when revocation notification is unnecessary")
	}
	return nil
}

func (this *Runtime) Subjects() []*MountedSubject { return this.subjects }

func (this *Runtime) Grants() *GrantsService { return this.grants }

func (this *Runtime) AdminGuard(options ...auth.Option) *auth.Guard {
	authenticators := make([]auth.Authenticator, 0, len(this.subjects))
	for _, mounted := range this.subjects {
		authenticators = append(authenticators, mounted.authenticator)
	}
	return auth.NewGuard(auth.Chain(authenticators...), append([]auth.Option{auth.Optional()}, options...)...)
}

func (this *Runtime) Sync(ctx context.Context) error {
	return Sync(ctx, this.store, this.declared, this.logger)
}
