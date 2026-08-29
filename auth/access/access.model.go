package access

//go:generate go run github.com/frostgrove/vv/cmd/vv -types Permission,Role,RolePermission,SubjectRole,SubjectPermission,SubjectDefaultRole,Credential,Session

import (
	"time"

	"github.com/google/uuid"
)

// Nothing in this file names a user, and that is the whole design. A row here
// points at a *subject* — a type and an id — the way Laravel's model_has_roles
// points at a morph. Access can then authenticate and authorize an identity it
// has never heard of, and adding a second kind of caller (a service account, an
// organisation) costs a Directory implementation and no migration here.
//
// The type that owns those identities is on the other side of [Directory].

// A Permission is one thing a caller may do, as a row. The code is what a
// policy is written against — see the constants in access.api.go — and the
// module column records which bounded context declared it, so an orphaned code
// from a module that no longer exists is visible rather than merely inert.
type Permission struct {
	ID        uuid.UUID `db:"id,pk,auto" json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Module    string    `json:"module"`
	CreatedAt time.Time `db:"created_at,generated" json:"createdAt"`
}

// A Role is a named bundle of permissions.
//
// IsSystem marks a role the application seeds and depends on. The CRUD service
// refuses to rename or delete one: a deployment that lost "admin" because
// somebody tidied a list has no way back in.
//
// It is `immutable`, so the generated update DTO does not carry it and no PATCH
// can reach it. Without that, promoting an ordinary role to system is a way to
// make it permanent — the flag is what the two refusals are keyed on, so a
// request able to set it is a request able to opt into them.
type Role struct {
	ID        uuid.UUID `db:"id,pk,auto" json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	IsSystem  bool      `db:"is_system,immutable" json:"isSystem"`
	CreatedAt time.Time `db:"created_at,generated" json:"createdAt"`

	Permissions []Permission `rel:"many_to_many,join=role_permissions" json:"permissions,omitempty"`
}

// RolePermission is the join row. It is a model of its own rather than only the
// far side of the relation because attaching and detaching a permission is an
// operation with its own endpoint, and a repository is what serves it.
type RolePermission struct {
	ID           uuid.UUID `db:"id,pk,auto" json:"id"`
	RoleID       uuid.UUID `json:"roleId"`
	PermissionID uuid.UUID `json:"permissionId"`
}

// A SubjectDefaultRole is the role a freshly registered caller of one kind is
// given when nothing named one.
//
// A row and not a setting. A default role is a statement about *this database* —
// the role it points at has to exist in it, and the id is what says so — while a
// configuration key is a string nothing checks until the first sign-up, where a
// typo grants nothing and reads as a person who registered and cannot do
// anything. See [[D-070]].
//
// Keyed by subject type rather than global, because the answer differs per kind
// of caller: a person who signs up is a client, and a service account that
// enrols is not.
type SubjectDefaultRole struct {
	ID uuid.UUID `db:"id,pk,auto" json:"id"`
	// SubjectType is unique. One kind of caller has one default, and a second
	// row for the same type would be a question with two answers resolved by
	// whichever the engine reached first.
	SubjectType string    `json:"subjectType"`
	RoleID      uuid.UUID `json:"roleId"`
	UpdatedAt   time.Time `db:"updated_at,generated" json:"updatedAt"`

	Role *Role `rel:"belongs_to" json:"role,omitempty"`
}

// A SubjectRole grants a role to one subject.
type SubjectRole struct {
	ID          uuid.UUID `db:"id,pk,auto" json:"id"`
	SubjectType string    `json:"subjectType"`
	SubjectID   uuid.UUID `json:"subjectId"`
	RoleID      uuid.UUID `json:"roleId"`
	GrantedAt   time.Time `db:"granted_at,generated" json:"grantedAt"`

	Role *Role `rel:"belongs_to" json:"role,omitempty"`
}

// A SubjectPermission grants one permission directly, bypassing roles. Laravel
// draws the same distinction, and it earns its keep: the one-off exception that
// would otherwise become a role with a single member.
type SubjectPermission struct {
	ID           uuid.UUID `db:"id,pk,auto" json:"id"`
	SubjectType  string    `json:"subjectType"`
	SubjectID    uuid.UUID `json:"subjectId"`
	PermissionID uuid.UUID `json:"permissionId"`
	GrantedAt    time.Time `db:"granted_at,generated" json:"grantedAt"`

	Permission *Permission `rel:"belongs_to" json:"permission,omitempty"`
}

// A Credential is one way a subject proves who it is: a provider, the
// identifier it is known by there, and whatever that provider verifies against.
//
// SecretHash never leaves the process. It carries json:"-" as well as living
// behind a repository nothing mounts, because two barriers cost one line and a
// hash in a response body cannot be taken back.
type Credential struct {
	ID          uuid.UUID `db:"id,pk,auto" json:"id"`
	SubjectType string    `json:"subjectType"`
	SubjectID   uuid.UUID `json:"subjectId"`
	// Provider names what verifies this credential. Today there is one,
	// ProviderPassword; a second (google, saml) adds rows, not columns.
	Provider string `json:"provider"`
	// Identifier is normalised by the provider before it is stored — lowercased
	// and trimmed for an address — so the unique index is over what a caller
	// actually typed, not over its spelling.
	Identifier string    `json:"identifier"`
	SecretHash string    `db:"secret_hash" json:"-"`
	CreatedAt  time.Time `db:"created_at,generated" json:"createdAt"`
	UpdatedAt  time.Time `db:"updated_at,generated" json:"updatedAt"`
}

// A Session is one live sign-in.
//
// The token is stored as a digest and never as itself: reading this table must
// not be enough to impersonate anybody, which is the property that makes a
// database backup, a log of a slow query and a support engineer's SELECT all
// harmless.
//
// ExpiresAt is absolute and nothing moves it. LastUsedAt drives the idle
// timeout and is written at most once per config.Session.TouchInterval, so an
// authenticated read is not also a write.
type Session struct {
	ID          uuid.UUID `db:"id,pk,auto" json:"id"`
	SubjectType string    `json:"subjectType"`
	SubjectID   uuid.UUID `json:"subjectId"`
	TokenHash   string    `db:"token_hash" json:"-"`
	UserAgent   string    `db:"user_agent" json:"userAgent"`
	IP          string    `db:"ip" json:"ip"`
	CreatedAt   time.Time `db:"created_at,generated" json:"createdAt"`
	LastUsedAt  time.Time `db:"last_used_at" json:"lastUsedAt"`
	ExpiresAt   time.Time `db:"expires_at" json:"expiresAt"`
	// RevokedAt closes the session. The row stays: "signed out at 14:02" is an
	// answer somebody asks for, and a deleted row cannot give it.
	RevokedAt     *time.Time `db:"revoked_at" json:"revokedAt,omitempty"`
	RevokedReason string     `db:"revoked_reason" json:"revokedReason,omitempty"`
}

// Live reports whether the session may still authenticate at the given moment.
func (this Session) Live(now time.Time, idle time.Duration) bool {
	switch {
	case this.RevokedAt != nil:
		return false
	case !now.Before(this.ExpiresAt):
		return false
	case idle > 0 && now.Sub(this.LastUsedAt) > idle:
		return false
	default:
		return true
	}
}
