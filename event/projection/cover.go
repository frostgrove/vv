package projection

import (
	"fmt"
	"slices"
)

// The set a host declares its topology as: every key matches exactly one member.
// A mask makes each partition correct on its own and says nothing about the set,
// which is where the two silent failures live — a forgotten member, whose share
// of the log is never delivered to anything while every runner reports healthy,
// and two members that overlap, which are two different names, two checkpoint
// rows, no fence between them, and every key matching both applied twice and out
// of order the moment their cursors diverge.
//
// Build your runners from Partitions(): a set that was checked is the one thing
// that makes those two unconstructible. A single runner cannot see the set, so a
// host that assembles runners by hand keeps the freedom it always had.
//
// The zero value is the one Cover a caller can produce without NewCover, and it
// is the one value NewCover never answers beside a nil error: a set of no members
// is its first refusal. Count() == 0 tells the two apart exactly, which is why no
// marker field is carried, and every door in this phase that takes a Cover
// refuses that one rather than folding an aggregate over a set nobody checked.
type Cover struct{ partitions []Partition }

// Two exact arithmetic facts and nothing heuristic: two members overlap when
// (idA ^ idB) & min(maskA, maskB) is zero, and a set with no overlap covers the
// space when the members' shares sum to MaxPartitions.
func NewCover(partitions ...Partition) (Cover, error) {
	if len(partitions) == 0 {
		return Cover{}, fmt.Errorf("%w: a cover of no partitions matches no key, and every key must match exactly one member", ErrTopology)
	}
	for outer := range partitions {
		for inner := outer + 1; inner < len(partitions); inner++ {
			left, right := partitions[outer], partitions[inner]
			if (left.id^right.id)&min(left.mask, right.mask) == 0 {
				return Cover{}, fmt.Errorf("%w: %s and %s both match every key one of them matches, and two members that overlap are two names, two checkpoint rows and no fence between them",
					ErrTopology, left.described(), right.described())
			}
		}
	}
	share := uint64(0)
	for _, part := range partitions {
		share += MaxPartitions / (uint64(part.mask) + 1)
	}
	if share != MaxPartitions {
		return Cover{}, fmt.Errorf("%w: %s matches no member of this set, so every key of it is delivered to nothing for ever while each member reports healthy",
			ErrTopology, gapIn(partitions).described())
	}
	return Cover{partitions: slices.Clone(partitions)}, nil
}

// A copy, so a caller cannot re-open a set that was checked.
func (this Cover) Partitions() []Partition { return slices.Clone(this.partitions) }

func (this Cover) Count() int { return len(this.partitions) }

// The share of the space no member matches, named at the finest granularity the
// set itself uses so the answer reads as one of the members that were meant to be
// there. It is reached only when the shares fall short of the whole and no two
// members overlap, which is exactly when such a share exists.
func gapIn(partitions []Partition) Partition {
	finest := uint32(0)
	for _, part := range partitions {
		finest = max(finest, part.mask)
	}
	for id := uint32(0); id <= finest; id++ {
		covered := false
		for _, part := range partitions {
			if part.mask&id == part.id {
				covered = true
				break
			}
		}
		if !covered {
			return Partition{id: id, mask: finest}
		}
	}
	return Whole()
}
