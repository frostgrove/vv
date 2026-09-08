package accessjwt

import "time"

type Outcome int

const (
	Rotate Outcome = iota

	RotateAgain

	Replay

	Unusable
)

func (this Outcome) String() string {
	switch this {
	case Rotate:
		return "rotate"
	case RotateAgain:
		return "rotate-again"
	case Replay:
		return "replay"
	default:
		return "unusable"
	}
}

type Presented struct {
	Digest string

	Current  string
	Previous string

	RotatedAt  *time.Time
	LastUsedAt time.Time

	// What the credential says its generation is, and what the session's is now.
	// A credential from before the previous rotation matches neither digest, so
	// without these it fell off the end of the lookup and read as an ordinary
	// expired credential — a stolen one the thief sat on closed nothing.
	Generation        int64
	CurrentGeneration int64

	Revoked   bool
	ExpiresAt time.Time
}

type Window struct {
	Grace time.Duration

	Idle time.Duration
}

func Classify(presented Presented, now time.Time, window Window) Outcome {
	switch {
	case presented.Revoked, !now.Before(presented.ExpiresAt):
		return Unusable
	case window.Idle > 0 && now.Sub(presented.LastUsedAt) > window.Idle:
		return Unusable
	case presented.Digest == "":
		return Unusable
	case presented.Digest == presented.Current:
		return Rotate
	case presented.Generation > 0 && presented.Generation < presented.CurrentGeneration-1:
		// Older than the previous rotation. It cannot match either digest, and it
		// is a credential this session really issued — so it was kept, and that is
		// a replay rather than something to shrug at.
		return Replay
	case presented.Previous == "" || presented.Digest != presented.Previous:
		return Unusable
	case presented.RotatedAt == nil:
		return Unusable
	case now.Sub(*presented.RotatedAt) <= window.Grace:
		return RotateAgain
	default:
		return Replay
	}
}
