package access

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/faults"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/google/uuid"
)

// A Store is every repository this context owns.
//
// One value rather than seven constructor parameters: a use case that took
// seven would take an eighth the first time a table arrived, and every call
// site and every test would change with it. Nothing here decides anything —
// that is [П4] — and the named queries below are queries, not rules.
type Store struct {
	Permissions        *PermissionRepo
	Roles              *RoleRepo
	RolePermissions    *RolePermissionRepo
	SubjectRoles       *SubjectRoleRepo
	SubjectPermissions *SubjectPermissionRepo
	Credentials        *CredentialRepo
	Sessions           *SessionRepo
}

// NewStore binds every repository to the one source the application opened.
//
// faults.Enrich is on all of them, which is what turns a unique-index refusal
// into a violation naming the model field rather than into a driver string with
// the constraint name in it. Two of these tables exist to be collided with —
// credentials.identifier and roles.slug — and a registration that answers
// "duplicate key value violates unique constraint" is telling a stranger the
// shape of the schema.
func NewStore(src crud.Source) *Store {
	return &Store{
		Permissions:        crud.Decorate(NewPermissionRepository(src), faults.Enrich[Permission, uuid.UUID]()),
		Roles:              crud.Decorate(NewRoleRepository(src), faults.Enrich[Role, uuid.UUID]()),
		RolePermissions:    crud.Decorate(NewRolePermissionRepository(src), faults.Enrich[RolePermission, uuid.UUID]()),
		SubjectRoles:       crud.Decorate(NewSubjectRoleRepository(src), faults.Enrich[SubjectRole, uuid.UUID]()),
		SubjectPermissions: crud.Decorate(NewSubjectPermissionRepository(src), faults.Enrich[SubjectPermission, uuid.UUID]()),
		Credentials:        crud.Decorate(NewCredentialRepository(src), faults.Enrich[Credential, uuid.UUID]()),
		Sessions:           crud.Decorate(NewSessionRepository(src), faults.Enrich[Session, uuid.UUID]()),
	}
}

// Tx runs fn against every repository in one transaction. Any of them would do
// — they share a source — and naming one at each call site would read like the
// transaction belonged to that table.
func (this *Store) Tx(ctx context.Context, fn func(context.Context) error) error {
	return this.Sessions.Tx(ctx, fn)
}

// OfSubject narrows to the rows that belong to one subject. Every table in this
// context that points at a caller carries the same two columns, so the
// predicate is written once — a hand-written pair that names only SubjectID
// matches another type's rows, and the query that does it looks correct.
func OfSubject(ref SubjectRef) crud.Option {
	return crud.Where(crud.And(
		crud.Eq("SubjectType", string(ref.Type)),
		crud.Eq("SubjectID", ref.ID),
	))
}

func ofSubject(ref SubjectRef) crud.Option { return OfSubject(ref) }

// The reasons a session row records. They are read by a person looking at a
// row, so they are words rather than codes.
const (
	ReasonSignedOut           = "signed out"
	ReasonSignedOutEverywhere = "signed out everywhere"
	ReasonPasswordChanged     = "password changed"
	ReasonRevokedByAdmin      = "revoked by an administrator"
	// ReasonRefreshReplayed marks a session closed because a refresh credential
	// was presented after it had been spent. It is the one reason worth
	// alerting on: it is what a stolen credential looks like.
	ReasonRefreshReplayed = "a spent refresh credential was replayed"
)

// PermissionByCode finds one declared permission. A code that is not in the
// table is not a permission this application enforces, and granting it would
// produce a row nothing ever reads.
func (this *Store) PermissionByCode(ctx context.Context, code auth.Permission) (Permission, error) {
	return this.Permissions.First(ctx, specs.As(Permission_.Code.Eq(string(code))))
}

// RoleBySlug finds one role by the identifier a caller spells.
func (this *Store) RoleBySlug(ctx context.Context, slug auth.Role) (Role, error) {
	return this.Roles.First(ctx, specs.As(Role_.Slug.Eq(string(slug))))
}

// CredentialFor finds the credential a caller is signing in with.
//
// The subject type is part of the predicate and not a check afterwards. An
// identifier is unique within a type, so without it this query has more than
// one answer and would return whichever row the engine reached first — which is
// a sign-in as somebody in a domain the caller never asked for.
//
// The identifier is compared verbatim. Whatever rule the application folds it
// with has already been applied on both sides; see [Subject.Normalize].
func (this *Store) CredentialFor(ctx context.Context, subjectType SubjectType, provider, identifier string) (Credential, error) {
	return this.Credentials.First(ctx, specs.As(specs.AllOf(
		Credential_.SubjectType.Eq(string(subjectType)),
		Credential_.Provider.Eq(provider),
		Credential_.Identifier.Eq(identifier),
	)))
}

// SessionByToken finds a session by the digest of the presented token.
//
// It answers crud.ErrNotFound for a token nobody minted, and the caller turns
// that into the same refusal an expired session gets: telling the two apart
// tells a stranger which of their guesses was once real.
func (this *Store) SessionByToken(ctx context.Context, digest string) (Session, error) {
	return this.Sessions.First(ctx, specs.As(Session_.TokenHash.Eq(digest)))
}

// LiveSessionsOf lists the sessions a subject could still use, newest first.
func (this *Store) LiveSessionsOf(ctx context.Context, ref SubjectRef) ([]Session, error) {
	return this.Sessions.GetAll(ctx,
		ofSubject(ref),
		specs.As(Session_.RevokedAt.IsNull()),
		crud.OrderBy(Session_.LastUsedAt.Desc()),
	)
}

// IsNotFound reports the sentinel every First above answers with when nothing
// matched, so a caller branches on it without importing crud. A sign-in path
// needs it: "no such credential" is a step in a flow there, not a failure.
func IsNotFound(err error) bool { return errors.Is(err, crud.ErrNotFound) }

func notFound(err error) bool { return IsNotFound(err) }
