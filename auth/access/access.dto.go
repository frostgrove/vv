package access

import (
	"time"

	"github.com/google/uuid"
)

type AuthResponse struct {
	Token     string    `json:"token,omitzero"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`

	Refresh          string    `json:"refresh,omitzero"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt,omitzero"`

	Principal PrincipalDto `json:"principal"`
}

type PrincipalDto struct {
	Subject     SubjectRef `json:"subject"`
	Profile     Profile    `json:"profile"`
	Roles       []string   `json:"roles"`
	Permissions []string   `json:"permissions"`
}

func NewPrincipalDto(principal *Principal) PrincipalDto {
	return PrincipalDto{
		Subject:     principal.Ref,
		Profile:     principal.Profile,
		Roles:       texts(principal.Roles),
		Permissions: texts(principal.Permissions),
	}
}

type SessionDto struct {
	ID         uuid.UUID `json:"id"`
	Current    bool      `json:"current"`
	UserAgent  string    `json:"userAgent,omitempty"`
	IP         string    `json:"ip,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func NewSessionDto(session Session, current uuid.UUID) SessionDto {
	return SessionDto{
		ID:         session.ID,
		Current:    session.ID == current,
		UserAgent:  session.UserAgent,
		IP:         session.IP,
		CreatedAt:  session.CreatedAt,
		LastUsedAt: session.LastUsedAt,
		ExpiresAt:  session.ExpiresAt,
	}
}

type GrantsDto struct {
	Subject           SubjectRef `json:"subject"`
	Roles             []string   `json:"roles"`
	DirectPermissions []string   `json:"directPermissions"`
	Effective         []string   `json:"effective"`
}

type LogoutResponse struct {
	Revoked int64 `json:"revoked"`
}

func texts[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}
