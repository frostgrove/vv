package access

import (
	"bytes"
	"context"
	"errors"
	"sort"

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

// Tx runs fn against every repository in one transaction. Any of them would do
// — they share a source — and naming one at each call site would read like the
// transaction belonged to that table.
func (this *Store) Tx(ctx context.Context, fn func(context.Context) error) error {
	return this.Sessions.Tx(ctx, fn)
}

// OwnedTx runs a security protocol in a transaction whose commit boundary this
// store owns. It deliberately does not join an ambient executor: a login must
// not return a token before somebody else's transaction commits, and a session
// invalidator must not announce a revocation that the ambient owner can still
// roll back. A valid ambient pool or transaction is ignored for this database;
// an invalid executor declaration still fails closed through [crud.InNewTx].
//
// Application use cases that are intentionally composable with an account
// write use [Store.Tx] instead. SignUp, Login and every session-closing use
// case use OwnedTx, commit their access state, and only then return/announce.
func (this *Store) OwnedTx(ctx context.Context, fn func(context.Context) error) error {
	return crud.InNewTx(ctx, this.source, fn)
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

// DefaultRoleRow finds the default-role binding for one kind of caller, with
// the role it points at loaded.
//
// It answers crud.ErrNotFound when the type has no default, which is a state a
// deployment is allowed to be in: an invitation-only product grants nothing on
// sign-up and an administrator does the granting. The caller branches on
// [IsNotFound] rather than getting a zero Role back, because a zero Role is
// indistinguishable from one whose slug is empty.
func (this *Store) DefaultRoleRow(ctx context.Context, subjectType SubjectType) (SubjectDefaultRole, error) {
	return this.DefaultRoles.First(ctx,
		specs.As(SubjectDefaultRole_.SubjectType.Eq(string(subjectType))),
		crud.Preload(SubjectDefaultRole_.Role.Path()),
	)
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
	return this.Credentials.First(ctx,
		specs.As(specs.AllOf(
			Credential_.SubjectType.Eq(string(subjectType)),
			Credential_.Provider.Eq(provider),
			Credential_.Identifier.Eq(identifier),
		)),
		crud.PrimaryOnly(),
	)
}

// LockCredentialFor finds a credential, then re-reads its subject's credentials
// through the canonical authentication lock.
//
// It must be called inside a transaction; Login uses [Store.OwnedTx]. The first
// read deliberately takes no row lock: its only job is to discover the subject
// behind an identifier. Locking through that unique secondary index and then
// asking for the subject's other rows would invert the order used by password
// reset on InnoDB. The canonical lock discovers the subject's primary keys,
// sorts them, and locks each exact PK with a current read. Only those results
// are returned. A reset that changed the identifier or secret between candidate
// discovery and its locking read is observed here rather than authenticated
// from the candidate snapshot.
//
// Every operation that can create a password session or invalidate all of a
// subject's sessions follows the same order:
//
//   - password credential rows, ascending primary key;
//   - session rows or inserts;
//   - transaction commit.
//
// A login locks one subject's complete password set and never another set.
// Subject-wide operations do the same. Consequently two such operations either
// wait or take identical row order; none can hold a later credential while
// asking for an earlier one.
func (this *Store) LockCredentialFor(
	ctx context.Context,
	subjectType SubjectType,
	provider, identifier string,
) (Credential, error) {
	candidate, err := this.CredentialFor(ctx, subjectType, provider, identifier)
	if err != nil {
		if errors.Is(err, crud.ErrNotFound) {
			// Keep an unknown identifier at the same database round trips as the
			// ordinary one-credential refusal before Login's dummy Argon2
			// verification, without taking a gap lock for a made-up subject.
			// Repeating the primary read is intentionally non-locking: an insert
			// between the two may make the padding find a row, but this login
			// linearised at the first miss and must still refuse it.
			_, paddingErr := this.CredentialFor(ctx, subjectType, provider, identifier)
			if paddingErr != nil && !errors.Is(paddingErr, crud.ErrNotFound) {
				return Credential{}, paddingErr
			}
			// A normal one-credential subject performs candidate discovery, ID
			// discovery, then one exact-PK locking read. Match that third round trip
			// without a FOR UPDATE miss (which would take an InnoDB gap lock).
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
		// A candidate deleted or moved before ID discovery otherwise costs two
		// reads while an ordinary miss/refusal costs three. Keep the enumeration
		// guarantee even across that rare concurrent transition.
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

// LockPasswordCredentials serialises a subject's password authentication and
// subject-wide invalidation operations. See [Store.LockCredentialFor] for the
// lock order and the reason this is a repository operation rather than a lock
// assembled in a use case.
func (this *Store) LockPasswordCredentials(ctx context.Context, ref SubjectRef) ([]Credential, error) {
	return this.LockCredentialsOf(ctx, ref, ProviderPassword)
}

// LockCredentialsOf is the SQL-shaped half of the authentication serialisation
// protocol. Keep it here: use cases decide when the protocol is required, while
// repositories alone decide how rows are selected and locked.
//
// The first query discovers primary keys without taking a lock. Sorting a
// secondary-index scan does not force InnoDB to acquire PRIMARY records in that
// order, so the method then locks each exact primary key in byte order, one
// statement at a time. Every participant therefore acquires the same physical
// records in the same order rather than trusting an ORDER BY execution plan.
// Each locking read is current and its row is revalidated: a concurrent change
// may move or delete a discovered credential. A credential created after the
// discovery overlaps this operation and linearises after it; it cannot have
// authenticated before it existed.
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

// FenceSessionIssue makes a successful password authentication conflict with
// a stale PostgreSQL REPEATABLE READ/SERIALIZABLE invalidation transaction.
//
// It must be called in the same transaction, after [Store.LockCredentialFor]
// returned credential and before the session insert. UpdateAll is deliberate:
// unlike Update, it never diffs an equal value away. PostgreSQL therefore
// creates a new tuple version even though secret_hash is unchanged. An
// invalidator whose older snapshot was established before this login committed
// cannot then lock the credential and successfully miss the new session; the
// server refuses it with a retryable serialisation failure instead.
//
// The ID and hash predicates pin the write to the exact locked value. MySQL and
// MariaDB may report zero affected rows for this equal-value update. That is
// success: their correctness comes from the preceding SELECT FOR UPDATE being
// a current read, not from RowsAffected. The canonical PostgreSQL migration's
// credentials_set_updated_at trigger moves UpdatedAt, intentionally making it
// the last successful password-use/change time as well as a mutation time.
// Other schemas need an equivalent trigger if they want that timestamp
// semantic; the fence itself does not depend on one.
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
