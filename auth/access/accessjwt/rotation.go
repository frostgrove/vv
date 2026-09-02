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
