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

func TestAClosedOrExpiredSessionIsRefusedWithoutBeingClassified(t *testing.T) {
	replayShaped := Presented{
		Digest:    "previous",
		Current:   "current",
		Previous:  "previous",
		RotatedAt: at(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
	}

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
