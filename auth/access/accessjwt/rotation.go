package accessjwt

import "time"

// An Outcome is what a presented refresh credential turns out to be.
//
// The type exists so the decision can be made without a database and tested
// without one. It is the shape the Python context this module replaces called
// `classify_lost_cas`, and it is the part of rotation that is easy to get
// subtly wrong: treating a concurrent refresh as a replay signs people out for
// having two tabs open, and treating a replay as concurrent leaves a stolen
// credential working.
type Outcome int

const (
	// Rotate: the credential is the current one. Swap it.
	Rotate Outcome = iota
	// RotateAgain: the credential is the previous one and the rotation that
	// replaced it happened within the grace window. Two tabs refreshed at once.
	// Rotate again and answer normally.
	RotateAgain
	// Replay: the credential was spent, and the grace window has passed. This
	// is what a stolen refresh token looks like. Close the lineage.
	Replay
	// Unusable: the session is revoked or expired, or the credential matches
	// nothing. One refusal, whatever the reason.
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

// A Presented is one refresh attempt, as a value.
type Presented struct {
	// Digest is the hash of the credential the caller sent.
	Digest string
	// Current and Previous are the session's two digests.
	Current  string
	Previous string
	// RotatedAt is when Previous was replaced, nil if it never was.
	RotatedAt *time.Time
	// Revoked and ExpiresAt are the session's own liveness.
	Revoked   bool
	ExpiresAt time.Time
}

// Classify decides what one refresh attempt is.
//
// Liveness first, and deliberately: a credential presented against a revoked or
// expired session is refused without being classified, because a replay against
// a session that is already closed costs nothing more and reporting it would
// bury the ones that matter.
func Classify(presented Presented, now time.Time, grace time.Duration) Outcome {
	switch {
	case presented.Revoked, !now.Before(presented.ExpiresAt):
		return Unusable
	case presented.Digest == "":
		return Unusable
	case presented.Digest == presented.Current:
		return Rotate
	case presented.Previous == "" || presented.Digest != presented.Previous:
		return Unusable
	case presented.RotatedAt == nil:
		// A previous digest with no rotation time is a row this module did not
		// write. Refusing is the only safe reading.
		return Unusable
	case now.Sub(*presented.RotatedAt) <= grace:
		return RotateAgain
	default:
		return Replay
	}
}
