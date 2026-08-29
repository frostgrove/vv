// Package access answers two questions and refuses to learn a third: who is
// this caller, and may they do this.
//
// It knows nothing about users. A caller is a [SubjectRef] — a type and an id —
// and the store behind that type is a [Directory] the application implements
// and registers. That is the same shape Laravel gives model_has_roles, and it
// is why adding a service-account sign-in later touches no table and no rule in
// here.
//
// # What this module is not
//
// Three things are deliberately absent, and each one is the application's:
//
//   - **No routes.** Nothing here mounts a handler or names a transport. What a
//     sign-up form asks for, whether one is reachable at all, and what a
//     response body looks like are product decisions.
//   - **No account creation.** [Directory] only reads. An application writes
//     its own row, with its own columns, and hands the finished [SubjectRef] to
//     credentials.EnrollUseCase.
//   - **No identifier normalisation.** An address is folded, a Google `sub` is
//     not, and only whoever issues the identifier knows which. Stored and
//     compared verbatim.
//
// Together those are why no subject type, role slug or route is spelled
// anywhere in this module.
//
// # What crosses the boundary
//
// An application's module imports this package for four things:
//
//   - [auth.Permission] constants of its own, declared in a [ModuleGrants]
//     value and handed to [Sync] so the permissions table can be seeded;
//   - [PrincipalFrom] to read the caller off a context;
//   - the security policies it builds out of those permissions;
//   - [Directory], if it owns a kind of identity.
//
// # Wiring
//
// Plain constructors, so a consumer's container is its own business — fx, wire,
// dig or a func main that calls them in order:
//
//	directories := access.MustDirectories(userDirectory)
//	store := access.NewStore(source)
//	grants := access.NewGrants(store, directories)
//	guard := access.NewGuard(access.NewAuthenticator(store, grants, cfg, logger))
//	deps := credentials.New(store, grants, access.NewHasher(), cfg, logger)
//
// and [Sync] once at start-up, before the server accepts anything.
package access

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"github.com/google/uuid"
)

// A SubjectType names a kind of caller. It is the morph key: "user" today, and
// whatever else grows an identity later.
type SubjectType string

// A SubjectRef identifies one caller, whatever kind it is.
type SubjectRef struct {
	Type SubjectType `json:"type"`
	ID   uuid.UUID   `json:"id"`
}

// Zero reports the unset reference, which is never a valid caller.
func (this SubjectRef) Zero() bool { return this.Type == "" || this.ID == uuid.Nil }

func (this SubjectRef) String() string { return string(this.Type) + ":" + this.ID.String() }

// A Directory is the identity store behind one [SubjectType].
//
// This is the whole of what access needs from the application that owns
// accounts, and every method is about an identity rather than about a person:
// no name, no address, no profile. The application implements it, registers it
// by the type it declares, and access never imports the package that provided
// it.
//
// Every method here *reads*. Nothing on this port creates an identity, and that
// is deliberate: an account is the application's row, with the application's
// columns — a company id, a phone number, a locale this module has no opinion
// about. Creating one through a port thin enough for this module to describe
// would mean either a field list that fits nobody or a map[string]any that
// type-checks nothing. So the application writes its own row and hands the
// finished [SubjectRef] to credentials.EnrollUseCase instead.
type Directory interface {
	// SubjectType is the morph key this directory answers for.
	SubjectType() SubjectType

	// Active reports whether this identity may still authenticate. It is read on
	// every authenticated request, which is what makes deactivation bite on the
	// next call rather than on the next sign-in.
	Active(ctx context.Context, id uuid.UUID) (bool, error)

	// Describe answers what a client is shown about itself. Access renders it
	// as-is and interprets nothing.
	Describe(ctx context.Context, id uuid.UUID) (Profile, error)

	// Touch records a successful sign-in. A directory with nothing to record
	// returns nil.
	Touch(ctx context.Context, id uuid.UUID) error
}

