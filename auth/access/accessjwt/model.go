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

func (rotatingSession) TableName() string { return "sessions" }

type rotatingUpdate struct {
	TokenHash         *string
	PreviousTokenHash *string
	LastUsedAt        *time.Time
	RotatedAt         crud.Opt[time.Time]
	RevokedAt         crud.Opt[time.Time]
	RevokedReason     *string
}
