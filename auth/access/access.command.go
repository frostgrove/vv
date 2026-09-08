package access

import (
	"github.com/frostgrove/vv/auth"
	"github.com/google/uuid"
)

type EnrollCommand struct {
	Subject SubjectRef

	Identifier string
	Password   string

	Role auth.Role
}

type OpenSessionCommand struct {
	Subject SubjectRef
	Agent   Agent
}

type LoginCommand struct {
	Subject SubjectType

	Identifier string
	Password   string
	Agent      Agent
}

type Agent struct {
	UserAgent string

	// A bare address, with no port. Gin and Fiber hand one over already;
	// net/http's RemoteAddr is "host:port" and used to be stored verbatim, so the
	// same client produced a different Attempt.IP on each binding and anything
	// grouping attempts by address — a lockout, a report, an alert — grouped by
	// ephemeral port instead, which never repeats.
	IP string
}

const MaxUserAgent = 256

func (this Agent) Truncated() Agent {
	if len(this.UserAgent) > MaxUserAgent {
		this.UserAgent = this.UserAgent[:MaxUserAgent]
	}
	return this
}

type LogoutCommand struct {
	SessionID uuid.UUID
}

type LogoutAllCommand struct {
	Subject SubjectRef
	Except  uuid.UUID
}

type ChangePasswordCommand struct {
	Subject SubjectRef
	Current string
	New     string

	// The caller, for the same limiter and observer that guard sign-in. Without
	// it the current-password check was an unlimited, unobserved oracle behind a
	// valid session: an attacker with a stolen access token could guess the
	// password at full speed, and nothing counted it.
	Agent Agent

	RevokeOthers bool
	Keep         uuid.UUID
}

type SetPasswordCommand struct {
	Subject  SubjectRef
	Password string
}

type GrantRoleCommand struct {
	Subject SubjectRef
	Role    auth.Role
}

type GrantPermissionCommand struct {
	Subject    SubjectRef
	Permission auth.Permission
}

type AttachPermissionCommand struct {
	Role       uuid.UUID
	Permission auth.Permission
}
