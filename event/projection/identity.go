package projection

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/frostgrove/vv/event"
)

type Generation uint32

// Renders nothing at all, so every projection that exists today is generation
// zero and its name and its checkpoint row do not move.
const Ungenerated Generation = 0

const (
	generationMark = "@"
	partitionMark  = "#"

	// The mark on the name of the row a split writes to record that it retired an
	// identity. It is built on partitionMark, so no identity renders it back — a
	// partition renders digits around a dot — and a rendered identity is injective,
	// so two retirement rows share a name only if two identities do.
	retiredMark = partitionMark + "split"
)

// Rendered into every name a projection records through — the checkpoint row key
// and the runner name — and parsed back out of one: "orders", "orders@2",
// "orders#3.7", "orders@2#3.7". Within event.MaxNameBytes and carrying neither
// bracket, so it passes the kernel's identifier rule.
//
// It has no exported fields, because Identity{Projection: "orders@2"} would
// render a string that parses back as another identity, and that string is the
// primary key of a checkpoint row, a park and a runner at once. NewIdentity
// refuses "@" and "#" in a projection name for the same reason, rather than
// escaping them: escaping would change the rendered name of a projection that is
// legal today, which moves its checkpoint row and restarts it at the origin
// against a live read model.
//
// The zero value is therefore the one Identity a caller can produce without
// NewIdentity, and it is the one value NewIdentity never answers beside a nil
// error: an empty projection is its first refusal. Projection() == "" tells the
// two apart exactly, which is why no marker field is carried, and every door in
// this phase that takes an Identity refuses that one.
type Identity struct {
	projection string
	generation Generation
	partition  Partition
}

// The kernel's identifier rule is duplicated below rather than delegated to,
// because an Identity is an input at doors that never reach event.Track — a
// parked letter, a redrive, an observation, a cutover — and the phase that adds
// them may not widen the event surface with a checker to call. unnameable and
// event.checkName are pinned to one another by
// TestNewIdentityRefusesEveryNameTheKernelRefuses, which walks both over one
// table and reports a name only one of them takes.
func NewIdentity(projection string, generation Generation, partition Partition) (Identity, error) {
	if broken := unnameable(projection); broken != "" {
		return Identity{}, fmt.Errorf("%w: Name %s, and a checkpoint row is keyed by the name a projection records through", ErrSpec, broken)
	}
	if found := strings.IndexAny(projection, generationMark+partitionMark); found >= 0 {
		return Identity{}, fmt.Errorf("%w: Name carries %q, which separates a projection from its generation and its partition. Escaping it instead would move the checkpoint row of a projection that is legal today and resume it from the origin against a live read model, so the migration is a rename, which is a rebuild",
			ErrSpec, projection[found:found+1])
	}
	held := Identity{projection: projection, generation: generation, partition: partition}
	if rendered := held.String(); len(rendered) > event.MaxNameBytes {
		return Identity{}, fmt.Errorf("%w: Name renders %d bytes beside its generation and its partition, and the kernel's identifier bound is %d", ErrSpec, len(rendered), event.MaxNameBytes)
	}
	return held, nil
}

// The kernel's rule, phrase for phrase: non-empty, within the bound, valid
// UTF-8, no control character, no bracket. There is no trimming and no case
// folding here either — a name compares as bytes, so a name of two spaces is one
// the kernel takes and this takes it too, and a difference in either direction is
// what the drift test reports.
func unnameable(name string) string {
	if name == "" {
		return "is empty"
	}
	if len(name) > event.MaxNameBytes {
		return "is longer than its limit"
	}
	for index := 0; index < len(name); {
		decoded, size := utf8.DecodeRuneInString(name[index:])
		if decoded == utf8.RuneError && size <= 1 {
			return "is not valid UTF-8"
		}
		if unicode.IsControl(decoded) {
			return "contains a control character"
		}
		index += size
	}
	if strings.ContainsAny(name, "[]") {
		return "contains a bracket, which closes the field a refusal's renderer opened and writes a second one after it"
	}
	return ""
}

// Refuses every string String never produces — "orders@0", "orders#0.0",
// "orders#", a generation with a leading zero, a mask that is not one — because
// injectivity is what makes a rendered name safe as a primary key.
func ParseIdentity(text string) (Identity, error) {
	rest, partition := text, Whole()
	if found := strings.Index(rest, partitionMark); found >= 0 {
		held, err := ParsePartition(rest[found+len(partitionMark):])
		if err != nil {
			return Identity{}, fmt.Errorf("%w: %q names no partition an identity renders: %w", ErrSpec, text, err)
		}
		if held.Whole() {
			return Identity{}, fmt.Errorf("%w: %q carries %q for the whole key space, which renders nothing at all", ErrSpec, text, partitionMark)
		}
		rest, partition = rest[:found], held
	}
	generation := Ungenerated
	if found := strings.Index(rest, generationMark); found >= 0 {
		held, err := parseGeneration(rest[found+len(generationMark):])
		if err != nil {
			return Identity{}, fmt.Errorf("%w: %q names no generation an identity renders: %w", ErrSpec, text, err)
		}
		rest, generation = rest[:found], held
	}
	return NewIdentity(rest, generation, partition)
}

func parseGeneration(text string) (Generation, error) {
	held, err := parseNumber(text)
	if err != nil {
		return Ungenerated, err
	}
	if Generation(held) == Ungenerated {
		return Ungenerated, fmt.Errorf("generation %d is Ungenerated, which renders nothing at all", held)
	}
	return Generation(held), nil
}

func (this Identity) Projection() string { return this.projection }

func (this Identity) Generation() Generation { return this.generation }

func (this Identity) Partition() Partition { return this.partition }

// The same projection and generation without the partition. It is what a park
// and a redrive are keyed by: a partition-keyed park orphans its letters the
// moment the partition holding them is split, and the following events of a
// parked sequence then go straight to a handler.
func (this Identity) Whole() Identity {
	this.partition = Whole()
	return this
}

// Every share of the key space this one is a fraction of, coarsest first: a
// runner at {2,3} is inside {0,1} is inside the whole. It is what a resume asks
// the store about, because only the first start of a projection may choose its
// topology, and a row for a coarser share is a second writer over every key of
// this one. It is empty for a projection that named no partition, so the runner
// every deployment is already running asks nothing extra.
func (this Identity) coarser() []Identity {
	var held []Identity
	for mask := uint32(0); mask < this.partition.mask; mask = mask<<1 + 1 {
		held = append(held, Identity{
			projection: this.projection,
			generation: this.generation,
			partition:  Partition{id: this.partition.id & mask, mask: mask},
		})
	}
	return held
}

// Whether a split can record this identity's retirement at all. The mark is
// bytes on top of a rendered name and the kernel's ceiling covers the whole row
// key, so a name within six bytes of it has no room for one. A split of such a
// name is refused rather than performed unrecorded, which is what lets a resume
// that finds no retirement row conclude there is none.
func retirable(identity Identity) bool {
	return len(identity.String())+len(retiredMark) <= event.MaxNameBytes
}

func (this Identity) String() string {
	rendered := this.projection
	if this.generation != Ungenerated {
		rendered += generationMark + strconv.FormatUint(uint64(this.generation), 10)
	}
	if !this.partition.Whole() {
		rendered += partitionMark + this.partition.String()
	}
	return rendered
}
