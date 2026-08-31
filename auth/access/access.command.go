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
	IP        string
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
