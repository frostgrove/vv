package event

// The value that authorises an append at a version, and it exists only where the
// framework produced it: Load and a successful Append. A composite literal of it
// compiles — unexported fields are not unconstructibility — so the claim rests on
// what Append does with one rather than on the type: the key of a forged token is
// empty and the kernel's key rules run before anything else, and its backing is
// invalid and matches nothing, including another invalid one.
type At[S any] struct {
	stream  Stream
	version Version
	backing Backing
}

func (this At[S]) Stream() Stream { return this.stream }

func (this At[S]) Version() Version { return this.version }

// What an append wrote, assembled by the kernel from what it already knows —
// the store's Append answers with an error and nothing else, so nothing sealed
// crosses the store boundary in the store's direction.
//
// Every accessor answers on an empty receipt rather than panicking, because a
// caller must not have to know its own decision was a no-op before it may ask a
// question. An empty one reports the token's stream, First 0, Last the version
// the token went in at, Count 0, and the invalid authority: an append that wrote
// nothing was written through nobody's transaction, so it is atomic with
// nothing. Two subsystems proving they wrote together ask Repo.Authority.
type Commit struct {
	stream    Stream
	first     Version
	last      Version
	count     int
	authority Authority
}

func (this Commit) Empty() bool { return this.count == 0 }

func (this Commit) Stream() Stream { return this.stream }

func (this Commit) First() Version { return this.first }

func (this Commit) Last() Version { return this.last }

func (this Commit) Count() int { return this.count }

func (this Commit) Authority() Authority { return this.authority }
