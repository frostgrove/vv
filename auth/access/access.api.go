package access

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"github.com/google/uuid"
)

type SubjectType string

type SubjectRef struct {
	Type SubjectType `json:"type"`
	ID   uuid.UUID   `json:"id"`
}

func (this SubjectRef) Zero() bool { return this.Type == "" || this.ID == uuid.Nil }

func (this SubjectRef) String() string { return string(this.Type) + ":" + this.ID.String() }

type Directory interface {
	SubjectType() SubjectType

	Active(ctx context.Context, id uuid.UUID) (bool, error)

	Describe(ctx context.Context, id uuid.UUID) (Profile, error)

	Touch(ctx context.Context, id uuid.UUID) error
}

type Profile struct {
	DisplayName string         `json:"displayName"`
	Identifier  string         `json:"identifier"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

const ProviderPassword = "password"

const (
	PermRoleRead    auth.Permission = "role.read"
	PermRoleWrite   auth.Permission = "role.write"
	PermRoleDelete  auth.Permission = "role.delete"
	PermGrantRead   auth.Permission = "grant.read"
	PermGrantWrite  auth.Permission = "grant.write"
	PermSessionRead auth.Permission = "session.read"
	PermSessionKill auth.Permission = "session.revoke"

	PermCredentialWrite auth.Permission = "credential.write"
)

const CodeUnknownSubjectType errs.Code = "unknown_subject_type"

const RoleAdmin auth.Role = "admin"

type ModuleGrants struct {
	Module string

	Permissions []PermissionDef

	Roles map[auth.Role][]auth.Permission
}

type PermissionDef struct {
	Code auth.Permission
	Name string
}

type Grants interface {
	For(ctx context.Context, ref SubjectRef) (*Principal, error)
}

type Principal struct {
	Ref         SubjectRef
	SessionID   uuid.UUID
	Roles       []auth.Role
	Permissions []auth.Permission
	Profile     Profile
}

func (this *Principal) Subject() string { return this.Ref.ID.String() }

func (this *Principal) In(role auth.Role) bool {
	for _, has := range this.Roles {
		if has == role {
			return true
		}
	}
	return false
}

func (this *Principal) Has(permission auth.Permission) bool {
	for _, has := range this.Permissions {
		if has == permission {
			return true
		}
	}
	return false
}

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

const (
	AttrSubjectID   = "subject_id"
	AttrSubjectType = "subject_type"
	AttrSessionID   = "session_id"
)

func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return nil, false
	}
	accessPrincipal, ok := principal.(*Principal)
	return accessPrincipal, ok
}

func RequirePrincipal(ctx context.Context) (*Principal, error) {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, auth.Unauthenticated("no principal in context")
	}
	return principal, nil
}

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

type DirectoryLookup interface {
	Directory(subjectType SubjectType) (Directory, bool)
}

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

var ErrNoRefresh = errors.New("access: this subject's strategy does not rotate")

func BadSessionID(raw string) error {
	return port.BadRequestAs(errs.CodeInvalidID, port.At("id"), "%q is not a session id", raw)
}