// A Profile is what a directory says about an identity, for rendering only.
type Profile struct {
	DisplayName string         `json:"displayName"`
	Identifier  string         `json:"identifier"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

// No subject type is named in this package, and that is the point. A constant
// spelling "user" here would be access knowing the name of something it must
// not depend on — the coupling [Directory] exists to remove. A subject type is
// a string that arrives from the caller and is only ever accepted because a
// registered directory claims it: see [SubjectParam] for one off a path, and
// every command in this package for one an application passes in.

// ProviderPassword is the provider a stored secret is verified under. It is a
// column value and not a closed set: an application that adds "google" adds
// rows, not a constant here.
const ProviderPassword = "password"

// The permissions this context owns. A module declares its own beside its own
// code — that is [ModuleGrants] — and never adds to this list.
const (
	PermRoleRead    auth.Permission = "role.read"
	PermRoleWrite   auth.Permission = "role.write"
	PermRoleDelete  auth.Permission = "role.delete"
	PermGrantRead   auth.Permission = "grant.read"
	PermGrantWrite  auth.Permission = "grant.write"
	PermSessionRead auth.Permission = "session.read"
	PermSessionKill auth.Permission = "session.revoke"
	// PermCredentialWrite is setting somebody else's password. It is separate
	// from PermGrantWrite because they are different powers: granting a role
	// gives an account abilities, and setting its password lets you *be* it.
	PermCredentialWrite auth.Permission = "credential.write"
)

// CodeUnknownSubjectType reports a subject type nothing serves. It is this
// module's own code because errs has none for it, and a client that got
// `bad_query` back would have no way to tell a malformed filter from a path
// parameter it spelled wrong.
const CodeUnknownSubjectType errs.Code = "unknown_subject_type"

// RoleAdmin is the one role [Sync] always seeds. It is marked system, and holds
// every permission every module declares — including permissions declared after
// it was seeded, because the sync runs at every start.
const RoleAdmin auth.Role = "admin"

// ModuleGrants is how a bounded context declares what it owns.
//
// An application collects one of these per module and hands the slice to
// [Sync], which folds every contribution into the permissions table and into
// the system roles at start-up. Declaring a permission beside the code that
// enforces it is the point: a central list is edited by whoever adds a feature
// and reviewed by nobody, and it goes stale in the direction that grants too
// much.
type ModuleGrants struct {
	// Module names the contributor, and is stored on each permission row.
	Module string
	// Permissions is the closed set this module enforces.
	Permissions []PermissionDef
	// Roles are the system roles this module wants to exist, and what it grants
	// them. A role named by two modules gets the union of what both granted; a
	// role nobody names is not created.
	Roles map[auth.Role][]auth.Permission
}

// A PermissionDef is one permission before it is a row.
type PermissionDef struct {
	Code auth.Permission
	Name string
}

// Grants is what a module asks when it needs a caller's effective rights
// outside a request — a background job acting as somebody, a CLI.
//
// Inside a request, read the principal off the context with [PrincipalFrom]
// instead: it is already resolved and asking again costs two queries.
type Grants interface {
	For(ctx context.Context, ref SubjectRef) (*Principal, error)
}

// A Principal is one authenticated caller, resolved from stored rows on every
// request. It implements vv's auth.Principal, which is what lets
// crud/decorators/security policies be written against it with no adapter.
//
// Roles and permissions come from the database and never from the token. That
// is the one invariant worth stating twice: a demotion takes effect on the next
// request, not when a token happens to expire.
type Principal struct {
	Ref         SubjectRef
	SessionID   uuid.UUID
	Roles       []auth.Role
	Permissions []auth.Permission
	Profile     Profile
}

// Subject implements auth.Principal. It is the bare id rather than
// "type:id" so that security.ScopeSubject compares against an id column.
func (this *Principal) Subject() string { return this.Ref.ID.String() }

// In implements auth.Principal.
func (this *Principal) In(role auth.Role) bool {
	for _, has := range this.Roles {
		if has == role {
			return true
		}
	}
	return false
}

// Has implements auth.Principal.
func (this *Principal) Has(permission auth.Permission) bool {
	for _, has := range this.Permissions {
		if has == permission {
			return true
		}
	}
	return false
}

// Attr implements auth.Principal.
//
// SubjectID is a uuid.UUID and not its text, because security.ScopeAttr turns
// whatever comes out of here into a bind parameter: a string against a uuid
// column is a type error in PostgreSQL, and the query it produces fails at run
// time rather than at wiring time.
func (this *Principal) Attr(name string) (any, bool) {
	switch name {
	case AttrSubjectID:
		return this.Ref.ID, true
	case AttrSubjectType:
		return string(this.Ref.Type), true
	case AttrSessionID:
		return this.SessionID, true
	default:
		return nil, false
	}
}

// The claim names [Principal.Attr] answers, and what security.ScopeAttr is
// given.
const (
	AttrSubjectID   = "subject_id"
	AttrSubjectType = "subject_type"
	AttrSessionID   = "session_id"
)

// PrincipalFrom reads the caller off a context. It is auth.PrincipalFrom
// narrowed to this application's own type, for the handlers that need the
// subject reference rather than the four interface methods.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return nil, false
	}
	accessPrincipal, ok := principal.(*Principal)
	return accessPrincipal, ok
}

// RequirePrincipal is [PrincipalFrom] for a caller that has no answer without
// one. The error it returns renders as a 401.
func RequirePrincipal(ctx context.Context) (*Principal, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, auth.Unauthenticated("no principal in context")
	}
	return principal, nil
}

// Require is [RequirePrincipal] plus a permission check, and it is what a
// hand-written endpoint calls where a CRUD resource would have a security.Gate.
//
// The two failures are different answers on purpose: an absent caller is a 401
// — "authenticate" — and a present one that is not allowed is a 403 —
// "authenticating again will not help". Collapsing them into one status makes a
// client with a valid session retry the login it does not need.
func Require(ctx context.Context, permissions ...auth.Permission) (*Principal, error) {
	principal, err := RequirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !auth.HasAll(principal, permissions...) {
		return nil, errs.Forbidden().
			Code(errs.CodeForbidden).
			Message("this account does not hold the permission this endpoint needs").
			Fault()
	}
	return principal, nil
}

// A DirectoryLookup answers which subject types this application serves.
// [GrantsService] is the implementation; the interface is here so a handler
// takes the narrow thing rather than the whole resolver.
type DirectoryLookup interface {
	Directory(subjectType SubjectType) (Directory, bool)
}

// SubjectParam reads the two path parameters that name a caller.
//
// The type is checked against the registered directories rather than taken as
// given. Without that, a grant addressed to a subject type nothing serves is a
// row pointing at nothing: it inserts, it answers 204, and it grants nobody
// anything — a bug whose only symptom is that the permission somebody was
// promised never arrived.
func SubjectParam(rawType, rawID string, directories DirectoryLookup) (SubjectRef, error) {
	subjectID, err := uuid.Parse(rawID)
	if err != nil {
		return SubjectRef{}, port.BadRequestAs(errs.CodeInvalidID, port.At("id"),
			"%q is not a subject id", rawID)
	}
	subjectType := SubjectType(rawType)
	if _, served := directories.Directory(subjectType); !served {
		return SubjectRef{}, port.BadRequestAs(CodeUnknownSubjectType, port.At("type"),
			"%q is not a subject type this application serves", rawType)
	}
	return SubjectRef{Type: subjectType, ID: subjectID}, nil
}

// ErrNoRefresh reports a refresh asked of a strategy that does not rotate. It
// is reachable only by wiring, since the route is not mounted without one.
var ErrNoRefresh = errors.New("access: this subject's strategy does not rotate")

// BadSessionID is the refusal for a path parameter that is not a uuid.
//
// A 400 and not a 404: the client sent something that cannot be a session id at
// all, which is a different fix from asking for one that does not exist.
func BadSessionID(raw string) error {
	return port.BadRequestAs(errs.CodeInvalidID, port.At("id"), "%q is not a session id", raw)
}

// An identifier is stored and compared exactly as it arrives, and normalising
// one is the caller's job.
//
// This module cannot do it correctly. What an identifier means belongs to
// whatever issues it: an email address is case-insensitive in its domain and
// arguably not in its local part, a Google `sub` is an opaque digit string that
// must never be folded, a SAML NameID is whatever the IdP says. A lowercasing
// call in here is right for one of those and silently wrong for the rest — and
// "silently" is the operative word, because the failure is an account that can
// be signed into twice under two spellings.
//
// The application applies its rule once, before it calls in, and then both
// sides of the comparison are the same string: whatever it stores through
// [credentials.EnrollUseCase] is whatever it looks up through
// [credentials.LoginUseCase].
