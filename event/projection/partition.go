package projection

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The ceiling on how many partitions one projection may be divided into, and
// therefore on a mask: the finest mask is MaxPartitions-1.
const MaxPartitions = 1024

const partitionSeparator = "."

// A fraction of the key space, and a mask rather than a modulus. A key belongs
// to the partition whose id is the low k bits of FNV-1a/32 over the UTF-8 bytes
// of its sequence key.
//
// The mask is always 2^k-1. A split takes one partition to two at mask 2m+1 and
// moves no key out of the parent's half of the space; under hash % N -> hash %
// (N+1) roughly N/(N+1) of all keys change partition and each lands in one whose
// checkpoint is at an unrelated position, so events of one key are skipped in one
// direction and re-delivered out of order in the other.
//
// No count is stored anywhere. What exists is a set of checkpoint rows, one per
// live partition, and the mask each runner was constructed with.
type Partition struct{ id, mask uint32 }

// The whole key space, which is what a projection that was never partitioned
// already is: it is the zero value, so a spec that names no partition is one
// runner over everything.
func Whole() Partition { return Partition{} }

func NewPartition(id, mask uint32) (Partition, error) {
	if mask&(mask+1) != 0 {
		return Partition{}, fmt.Errorf("%w: a partition mask ends on a consecutive run of 1s and %d does not, so the low bits of a hash would not name one member of any split", ErrTopology, mask)
	}
	if uint64(mask)+1 > MaxPartitions {
		return Partition{}, fmt.Errorf("%w: a mask of %d asks for %d partitions and MaxPartitions is %d", ErrTopology, mask, uint64(mask)+1, MaxPartitions)
	}
	if id > mask {
		return Partition{}, fmt.Errorf("%w: a partition id is the low bits of a hash under its own mask, and %d is above the mask %d", ErrTopology, id, mask)
	}
	return Partition{id: id, mask: mask}, nil
}

// The inverse of String, so the whole key space parses out of the empty text and
// "0.0" is refused: two spellings of one partition would make a name that is a
// primary key ambiguous.
func ParsePartition(text string) (Partition, error) {
	if text == "" {
		return Whole(), nil
	}
	written, spelled, found := strings.Cut(text, partitionSeparator)
	if !found {
		return Partition{}, fmt.Errorf("%w: a partition renders as an id and a mask around %q and %q carries none", ErrTopology, partitionSeparator, text)
	}
	id, err := parseNumber(written)
	if err != nil {
		return Partition{}, fmt.Errorf("%w: %q names no partition id: %w", ErrTopology, text, err)
	}
	mask, err := parseNumber(spelled)
	if err != nil {
		return Partition{}, fmt.Errorf("%w: %q names no partition mask: %w", ErrTopology, text, err)
	}
	if mask == 0 {
		return Partition{}, fmt.Errorf("%w: %q is the whole key space, which renders as nothing at all rather than as a mask of zero", ErrTopology, text)
	}
	return NewPartition(id, mask)
}

// The refusal is the caller's to wrap, so a number that is not one reads as one
// sentence rather than as the same sentinel twice.
func parseNumber(text string) (uint32, error) {
	if text == "" {
		return 0, errors.New("it is empty")
	}
	if len(text) > 1 && text[0] == '0' {
		return 0, fmt.Errorf("%q carries a leading zero and nothing here renders one", text)
	}
	held, err := strconv.ParseUint(text, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number within 32 bits", text)
	}
	return uint32(held), nil
}

func (this Partition) Matches(sequence string) bool {
	return this.mask == 0 || this.mask&hash(sequence) == this.id
}

// The one topology change there is, and the arithmetic is Axon's Segment.split:
// the lower child keeps the parent's id under the finer mask and the higher one
// takes the bit the finer mask added. Every key the parent matched is matched by
// exactly one of them and no key of any other partition moves, which is the whole
// difference between this and a modulus.
func (this Partition) Split() (Partition, Partition, error) {
	mask := this.mask<<1 + 1
	if uint64(mask)+1 > MaxPartitions {
		return Partition{}, Partition{}, fmt.Errorf("%w: splitting %s asks for %d partitions and MaxPartitions is %d", ErrTopology, this.described(), uint64(mask)+1, MaxPartitions)
	}
	return Partition{id: this.id, mask: mask}, Partition{id: this.id + (mask ^ this.mask), mask: mask}, nil
}

func (this Partition) Mask() uint32 { return this.mask }

func (this Partition) ID() uint32 { return this.id }

// How many partitions a set at this mask has, for a reader. It is stored nowhere
// and nothing computes a partition from it.
func (this Partition) Count() int { return int(this.mask) + 1 }

func (this Partition) Whole() bool { return this.mask == 0 }

func (this Partition) String() string {
	if this.Whole() {
		return ""
	}
	return strconv.FormatUint(uint64(this.id), 10) + partitionSeparator + strconv.FormatUint(uint64(this.mask), 10)
}

// The whole key space renders as nothing at all, which is right for a name and
// useless in a refusal.
func (this Partition) described() string {
	if this.Whole() {
		return "the whole key space"
	}
	return this.String()
}

// FNV-1a/32 over the UTF-8 bytes of the sequence key. Published, and never
// changed: changing it moves every key at once, which is the modulus failure by
// another door. It is the same rule as a composed key — a hash is a wire format.
func hash(sequence string) uint32 {
	const (
		offset uint32 = 2166136261
		prime  uint32 = 16777619
	)
	held := offset
	for index := range len(sequence) {
		held ^= uint32(sequence[index])
		held *= prime
	}
	return held
}
