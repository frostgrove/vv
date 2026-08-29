package access

import (
	"time"

	"github.com/google/uuid"
)

// AuthResponse is the single answer to register, login and rotating.
//
// One shape for all three on purpose: a client that has just signed up and a
// client that has just signed in are in the same state, and a second shape for
// the second case is a second branch in every consumer of it.
//
// Every credential field is omitzero, and that is what lets one shape also serve
// a caller who asked for its credentials in cookies: what an HTTP binding put in
// a cookie it clears here, and the field is then absent rather than empty. A
// `"token": ""` is a field a client has to know to ignore, and one that did not
// would present the empty string as a bearer and be told it is not signed in.
// omitzero and not omitempty because a time.Time is a struct, and omitempty has
// never omitted one.
type AuthResponse struct {
	Token     string    `json:"token,omitzero"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`

	// Refresh is present only for a strategy that rotates. An opaque session
	// has nothing to refresh — its token is valid until the row says otherwise
	// — so the field is omitted rather than filled with a copy of Token, which
	// a client would then send to a /auth/refresh that does not exist.
	Refresh          string    `json:"refresh,omitzero"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt,omitzero"`

	Principal PrincipalDto `json:"principal"`
}

// PrincipalDto is the caller as a client sees itself. It carries the resolved
// permissions rather than only the roles, because a UI that hides a button
// needs the answer and not the input to it — and re-deriving role → permission
// on the client is a second copy of the rule that goes stale silently.
type PrincipalDto struct {
	Subject     SubjectRef `json:"subject"`
	Profile     Profile    `json:"profile"`
	Roles       []string   `json:"roles"`
	Permissions []string   `json:"permissions"`
}

// NewPrincipalDto renders a principal for the wire.
func NewPrincipalDto(principal *Principal) PrincipalDto {
	return PrincipalDto{
		Subject:     principal.Ref,
		Profile:     principal.Profile,
		Roles:       texts(principal.Roles),
		Permissions: texts(principal.Permissions),
	}
}

// SessionDto is one of the caller's live sign-ins, for the session list.
//
// Neither the token nor its digest appears. Listing your own sessions must not
// be a way to reach the credential of any of them, including the current one.
type SessionDto struct {
	ID         uuid.UUID `json:"id"`
	Current    bool      `json:"current"`
	UserAgent  string    `json:"userAgent,omitempty"`
	IP         string    `json:"ip,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// NewSessionDto renders a session, marking the one the request arrived on.
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

// GrantsDto is what a subject holds, as an administrator sees it: the roles it
// was given, the permissions granted to it directly, and the union the gate
// actually enforces.
type GrantsDto struct {
	Subject           SubjectRef `json:"subject"`
	Roles             []string   `json:"roles"`
	DirectPermissions []string   `json:"directPermissions"`
	Effective         []string   `json:"effective"`
}

// LogoutResponse reports how many sessions were closed. logout-all answering
// "1" when the caller expected four is the difference between a bug and a
// second device somebody forgot about.
type LogoutResponse struct {
	Revoked int64 `json:"revoked"`
}

// texts renders a slice of a string-kinded type — auth.Role, auth.Permission —
// for JSON. Not named `strings`, which would shadow the package of that name
// for every file here.
func texts[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}
