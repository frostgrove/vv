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
	}, now, Window{Grace: 10 * time.Second})
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
	}, now, Window{Grace: 10 * time.Second})
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
	}, now, Window{Grace: 10 * time.Second})
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
	if got := Classify(presented, now, Window{Grace: 10 * time.Second}); got != RotateAgain {
		t.Fatalf("exactly at the grace edge classified as %v", got)
	}
	presented.RotatedAt = at(-10*time.Second - time.Nanosecond)
	if got := Classify(presented, now, Window{Grace: 10 * time.Second}); got != Replay {
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

	if got := Classify(replayShaped, now, Window{Grace: 10 * time.Second}); got != Replay {
		t.Fatalf("the control input classified as %v, so the two checks below prove nothing", got)
	}

	revoked := replayShaped
	revoked.Revoked = true
	if got := Classify(revoked, now, Window{Grace: 10 * time.Second}); got != Unusable {
		t.Fatalf("a revoked session classified as %v", got)
	}

	expired := replayShaped
	expired.ExpiresAt = now.Add(-time.Second)
	if got := Classify(expired, now, Window{Grace: 10 * time.Second}); got != Unusable {
		t.Fatalf("an expired session classified as %v", got)
	}
}

func TestACredentialThatMatchesNeitherDigestIsSimplyUnusable(t *testing.T) {
	base := Presented{Current: "current", Previous: "previous", RotatedAt: at(0), ExpiresAt: now.Add(time.Hour)}

	unknown := base
	unknown.Digest = "something-else"
	if got := Classify(unknown, now, Window{Grace: 10 * time.Second}); got != Unusable {
		t.Fatalf("an unknown digest classified as %v", got)
	}

	empty := base
	if got := Classify(empty, now, Window{Grace: 10 * time.Second}); got != Unusable {
		t.Fatalf("an empty digest classified as %v", got)
	}
}

func TestAPreviousDigestWithNoRotationTimeIsRefused(t *testing.T) {
	got := Classify(Presented{
		Digest:    "previous",
		Current:   "current",
		Previous:  "previous",
		ExpiresAt: now.Add(time.Hour),
	}, now, Window{Grace: 10 * time.Second})
	if got != Unusable {
		t.Fatalf("a previous digest with no rotation time classified as %v", got)
	}
}

func TestASessionIdleLongerThanTheIdleTTLIsUnusableHoweverGoodItsCredential(t *testing.T) {
	live := Presented{
		Digest:     "current",
		Current:    "current",
		LastUsedAt: now.Add(-6 * 24 * time.Hour),
		ExpiresAt:  now.Add(24 * time.Hour),
	}
	window := Window{Grace: 10 * time.Second, Idle: 7 * 24 * time.Hour}

	if got := Classify(live, now, window); got != Rotate {
		t.Fatalf("the control input classified as %v, so the check below proves nothing", got)
	}

	abandoned := live
	abandoned.LastUsedAt = now.Add(-8 * 24 * time.Hour)
	if got := Classify(abandoned, now, window); got != Unusable {
		t.Fatalf("a session idle past the idle TTL classified as %v; rotating it would restart a session nobody touched for eight days", got)
	}
}

func TestAnIdleDeadlineNobodySetLeavesEveryCredentialAlone(t *testing.T) {
	forgotten := Presented{
		Digest:     "current",
		Current:    "current",
		LastUsedAt: now.Add(-365 * 24 * time.Hour),
		ExpiresAt:  now.Add(24 * time.Hour),
	}
	if got := Classify(forgotten, now, Window{Grace: 10 * time.Second}); got != Rotate {
		t.Fatalf("a deployment that configured no idle deadline had one applied anyway: %v", got)
	}
}
