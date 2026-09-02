package access

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/faults"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/google/uuid"
)

type Store struct {
	source             crud.Source
	Permissions        *PermissionRepo
	Roles              *RoleRepo
	RolePermissions    *RolePermissionRepo
	SubjectRoles       *SubjectRoleRepo
	SubjectPermissions *SubjectPermissionRepo
	DefaultRoles       *SubjectDefaultRoleRepo
	Credentials        *CredentialRepo
	Sessions           *SessionRepo
}

func NewStore(src crud.Source) *Store {
	return &Store{
		source:             src,
		Permissions:        crud.Decorate(NewPermissionRepository(src), faults.Enrich[Permission, uuid.UUID]()),
		Roles:              crud.Decorate(NewRoleRepository(src), faults.Enrich[Role, uuid.UUID]()),
		RolePermissions:    crud.Decorate(NewRolePermissionRepository(src), faults.Enrich[RolePermission, uuid.UUID]()),
		SubjectRoles:       crud.Decorate(NewSubjectRoleRepository(src), faults.Enrich[SubjectRole, uuid.UUID]()),
		SubjectPermissions: crud.Decorate(NewSubjectPermissionRepository(src), faults.Enrich[SubjectPermission, uuid.UUID]()),
		DefaultRoles:       crud.Decorate(NewSubjectDefaultRoleRepository(src), faults.Enrich[SubjectDefaultRole, uuid.UUID]()),
		Credentials:        crud.Decorate(NewCredentialRepository(src), faults.Enrich[Credential, uuid.UUID]()),
		Sessions:           crud.Decorate(NewSessionRepository(src), faults.Enrich[Session, uuid.UUID]()),
	}
}

func (this *Store) Tx(ctx context.Context, fn func(context.Context) error) error {
	return this.Sessions.Tx(ctx, fn)
}

func (this *Store) OwnedTx(ctx context.Context, fn func(context.Context) error) error {
	return crud.InNewTx(ctx, this.source, fn)
}

func OfSubject(ref SubjectRef) crud.Option {
	return crud.Where(crud.And(
		crud.Eq("SubjectType", string(ref.Type)),
		crud.Eq("SubjectID", ref.ID),
	))
}

func ofSubject(ref SubjectRef) crud.Option { return OfSubject(ref) }

const (
	ReasonSignedOut           = "signed out"
	ReasonSignedOutEverywhere = "signed out everywhere"
	ReasonPasswordChanged     = "password changed"
	ReasonRevokedByAdmin      = "revoked by an administrator"

	ReasonRefreshReplayed = "a spent refresh credential was replayed"
)

func (this *Store) PermissionByCode(ctx context.Context, code auth.Permission) (Permission, error) {
	return this.Permissions.First(ctx, specs.As(Permission_.Code.Eq(string(code))))
}

func (this *Store) RoleBySlug(ctx context.Context, slug auth.Role) (Role, error) {
	return this.Roles.First(ctx, specs.As(Role_.Slug.Eq(string(slug))))
}

func (this *Store) DefaultRoleRow(ctx context.Context, subjectType SubjectType) (SubjectDefaultRole, error) {
	return this.DefaultRoles.First(ctx,
		specs.As(SubjectDefaultRole_.SubjectType.Eq(string(subjectType))),
		crud.Preload(SubjectDefaultRole_.Role.Path()),
	)
}

func (this *Store) CredentialFor(ctx context.Context, subjectType SubjectType, provider, identifier string) (Credential, error) {
	return this.Credentials.First(ctx,
		specs.As(specs.AllOf(
			Credential_.SubjectType.Eq(string(subjectType)),
			Credential_.Provider.Eq(provider),
			Credential_.Identifier.Eq(identifier),
		)),
		crud.PrimaryOnly(),
	)
}

