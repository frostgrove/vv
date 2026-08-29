package access

import (
	"github.com/frostgrove/vv/auth"
	"github.com/google/uuid"
)

// The value objects a use case takes. They are structs rather than argument
// lists because every one of them has two fields of the same type — two
// strings, two ids — and a transposed pair at a call site compiles.

// EnrollCommand gives a subject that already exists something to sign in with.
//
// Subject is the caller's: this context does not create identities, so the
// reference is a finished one and the subject type is whatever the application
// registered a Directory for.
type EnrollCommand struct {
	Subject SubjectRef
	// Identifier is stored exactly as it arrives. Normalising it — lowercasing
	// an address, trimming it — is the application's, because what an
	// identifier means belongs to whatever issues it, and the same rule has to
	// be applied before [LoginCommand] for the two to meet.
	Identifier string
	Password   string
	// Role is granted on success. Empty grants nothing.
	Role auth.Role
}

// OpenSessionCommand mints a session for a subject something else has already
// authenticated.
type OpenSessionCommand struct {
	Subject SubjectRef
	Agent   Agent
}

// LoginCommand exchanges a credential for a session.
type LoginCommand struct {
	// Subject is which kind of caller is signing in. It is required: an
	// identifier is unique within a subject type, so a sign-in that did not say
	// which one is a question with more than one answer — and answering it by
	// whichever row came back first is a sign-in as the wrong person.
	Subject SubjectType
	// Identifier is compared verbatim against the stored column. See
	// [EnrollCommand].
	Identifier string
	Password   string
	Agent      Agent
}

// Agent is what the transport knows about the caller's device. It is recorded
// on the session so that a session list is something a person can recognise
// themselves in, and truncated at the boundary rather than at the column: one
// engine raises where another quietly stores a shortened value, and a limit
// enforced in only one of them is not a limit.
type Agent struct {
	UserAgent string
	IP        string
}

// MaxUserAgent bounds what is stored. A header is caller-controlled and
// unbounded; the column is not.
const MaxUserAgent = 256

// Truncated returns the agent as it will be stored.
func (this Agent) Truncated() Agent {
	if len(this.UserAgent) > MaxUserAgent {
		this.UserAgent = this.UserAgent[:MaxUserAgent]
	}
	return this
}

// LogoutCommand closes one session — the one the request arrived on.
type LogoutCommand struct {
	SessionID uuid.UUID
}

// LogoutAllCommand closes every session a subject holds.
//
// Except is the session to keep, and it is a field rather than a second use
// case: "sign out everywhere else" is what a person means by the button, and
// signing the current device out too makes the confirmation dialog land on a
// login screen.
type LogoutAllCommand struct {
	Subject SubjectRef
	Except  uuid.UUID
}

// ChangePasswordCommand replaces a caller's own secret.
//
// Current is required even though the caller is authenticated: a session
// somebody walked away from must not be enough to lock its owner out.
type ChangePasswordCommand struct {
	Subject SubjectRef
	Current string
	New     string
	// RevokeOthers closes every other session on success, which is what makes a
	// password change useful after a device is lost.
	RevokeOthers bool
	Keep         uuid.UUID
}

// SetPasswordCommand is an administrator giving a subject a password it did not
// choose — the reset, and what makes an account created through the users
// endpoint able to sign in at all.
//
// There is no Current: the caller is not the subject. What guards it is the
// permission, and the sessions it closes.
type SetPasswordCommand struct {
	Subject  SubjectRef
	Password string
}

// GrantRoleCommand attaches or detaches a role.
type GrantRoleCommand struct {
	Subject SubjectRef
	Role    auth.Role
}

// GrantPermissionCommand attaches or detaches a permission directly, without a
// role.
type GrantPermissionCommand struct {
	Subject    SubjectRef
	Permission auth.Permission
}

// AttachPermissionCommand attaches or detaches a permission on a role.
type AttachPermissionCommand struct {
	Role       uuid.UUID
	Permission auth.Permission
}
