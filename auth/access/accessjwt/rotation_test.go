package accessjwt

import (
	"testing"
	"time"
)

func at(offset time.Duration) *time.Time {
	moment := time.Unix(1_700_000_000, 0).Add(offset)
	return &moment
}

var now = time.Unix(1_700_000_000, 0)

// The current credential rotates. This is the control for everything below: if
// Classify ever returned Unusable unconditionally, only this test would notice.
func TestTheCurrentCredentialRotates(t *testing.T) {
	got := Classify(Presented{
		Digest:    "current",
		Current:   "current",
		ExpiresAt: now.Add(time.Hour),
	}, now, 10*time.Second)
	if got != Rotate {
		t.Fatalf("the live current credential classified as %v", got)
	}
}

// Two tabs refreshing at once is not an attack. Refusing here signs somebody
// out for having two windows open, which is the failure people actually hit.
func TestThePreviousCredentialInsideTheGraceWindowRotatesAgain(t *testing.T) {
	got := Classify(Presented{
		Digest:    "previous",
		Current:   "current",
		Previous:  "previous",
		RotatedAt: at(-2 * time.Second),
		ExpiresAt: now.Add(time.Hour),
	}, now, 10*time.Second)
	if got != RotateAgain {
		t.Fatalf("a concurrent refresh classified as %v", got)
	}
}

// The same credential outside the window is a credential someone kept. That is
// what a stolen refresh token looks like, and the lineage is closed.
func TestThePreviousCredentialOutsideTheGraceWindowIsAReplay(t *testing.T) {
	got := Classify(Presented{
		Digest:    "previous",
		Current:   "current",
		Previous:  "previous",
		RotatedAt: at(-11 * time.Second),
		ExpiresAt: now.Add(time.Hour),
	}, now, 10*time.Second)
	if got != Replay {
		t.Fatalf("a replay classified as %v", got)
	}
}

// The boundary is inclusive, and it is worth pinning: an off-by-one here turns
// every refresh that lands exactly on the grace edge into a forced sign-out.
func TestTheGraceWindowIsInclusiveAtItsEdge(t *testing.T) {
	presented := Presented{
		Digest:    "previous",
		Current:   "current",
		Previous:  "previous",
		RotatedAt: at(-10 * time.Second),
		ExpiresAt: now.Add(time.Hour),
	}
	if got := Classify(presented, now, 10*time.Second); got != RotateAgain {
		t.Fatalf("exactly at the grace edge classified as %v", got)
	}
	presented.RotatedAt = at(-10*time.Second - time.Nanosecond)
	if got := Classify(presented, now, 10*time.Second); got != Replay {
		t.Fatalf("one nanosecond past the grace edge classified as %v", got)
	}
}

// A closed or expired session refuses without classifying. A replay against a
// session that is already shut costs nothing more, and reporting it would bury
// the ones that matter.
func TestAClosedOrExpiredSessionIsRefusedWithoutBeingClassified(t *testing.T) {
	replayShaped := Presented{
		Digest:    "previous",
		Current:   "current",
		Previous:  "previous",
		RotatedAt: at(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
	}
	// The control: with the session live, this same input is a replay.
	if got := Classify(replayShaped, now, 10*time.Second); got != Replay {
		t.Fatalf("the control input classified as %v, so the two checks below prove nothing", got)
	}

	revoked := replayShaped
	revoked.Revoked = true
	if got := Classify(revoked, now, 10*time.Second); got != Unusable {
		t.Fatalf("a revoked session classified as %v", got)
	}

	expired := replayShaped
	expired.ExpiresAt = now.Add(-time.Second)
	if got := Classify(expired, now, 10*time.Second); got != Unusable {
		t.Fatalf("an expired session classified as %v", got)
	}
}

// A digest matching neither is not a replay — nothing says it was ever real —
// and an empty one is a caller who sent no credential at all.
func TestACredentialThatMatchesNeitherDigestIsSimplyUnusable(t *testing.T) {
	base := Presented{Current: "current", Previous: "previous", RotatedAt: at(0), ExpiresAt: now.Add(time.Hour)}

	unknown := base
	unknown.Digest = "something-else"
	if got := Classify(unknown, now, 10*time.Second); got != Unusable {
		t.Fatalf("an unknown digest classified as %v", got)
	}

	empty := base
	if got := Classify(empty, now, 10*time.Second); got != Unusable {
		t.Fatalf("an empty digest classified as %v", got)
	}
}

// A row with a previous digest and no rotation time was not written by this
// module. Refusing is the only safe reading.
func TestAPreviousDigestWithNoRotationTimeIsRefused(t *testing.T) {
	got := Classify(Presented{
		Digest:    "previous",
		Current:   "current",
		Previous:  "previous",
		ExpiresAt: now.Add(time.Hour),
	}, now, 10*time.Second)
	if got != Unusable {
		t.Fatalf("a previous digest with no rotation time classified as %v", got)
	}
}