func (this *Store) LockCredentialFor(
	ctx context.Context,
	subjectType SubjectType,
	provider, identifier string,
) (Credential, error) {
	candidate, err := this.CredentialFor(ctx, subjectType, provider, identifier)
	if err != nil {
		if errors.Is(err, crud.ErrNotFound) {
			_, paddingErr := this.CredentialFor(ctx, subjectType, provider, identifier)
			if paddingErr != nil && !errors.Is(paddingErr, crud.ErrNotFound) {
				return Credential{}, paddingErr
			}

			if paddingErr = this.padCredentialLockMiss(ctx); paddingErr != nil {
				return Credential{}, paddingErr
			}
		}
		return Credential{}, err
	}
	ref := SubjectRef{Type: SubjectType(candidate.SubjectType), ID: candidate.SubjectID}
	locked, err := this.LockCredentialsOf(ctx, ref, provider)
	if err != nil {
		return Credential{}, err
	}
	if len(locked) == 0 {
		if err := this.padCredentialLockMiss(ctx); err != nil {
			return Credential{}, err
		}
	}
	for _, current := range locked {
		if current.ID == candidate.ID && current.Identifier == identifier {
			return current, nil
		}
	}
	return Credential{}, crud.ErrNotFound
}

func (this *Store) padCredentialLockMiss(ctx context.Context) error {
	_, err := this.Credentials.First(ctx,
		specs.As(Credential_.ID.Eq(uuid.Nil)),
		crud.PrimaryOnly(),
	)
	if errors.Is(err, crud.ErrNotFound) {
		return nil
	}
	return err
}

func (this *Store) LockPasswordCredentials(ctx context.Context, ref SubjectRef) ([]Credential, error) {
	return this.LockCredentialsOf(ctx, ref, ProviderPassword)
}

func (this *Store) LockCredentialsOf(ctx context.Context, ref SubjectRef, provider string) ([]Credential, error) {
	discovered, err := this.Credentials.GetAll(ctx,
		ofSubject(ref),
		specs.As(Credential_.Provider.Eq(provider)),
		crud.Select(Credential_.ID.Name()),
		crud.PrimaryOnly(),
	)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(discovered))
	for _, credential := range discovered {
		ids = append(ids, credential.ID)
	}
	sort.Slice(ids, func(i, j int) bool {
		return bytes.Compare(ids[i][:], ids[j][:]) < 0
	})

	locked := make([]Credential, 0, len(ids))
	for _, id := range ids {
		credential, err := this.Credentials.First(ctx,
			specs.As(Credential_.ID.Eq(id)),
			crud.ForUpdate(),
			crud.PrimaryOnly(),
		)
		if err != nil {
			if errors.Is(err, crud.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if credential.SubjectType != string(ref.Type) || credential.SubjectID != ref.ID ||
			credential.Provider != provider {
			continue
		}
		locked = append(locked, credential)
	}
	return locked, nil
}

func (this *Store) FenceSessionIssue(ctx context.Context, credential Credential) error {
	secret := credential.SecretHash
	_, err := this.Credentials.UpdateAll(ctx,
		CredentialUpdate{SecretHash: &secret},
		specs.As(specs.AllOf(
			Credential_.ID.Eq(credential.ID),
			Credential_.SecretHash.Eq(credential.SecretHash),
		)),
		crud.PrimaryOnly(),
	)
	return err
}

func (this *Store) SessionByToken(ctx context.Context, digest string) (Session, error) {
	return this.Sessions.First(ctx, specs.As(Session_.TokenHash.Eq(digest)))
}

func (this *Store) LiveSessionsOf(
	ctx context.Context,
	ref SubjectRef,
	now time.Time,
	idle time.Duration,
) ([]Session, error) {
	options := []crud.Option{
		ofSubject(ref),
		specs.As(Session_.RevokedAt.IsNull()),
		specs.As(Session_.ExpiresAt.Gt(now)),
	}
	if idle > 0 {
		options = append(options, specs.As(Session_.LastUsedAt.Gt(now.Add(-idle))))
	}
	return this.Sessions.GetAll(ctx, append(options, crud.OrderBy(Session_.LastUsedAt.Desc()))...)
}

func (this *Store) SessionsRevokedSince(ctx context.Context, since, now time.Time) ([]Session, error) {
	return this.Sessions.GetAll(ctx,
		specs.As(Session_.RevokedAt.Gte(since)),
		specs.As(Session_.ExpiresAt.Gt(now)),
		crud.Select(Session_.ID.Name(), Session_.SubjectType.Name()),
		crud.OrderBy(Session_.RevokedAt.Asc()),
	)
}

func ambiguousPassword(ref SubjectRef, found int) error {
	return fmt.Errorf(
		"access: %s holds %d password credentials; a password change would leave the others signing in",
		ref, found)
}

func IsNotFound(err error) bool { return errors.Is(err, crud.ErrNotFound) }

func notFound(err error) bool { return IsNotFound(err) }
