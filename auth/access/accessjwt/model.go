package accessjwt

import (
	"time"

	"github.com/google/uuid"
)

// rotatingSession is the sessions row as this module sees it: the core's
// columns plus the two the rotation migration adds.
//
// A second model over the same table rather than two columns on access.Session,
// because a deployment holding opaque sessions never rotates and would be
// carrying two columns that mean nothing. crud.Tabler is what points it at the
// shared table.
type rotatingSession struct {
	ID          uuid.UUID `db:"id,pk,auto"`
	SubjectType string
	SubjectID   uuid.UUID
	TokenHash   string `db:"token_hash"`
	// PreviousTokenHash is the digest this session accepted before the last
	// rotation. Empty rather than null so a lookup by it needs no null-safe
	// comparison, and the partial index skips the empty ones.
	PreviousTokenHash string     `db:"previous_token_hash"`
	UserAgent         string     `db:"user_agent"`
	IP                string     `db:"ip"`
	CreatedAt         time.Time  `db:"created_at,generated"`
	LastUsedAt        time.Time  `db:"last_used_at"`
	RotatedAt         *time.Time `db:"rotated_at"`
	ExpiresAt         time.Time  `db:"expires_at"`
	RevokedAt         *time.Time `db:"revoked_at"`
	RevokedReason     string     `db:"revoked_reason"`
}

// TableName implements crud.Tabler: this is the access sessions table, read
// through a wider model.
func (rotatingSession) TableName() string { return "sessions" }

// rotatingUpdate is the partial-update DTO. Hand-written rather than generated
// because this model exists only inside this module and nothing mounts CRUD
// over it.
type rotatingUpdate struct {
	TokenHash         *string
	PreviousTokenHash *string
	LastUsedAt        *time.Time
	RotatedAt         any
	RevokedAt         any
	RevokedReason     *string
}
