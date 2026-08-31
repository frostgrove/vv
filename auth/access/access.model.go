package access

//go:generate go run github.com/frostgrove/vv/cmd/vv -types Permission,Role,RolePermission,SubjectRole,SubjectPermission,SubjectDefaultRole,Credential,Session

import (
	"time"

	"github.com/google/uuid"
)

type Permission struct {
	ID        uuid.UUID `db:"id,pk,auto" json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Module    string    `json:"module"`
	CreatedAt time.Time `db:"created_at,generated" json:"createdAt"`
}

type Role struct {
	ID        uuid.UUID `db:"id,pk,auto" json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	IsSystem  bool      `db:"is_system,immutable" json:"isSystem"`
	CreatedAt time.Time `db:"created_at,generated" json:"createdAt"`

	Permissions []Permission `rel:"many_to_many,join=role_permissions" json:"permissions,omitempty"`
}

type RolePermission struct {
	ID           uuid.UUID `db:"id,pk,auto" json:"id"`
	RoleID       uuid.UUID `json:"roleId"`
	PermissionID uuid.UUID `json:"permissionId"`
}

type SubjectDefaultRole struct {
	ID uuid.UUID `db:"id,pk,auto" json:"id"`

	SubjectType string    `json:"subjectType"`
	RoleID      uuid.UUID `json:"roleId"`
	UpdatedAt   time.Time `db:"updated_at,generated" json:"updatedAt"`

	Role *Role `rel:"belongs_to" json:"role,omitempty"`
}

type SubjectRole struct {
	ID          uuid.UUID `db:"id,pk,auto" json:"id"`
	SubjectType string    `json:"subjectType"`
	SubjectID   uuid.UUID `json:"subjectId"`
	RoleID      uuid.UUID `json:"roleId"`
	GrantedAt   time.Time `db:"granted_at,generated" json:"grantedAt"`

	Role *Role `rel:"belongs_to" json:"role,omitempty"`
}

type SubjectPermission struct {
	ID           uuid.UUID `db:"id,pk,auto" json:"id"`
	SubjectType  string    `json:"subjectType"`
	SubjectID    uuid.UUID `json:"subjectId"`
	PermissionID uuid.UUID `json:"permissionId"`
	GrantedAt    time.Time `db:"granted_at,generated" json:"grantedAt"`

	Permission *Permission `rel:"belongs_to" json:"permission,omitempty"`
}

type Credential struct {
	ID          uuid.UUID `db:"id,pk,auto" json:"id"`
	SubjectType string    `json:"subjectType"`
	SubjectID   uuid.UUID `json:"subjectId"`

	Provider string `json:"provider"`

	Identifier string    `json:"identifier"`
	SecretHash string    `db:"secret_hash" json:"-"`
	CreatedAt  time.Time `db:"created_at,generated" json:"createdAt"`

	UpdatedAt time.Time `db:"updated_at,generated" json:"updatedAt"`
}

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

	RevokedAt     *time.Time `db:"revoked_at" json:"revokedAt,omitempty"`
	RevokedReason string     `db:"revoked_reason" json:"revokedReason,omitempty"`
}

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
