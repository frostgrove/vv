package eventtest_test

import (
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventtest"
)

// The proxy an application discharges an obligation the framework cannot check
// with, so what it must not do is answer nothing for a pair that collides — an
// application told its identities are injective when they are not runs two
// aggregates over one history, each folding the other's facts, with no error at
// any point. Answering something for a pair that does not collide is the same
// defect facing the other way: a proxy that refuses everything certifies
// nothing. So the property is an equivalence and not an implication, over
// identity parts the framework never sees until a mapper renders them: the
// separator Compose escapes, the empty part, control bytes, and a part longer
// than any key may be.
func FuzzKeysReportsACollisionExactlyWhenTwoIdentitiesRenderOneKey(f *testing.F) {
	for _, seed := range [][4]string{
		{"eu", "west/17", "eu/west", "17"},
		{"eu", "17", "eu", "18"},
		{"", "", "", ""},
		{"", "a", "a", ""},
		{"a", "b", "a", "b"},
		{"a/b", "c", "a", "b/c"},
		{"a\x00b", "c", "a", "c"},
		{"a\\b", "c", "a", "\\b/c"},
		{strings.Repeat("k", 4096), "a", "b", "c"},
		{"café", "a", "cafe\u0301", "a"},
	} {
		f.Add(seed[0], seed[1], seed[2], seed[3])
	}

	f.Fuzz(func(t *testing.T, leftRegion, leftBin, rightRegion, rightBin string) {
		composed := event.Define[inventory]("eventtest.fuzz", func(id warehouseID) event.Key {
			return event.Compose(id.Region, id.Bin)
		})
		left := warehouseID{Region: leftRegion, Bin: leftBin}
		right := warehouseID{Region: rightRegion, Bin: rightBin}

		leftKey, leftErr := composed.Key(left)
		rightKey, rightErr := composed.Key(right)
		collides := leftErr != nil || rightErr != nil || leftKey == rightKey

		switch reported := eventtest.KeysAnswers(composed, left, right); {
		case collides && reported == nil:
			t.Fatalf("two identities this aggregate does not render two legal keys for were reported injective, so an application composing them is told it has one history per instance when it has one history for two: %q/%q and %q/%q",
				leftRegion, leftBin, rightRegion, rightBin)
		case !collides && reported != nil:
			t.Fatalf("two identities rendering the two distinct keys %q and %q were reported to collide with %v, so the proxy refuses a mapper that is correct",
				leftKey, rightKey, reported)
		}
	})
}
