package accessjwt

import (
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/google/uuid"
)

type rotatingSession struct {
	ID          uuid.UUID `db:"id,pk,auto"`
	SubjectType string
	SubjectID   uuid.UUID
	TokenHash   string `db:"token_hash"`

	PreviousTokenHash string `db:"previous_token_hash"`

	// How many times this session has rotated. It is minted into every refresh
	// credential the session issues, so a credential older than the last two can
	// be recognised as one. Without it, reuse was detectable exactly one rotation
	// back — the current digest and the previous one are two columns, and a
	// credential from three rotations ago matched neither, so a stolen credential
	// the thief sat on became an ordinary 401 that closed nothing.
	Generation    int64      `db:"generation"`
	UserAgent     string     `db:"user_agent"`
	IP            string     `db:"ip"`
	CreatedAt     time.Time  `db:"created_at,generated"`
	LastUsedAt    time.Time  `db:"last_used_at"`
	RotatedAt     *time.Time `db:"rotated_at"`
	ExpiresAt     time.Time  `db:"expires_at"`
	RevokedAt     *time.Time `db:"revoked_at"`
	RevokedReason string     `db:"revoked_reason"`
}

func (rotatingSession) TableName() string { return "sessions" }

type rotatingUpdate struct {
	TokenHash         *string
	PreviousTokenHash *string
	LastUsedAt        *time.Time
	RotatedAt         crud.Opt[time.Time]
	RevokedAt         crud.Opt[time.Time]
	RevokedReason     *string
}
