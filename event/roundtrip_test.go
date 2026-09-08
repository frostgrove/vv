package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"math/big"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A reader and a writer over one reusable buffer each, which is the shape a
// hand-rolled binary codec takes. Reusing the buffer it encodes into is
// permitted and the kernel copies what it keeps; reusing the buffer it decodes
// into is the one obligation the codec contract states that no signature can
// hold, and it is what the round trip is here to catch.
type scratchCodec struct {
	scratch [32]byte
	written [32]byte
}

func (*scratchCodec) CanEncode() error { return nil }

const scratchWidth = 8

func (this *scratchCodec) Encode(value note) ([]byte, error) {
	this.written = [32]byte{}
	copy(this.written[:scratchWidth], value.Body)
	return this.written[:scratchWidth], nil
}

func (this *scratchCodec) Decode(payload []byte) (note, error) {
	this.scratch = [32]byte{}
	copy(this.scratch[:scratchWidth], payload)
	return note{Body: this.scratch[:scratchWidth]}, nil
}

// The same obligation broken the way an ordinary implementation breaks it: one
// buffer, sliced to the width of the payload and copied into, never cleared.
// The zero value's encoding is shorter than any sample's, so it cannot reach
// the bytes the first answer points at — which is every proxy that works by
// disturbing the buffer.
type prefixCodec struct{ buffer [64]byte }

func (*prefixCodec) CanEncode() error { return nil }

func (*prefixCodec) Encode(value note) ([]byte, error) {
	return append([]byte{byte(len(value.Body))}, value.Body...), nil
}

func (this *prefixCodec) Decode(payload []byte) (note, error) {
	width, err := prefixWidth(payload)
	if err != nil {
		return note{}, err
	}
	copy(this.buffer[:width], payload[1:])
	return note{Body: this.buffer[:width]}, nil
}

// A payload keeping its contents where the aliasing walk cannot see them, and
// declaring the Equal that lets the fidelity half answer for it anyway — so what
// this case pins is that a shared buffer behind an unexported field is found,
// not that such a payload is refused as uncomparable.
type sealedNote struct{ body []byte }

func (this sealedNote) Equal(other sealedNote) bool { return bytes.Equal(this.body, other.body) }

// One buffer to encode into and one to decode into, and the decoded value
// reaches the second through a field no walk over exported fields compares —
// which is what a zero-copy reader over its own scratch space is, a string built
// on it or a slice it keeps private. Reusing the encode buffer is permitted and
// the kernel copies what it keeps; the decode buffer is the obligation, and only
// disturbing it finds this one.
type sealedCodec struct {
	written [32]byte
	scratch [32]byte
}

func (*sealedCodec) CanEncode() error { return nil }

func (this *sealedCodec) Encode(value sealedNote) ([]byte, error) {
	this.written = [32]byte{}
	this.written[0] = byte(len(value.body))
	copy(this.written[1:], value.body)
	return this.written[:1+len(value.body)], nil
}

func (this *sealedCodec) Decode(payload []byte) (sealedNote, error) {
	width, err := prefixWidth(payload)
	if err != nil {
		return sealedNote{}, err
	}
	this.scratch = [32]byte{}
	copy(this.scratch[:width], payload[1:])
	return sealedNote{body: this.scratch[:width]}, nil
}

// The same wire format and the same reused encode buffer, decoding into memory
// of its own.
type sealingCodec struct{ sealedCodec }

func (*sealingCodec) Decode(payload []byte) (sealedNote, error) {
	width, err := prefixWidth(payload)
	if err != nil {
		return sealedNote{}, err
	}
	return sealedNote{body: bytes.Clone(payload[1 : 1+width])}, nil
}

// A reader that unescapes in place, which is what a codec written for speed does
// with a buffer it was told it owns. Every Decode the kernel makes is given a
// buffer of the kernel's own — at the fold and twice inside the round trip — so
// consuming it is ordinary.
type unescapingCodec struct{}

func (unescapingCodec) CanEncode() error { return nil }

func (unescapingCodec) Encode(value note) ([]byte, error) {
	written := make([]byte, 0, 2*len(value.Body))
	for _, held := range value.Body {
		if held == '\\' {
			written = append(written, '\\')
		}
		written = append(written, held)
	}
	return written, nil
}

func (unescapingCodec) Decode(payload []byte) (note, error) {
	width := 0
	for index := 0; index < len(payload); index++ {
		if payload[index] == '\\' {
			index++
			if index == len(payload) {
				return note{}, errors.New("an escape is cut short at the end of the payload")
			}
		}
		payload[width] = payload[index]
		width++
	}
	return note{Body: payload[:width]}, nil
}

type counted struct{ Counts map[string]int64 }

// The same obligation broken through a map instead of a buffer, and broken the
// way an ordinary implementation breaks it: one scratch map, filled and never
// cleared, so the entries the application already folded into its state are
// rewritten by the next payload the codec reads and the zero value disturbs
// nothing.
type scratchMapCodec struct{ scratch map[string]int64 }

func (*scratchMapCodec) CanEncode() error { return nil }

func (*scratchMapCodec) Encode(value counted) ([]byte, error) { return json.Marshal(value) }

func (this *scratchMapCodec) Decode(payload []byte) (counted, error) {
	var read counted
	if err := json.Unmarshal(payload, &read); err != nil {
		return counted{}, err
	}
	if this.scratch == nil {
		this.scratch = map[string]int64{}
	}
	maps.Copy(this.scratch, read.Counts)
	return counted{Counts: this.scratch}, nil
}

type listed struct {
	N    int
	Tags []string
}

// A codec that answers a payload carrying no tags with one shared empty slice.
// Every answer it gives holds the same address and nothing can be written
// through any of them, which is what a pointer comparison alone calls reuse.
type emptyingCodec struct{ scratch [4]string }

func (*emptyingCodec) CanEncode() error { return nil }

func (*emptyingCodec) Encode(value listed) ([]byte, error) { return json.Marshal(value) }

func (this *emptyingCodec) Decode(payload []byte) (listed, error) {
	var read listed
	err := json.Unmarshal(payload, &read)
	if len(read.Tags) == 0 {
		read.Tags = this.scratch[:0]
	}
	return read, err
}

// The same obligation broken one hop further in than a field holding a slice
// directly: the memory two answers share is an element of a slice, a value of a
// map, and a field behind a pointer. That is what a compact binary format
// decodes into, and none of the three is reachable by comparing the two answers
// where they stand.
type chunked struct{ Chunks [][]byte }

type parted struct{ Parts map[string][]byte }

type bodied struct{ Body []byte }

type pocketed struct{ Held *bodied }

// One count of frames, then a length-prefixed frame each. Every payload of the
// three encodes its zero value as a count of nought, whose decode writes nothing
// at all, so the buffer the first answer points at is never disturbed and the
// two answers themselves are the only evidence of what they share.
func withFrame(written, held []byte) []byte {
	return append(append(written, byte(len(held))), held...)
}

type unframing struct {
	payload []byte
	at      int
	used    int
}

func (this *unframing) count() (int, error) {
	if len(this.payload) == 0 {
		return 0, errors.New("a payload carrying no frame count is not this format")
	}
	this.at = 1
	return int(this.payload[0]), nil
}

func (this *unframing) next() ([]byte, error) {
	if this.at >= len(this.payload) {
		return nil, errors.New("a frame carrying no length is not this format")
	}
	width := int(this.payload[this.at])
	this.at++
	if this.at+width > len(this.payload) {
		return nil, errors.New("the declared length is not the length that arrived")
	}
	held := this.payload[this.at : this.at+width]
	this.at += width
	return held, nil
}

// Into a buffer of the codec's own where it was given one, filled from the front
// on every decode and never cleared, and into memory of its own where it was
// not — which is the whole difference between each pair of codecs below.
func (this *unframing) keep(buffer, held []byte) ([]byte, error) {
	if buffer == nil {
		return bytes.Clone(held), nil
	}
	if this.used+len(held) > len(buffer) {
		return nil, errors.New("the payload is wider than the buffer this codec reuses")
	}
	kept := buffer[this.used : this.used+len(held)]
	copy(kept, held)
	this.used += len(held)
	return kept, nil
}

func writeChunks(value chunked) ([]byte, error) {
	written := []byte{byte(len(value.Chunks))}
	for _, held := range value.Chunks {
		written = withFrame(written, held)
	}
	return written, nil
}

func readChunks(payload, buffer []byte) (chunked, error) {
	read := unframing{payload: payload}
	count, err := read.count()
	if err != nil {
		return chunked{}, err
	}
	held := chunked{}
	for range count {
		body, err := read.next()
		if err != nil {
			return chunked{}, err
		}
		kept, err := read.keep(buffer, body)
		if err != nil {
			return chunked{}, err
		}
		held.Chunks = append(held.Chunks, kept)
	}
	return held, nil
}

type chunkingCodec struct{ buffer [64]byte }

func (*chunkingCodec) CanEncode() error { return nil }

func (*chunkingCodec) Encode(value chunked) ([]byte, error) { return writeChunks(value) }

func (this *chunkingCodec) Decode(payload []byte) (chunked, error) {
	return readChunks(payload, this.buffer[:])
}

type chunkCloningCodec struct{}

func (chunkCloningCodec) CanEncode() error { return nil }

func (chunkCloningCodec) Encode(value chunked) ([]byte, error) { return writeChunks(value) }

func (chunkCloningCodec) Decode(payload []byte) (chunked, error) { return readChunks(payload, nil) }

func writeParts(value parted) ([]byte, error) {
	written := []byte{byte(len(value.Parts))}
	for _, name := range slices.Sorted(maps.Keys(value.Parts)) {
		written = withFrame(withFrame(written, []byte(name)), value.Parts[name])
	}
	return written, nil
}

func readParts(payload, buffer []byte) (parted, error) {
	read := unframing{payload: payload}
	count, err := read.count()
	if err != nil {
		return parted{}, err
	}
	held := parted{Parts: map[string][]byte{}}
	for range count {
		name, err := read.next()
		if err != nil {
			return parted{}, err
		}
		body, err := read.next()
		if err != nil {
			return parted{}, err
		}
		kept, err := read.keep(buffer, body)
		if err != nil {
			return parted{}, err
		}
		held.Parts[string(name)] = kept
	}
	return held, nil
}

type partingCodec struct{ buffer [64]byte }

func (*partingCodec) CanEncode() error { return nil }

func (*partingCodec) Encode(value parted) ([]byte, error) { return writeParts(value) }

func (this *partingCodec) Decode(payload []byte) (parted, error) {
	return readParts(payload, this.buffer[:])
}

type partCloningCodec struct{}

func (partCloningCodec) CanEncode() error { return nil }

func (partCloningCodec) Encode(value parted) ([]byte, error) { return writeParts(value) }

func (partCloningCodec) Decode(payload []byte) (parted, error) { return readParts(payload, nil) }

func writePocket(value pocketed) ([]byte, error) {
	if value.Held == nil {
		return []byte{0}, nil
	}
	return withFrame([]byte{1}, value.Held.Body), nil
}

func readPocket(payload, buffer []byte) (pocketed, error) {
	read := unframing{payload: payload}
	count, err := read.count()
	if err != nil {
		return pocketed{}, err
	}
	if count == 0 {
		return pocketed{}, nil
	}
	body, err := read.next()
	if err != nil {
		return pocketed{}, err
	}
	kept, err := read.keep(buffer, body)
	if err != nil {
		return pocketed{}, err
	}
	return pocketed{Held: &bodied{Body: kept}}, nil
}

type pocketingCodec struct{ buffer [64]byte }

func (*pocketingCodec) CanEncode() error { return nil }

func (*pocketingCodec) Encode(value pocketed) ([]byte, error) { return writePocket(value) }

func (this *pocketingCodec) Decode(payload []byte) (pocketed, error) {
	return readPocket(payload, this.buffer[:])
}

type pocketCloningCodec struct{}

func (pocketCloningCodec) CanEncode() error { return nil }

func (pocketCloningCodec) Encode(value pocketed) ([]byte, error) { return writePocket(value) }

func (pocketCloningCodec) Decode(payload []byte) (pocketed, error) { return readPocket(payload, nil) }

type floatCodec struct{}

func (floatCodec) CanEncode() error { return nil }

func (floatCodec) Encode(value reading) ([]byte, error) {
	return []byte(strconv.FormatFloat(value.Rate, 'g', -1, 64)), nil
}

func (floatCodec) Decode(payload []byte) (reading, error) {
	rate, err := strconv.ParseFloat(string(payload), 64)
	return reading{Rate: rate}, err
}

type roundingCodec struct{}

func (roundingCodec) CanEncode() error { return nil }

func (roundingCodec) Encode(value reading) ([]byte, error) {
	return []byte(strconv.FormatInt(int64(value.Rate), 10)), nil
}

func (roundingCodec) Decode(payload []byte) (reading, error) {
	whole, err := strconv.ParseInt(string(payload), 10, 64)
	return reading{Rate: float64(whole)}, err
}

// A payload type whose Equal answers a question of its own, at the one position
// the comparison asks the name at all. Calling it would panic rather than
// compare, so the signature is checked before the name is trusted.
type weighed struct{ amount int64 }

func (this weighed) Equal(first, second int64) bool { return first == second }

type weighingCodec struct{}

func (weighingCodec) CanEncode() error { return nil }

func (weighingCodec) Encode(value weighed) ([]byte, error) {
	return []byte(strconv.FormatInt(value.amount, 10)), nil
}

func (weighingCodec) Decode(payload []byte) (weighed, error) {
	amount, err := strconv.ParseInt(string(payload), 10, 64)
	return weighed{amount: amount}, err
}

// A codec that loses the same digit on the way out and on the way in, so its own
// bytes say nothing is wrong and only comparing the two values does — which is
// why the wire is asked behind the walk and never instead of it.
type coarseCodec struct{}

func (coarseCodec) CanEncode() error { return nil }

func (coarseCodec) Encode(value weighed) ([]byte, error) {
	return []byte(strconv.FormatInt(value.amount/10, 10)), nil
}

func (coarseCodec) Decode(payload []byte) (weighed, error) {
	amount, err := strconv.ParseInt(string(payload), 10, 64)
	return weighed{amount: 10 * amount}, err
}

// A field of pointer type whose own type answers Equal on the pointer receiver,
// which is the ordinary Go idiom and a nil receiver every time the field is
// unset.
type target struct{ ID int }

func (this *target) Equal(other *target) bool { return this.ID == other.ID }

type referring struct {
	Ref  *target
	Note string
}

type unnotedCodec struct{}

func (unnotedCodec) CanEncode() error { return nil }

func (unnotedCodec) Encode(value referring) ([]byte, error) { return json.Marshal(value) }

func (unnotedCodec) Decode(payload []byte) (referring, error) {
	var read referring
	err := json.Unmarshal(payload, &read)
	read.Note = ""
	return read, err
}

// The same payload with the pointer forgotten instead of the note, which is what
// every codec that skips an optional field produces. The field beside it is kept,
// so the struct walk reaches the pointer and one side is set where the other is
// not.
type unreferringCodec struct{}

func (unreferringCodec) CanEncode() error { return nil }

func (unreferringCodec) Encode(value referring) ([]byte, error) { return json.Marshal(value) }

func (unreferringCodec) Decode(payload []byte) (referring, error) {
	var read referring
	err := json.Unmarshal(payload, &read)
	read.Ref = nil
	return read, err
}

// An Equal an application wrote for the application's question: two documents
// under one title are one document to it, and the body is not part of the
// answer. Given the verdict it passes every codec that drops the body.
type document struct{ Title, Body string }

func (this document) Equal(other document) bool { return this.Title == other.Title }

type titlingCodec struct{}

func (titlingCodec) CanEncode() error { return nil }

func (titlingCodec) Encode(value document) ([]byte, error) { return json.Marshal(value) }

func (titlingCodec) Decode(payload []byte) (document, error) {
	var read document
	err := json.Unmarshal(payload, &read)
	return document{Title: read.Title}, err
}

// A value type keeping its state where a field walk cannot reach it, which is
// the shape that makes the type's own Equal the only answer — and an Equal that
// reads the first of those marks, so a codec that reads none back makes it panic
// rather than compare.
type spanned struct{ marks []int64 }

func (this spanned) Equal(other spanned) bool { return this.marks[0] == other.marks[0] }

type unmarkedCodec struct{}

func (unmarkedCodec) CanEncode() error { return nil }

func (unmarkedCodec) Encode(value spanned) ([]byte, error) { return json.Marshal(value.marks) }

func (unmarkedCodec) Decode([]byte) (spanned, error) { return spanned{}, nil }

type markedCodec struct{}

func (markedCodec) CanEncode() error { return nil }

func (markedCodec) Encode(value spanned) ([]byte, error) { return json.Marshal(value.marks) }

func (markedCodec) Decode(payload []byte) (spanned, error) {
	var read []int64
	err := json.Unmarshal(payload, &read)
	return spanned{marks: read}, err
}

type listing struct{ Marks []int }

func marksOf(count int) listing {
	held := listing{Marks: make([]int, count)}
	for index := range held.Marks {
		held.Marks[index] = index + 1
	}
	return held
}

// A codec that reads one element back wrong, at a position the caller chooses,
// so a comparison that stops early can be asked what it saw and what it did not.
type slippingCodec struct{ at int }

func (slippingCodec) CanEncode() error { return nil }

func (slippingCodec) Encode(value listing) ([]byte, error) { return json.Marshal(value) }

func (this slippingCodec) Decode(payload []byte) (listing, error) {
	var read listing
	err := json.Unmarshal(payload, &read)
	if err == nil && this.at < len(read.Marks) {
		read.Marks[this.at]++
	}
	return read, err
}

// A fact whose whole content is that it happened, and the shape half of event
// sourcing has. Its reader type holds one value, so its only sample encodes as
// its own zero value and there is no other sample a caller could give.
type voided struct{}

// The same fact by a type that forbids ==, which is what a zero-length array of
// a func type is there for. It still holds exactly one value.
type witnessed struct{ _ [0]func() }

type witnessCodec struct{}

func (witnessCodec) CanEncode() error { return nil }

func (witnessCodec) Encode(witnessed) ([]byte, error) { return []byte(`{}`), nil }

func (witnessCodec) Decode([]byte) (witnessed, error) { return witnessed{}, nil }

// The same marker beside data, which is what a fact carrying both looks like.
type witnessedNote struct {
	Mark witnessed
	Body string
}

// A codec that records the first three bytes and reads back exactly what it
// wrote, so its own wire says nothing is wrong and only the field beside the
// marker does.
type trimmingCodec struct{}

func (trimmingCodec) CanEncode() error { return nil }

func (trimmingCodec) Encode(value witnessedNote) ([]byte, error) {
	return json.Marshal(value.Body[:min(len(value.Body), 3)])
}

func (trimmingCodec) Decode(payload []byte) (witnessedNote, error) {
	var body string
	err := json.Unmarshal(payload, &body)
	return witnessedNote{Body: body}, err
}

// A value object that keeps its state privately, is compared with == and
// declares no Equal at all — netip.Addr, netip.Prefix and most of what an
// application writes for a money amount or an identifier.
type located struct {
	IP   netip.Addr
	Note string
}

type unlocatingCodec struct{}

func (unlocatingCodec) CanEncode() error { return nil }

func (unlocatingCodec) Encode(value located) ([]byte, error) { return json.Marshal(value) }

func (unlocatingCodec) Decode(payload []byte) (located, error) {
	var read located
	err := json.Unmarshal(payload, &read)
	read.IP = netip.Addr{}
	return read, err
}

// The same shape with nothing that can answer for it: no exported field, no
// Equal, and a slice inside, so == is a compile error and reflect refuses it.
type hushed struct{ marks []int64 }

type hushingCodec struct{}

func (hushingCodec) CanEncode() error { return nil }

func (hushingCodec) Encode(value hushed) ([]byte, error) { return json.Marshal(value.marks) }

func (hushingCodec) Decode([]byte) (hushed, error) { return hushed{marks: []int64{99, 99}}, nil }

// The same wire format read back as it was written.
type murmuringCodec struct{}

func (murmuringCodec) CanEncode() error { return nil }

func (murmuringCodec) Encode(value hushed) ([]byte, error) { return json.Marshal(value.marks) }

func (murmuringCodec) Decode(payload []byte) (hushed, error) {
	var read []int64
	err := json.Unmarshal(payload, &read)
	return hushed{marks: read}, err
}

// An amount recorded exactly, which is the payload an accounting ledger has and
// the shape no method name can reach: every field of a big.Int is unexported, it
// declares no Equal and it holds a slice, so == is refused too — and neither
// repair is one a consumer could perform on a type the standard library owns.
type ledgered struct {
	Amount *big.Int
	Note   string
}

// A codec of the caller's own over the same payload, so the case is not the
// shipped codec being trusted.
type tallyingCodec struct{}

func (tallyingCodec) CanEncode() error { return nil }

func (tallyingCodec) Encode(value ledgered) ([]byte, error) {
	return json.Marshal([2]string{amountText(value.Amount), value.Note})
}

func (tallyingCodec) Decode(payload []byte) (ledgered, error) {
	var read [2]string
	if err := json.Unmarshal(payload, &read); err != nil {
		return ledgered{}, err
	}
	if read[0] == "" {
		return ledgered{Note: read[1]}, nil
	}
	amount, held := new(big.Int).SetString(read[0], 10)
	if !held {
		return ledgered{}, errors.New("the amount that arrived is not a decimal integer")
	}
	return ledgered{Amount: amount, Note: read[1]}, nil
}

func amountText(amount *big.Int) string {
	if amount == nil {
		return ""
	}
	return amount.String()
}

// The same payload through a codec that keeps the note and reads the amount back
// as nothing, which is the difference the wire answers for.
type untalliedCodec struct{}

func (untalliedCodec) CanEncode() error { return nil }

func (untalliedCodec) Encode(value ledgered) ([]byte, error) { return json.Marshal(value) }

func (untalliedCodec) Decode(payload []byte) (ledgered, error) {
	var read ledgered
	err := json.Unmarshal(payload, &read)
	read.Amount = big.NewInt(0)
	return read, err
}

// A payload holding a function. No wire format records one and Go compares no
// two, so both answers a round trip has are silent for it.
type routed struct {
	Do   func() int
	Note string
}

type routingCodec struct{ reads func() int }

func (routingCodec) CanEncode() error { return nil }

func (routingCodec) Encode(value routed) ([]byte, error) { return json.Marshal(value.Note) }

func (this routingCodec) Decode(payload []byte) (routed, error) {
	var note string
	err := json.Unmarshal(payload, &note)
	return routed{Do: this.reads, Note: note}, err
}

// The same payload losing the note as well, so two conditions the walk carries
// out rather than resolves are true of one value at once and which repair the
// caller is told about is decided by the order they were found in.
type unroutedCodec struct{ reads func() int }

func (unroutedCodec) CanEncode() error { return nil }

func (unroutedCodec) Encode(value routed) ([]byte, error) { return json.Marshal(value.Note) }

func (this unroutedCodec) Decode([]byte) (routed, error) { return routed{Do: this.reads}, nil }

// The repair that refusal names: what identifies the behaviour is recorded, and
// the fold chooses the function from it.
type named struct {
	Do   string
	Note string
}

// A channel takes the same arm and is comparable, so == answers for it and a
// codec that hands back another channel is a difference like any other.
type piped struct {
	Feed chan int
	Note string
}

type pipingCodec struct{}

func (pipingCodec) CanEncode() error { return nil }

func (pipingCodec) Encode(value piped) ([]byte, error) { return json.Marshal(value.Note) }

func (pipingCodec) Decode(payload []byte) (piped, error) {
	var note string
	err := json.Unmarshal(payload, &note)
	return piped{Feed: make(chan int), Note: note}, err
}

// And the same again with an Equal declared on the pointer receiver, which is
// not in the value's method set and is therefore never found.
type heldTight struct{ marks []int64 }

func (this *heldTight) Equal(other *heldTight) bool { return len(this.marks) == len(other.marks) }

type looseningCodec struct{}

func (looseningCodec) CanEncode() error { return nil }

func (looseningCodec) Encode(value heldTight) ([]byte, error) { return json.Marshal(value.marks) }

func (looseningCodec) Decode([]byte) (heldTight, error) { return heldTight{}, nil }

// A payload whose one field is an interface — the shape the shipped codec
// refuses at declaration, telling the caller to declare a codec of their own,
// and therefore the shape Codec[V] exists to admit. What it reads back is
// whatever the case under test needs, so one fixture covers a faithful read and
// every substitution of one value for another.
type boxed struct{ Meta any }

type narrow struct{ A int }

type wide struct{ A, B, C int }

type boxingCodec struct{ reads func() any }

func (boxingCodec) CanEncode() error { return nil }

func (boxingCodec) Encode(value boxed) ([]byte, error) { return json.Marshal(value.Meta) }

func (this boxingCodec) Decode([]byte) (boxed, error) { return boxed{Meta: this.reads()}, nil }

// Two answers for one payload, in the order the round trip asks for them: the
// aliasing half decodes the same bytes twice and compares what came back, so a
// codec whose two answers are of different types is what drives that walk into
// a value of a shape it did not come from.
func inTurn(first, second any) func() any {
	answered := false
	return func() any {
		if answered {
			return second
		}
		answered = true
		return first
	}
}

type allocatingCodec struct{}

func (allocatingCodec) CanEncode() error { return nil }

func (allocatingCodec) Encode(value note) ([]byte, error) {
	return append([]byte{byte(len(value.Body))}, value.Body...), nil
}

func (allocatingCodec) Decode(payload []byte) (note, error) {
	width, err := prefixWidth(payload)
	if err != nil {
		return note{}, err
	}
	return note{Body: bytes.Clone(payload[1 : 1+width])}, nil
}

func prefixWidth(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, errors.New("a payload carrying no length is not this format")
	}
	width := int(payload[0])
	if width > len(payload)-1 || width > 64 {
		return 0, errors.New("the declared length is not the length that arrived")
	}
	return width, nil
}

// A codec that records less than it was handed. Nothing about the bytes is
// wrong, nothing is aliased, and the fact reads back as an ordinary one forever.
type truncatingCodec struct{}

func (truncatingCodec) CanEncode() error { return nil }

func (truncatingCodec) Encode(value note) ([]byte, error) {
	return bytes.Clone(value.Body[:min(len(value.Body), 3)]), nil
}

func (truncatingCodec) Decode(payload []byte) (note, error) {
	return note{Body: bytes.Clone(payload)}, nil
}

// Four codecs that keep the shape and change one ordinary thing each: the unit
// mix-up that writes minor units and reads major, a string clipped to a column
// width, a map entry the writer skips, and a value behind a pointer. Nothing
// about the bytes is wrong, every entry that arrives is the entry it was given,
// and only comparing the two values says what was lost.
type driftingCodec struct{}

func (driftingCodec) CanEncode() error { return nil }

func (driftingCodec) Encode(value creditedV2) ([]byte, error) {
	value.Minor++
	return json.Marshal(value)
}

func (driftingCodec) Decode(payload []byte) (creditedV2, error) {
	var read creditedV2
	err := json.Unmarshal(payload, &read)
	return read, err
}

type clippingCodec struct{}

func (clippingCodec) CanEncode() error { return nil }

func (clippingCodec) Encode(value creditedV2) ([]byte, error) {
	value.Reason = value.Reason[:min(len(value.Reason), 3)]
	return json.Marshal(value)
}

func (clippingCodec) Decode(payload []byte) (creditedV2, error) {
	var read creditedV2
	err := json.Unmarshal(payload, &read)
	return read, err
}

type droppingCodec struct{}

func (droppingCodec) CanEncode() error { return nil }

func (droppingCodec) Encode(value counted) ([]byte, error) {
	kept := maps.Clone(value.Counts)
	delete(kept, "drop")
	return json.Marshal(counted{Counts: kept})
}

func (droppingCodec) Decode(payload []byte) (counted, error) {
	var read counted
	err := json.Unmarshal(payload, &read)
	return read, err
}

type totalled struct{ Total int64 }

type totalling struct{ Held *totalled }

type slantingCodec struct{}

func (slantingCodec) CanEncode() error { return nil }

func (slantingCodec) Encode(value totalling) ([]byte, error) { return json.Marshal(value) }

func (slantingCodec) Decode(payload []byte) (totalling, error) {
	var read totalling
	err := json.Unmarshal(payload, &read)
	if read.Held != nil {
		read.Held.Total++
	}
	return read, err
}

// Two codecs that keep the count and lose the place: the key normalisation
// somebody adds for a database column, and an amount changed one level in from
// the scalar. Both write every entry they were given and read every entry back,
// so a length is equal on both sides and only looking up each key in the value
// beside it says one is not the other.
type rekeyingCodec struct{}

func (rekeyingCodec) CanEncode() error { return nil }

func (rekeyingCodec) Encode(value counted) ([]byte, error) { return json.Marshal(value) }

func (rekeyingCodec) Decode(payload []byte) (counted, error) {
	var read counted
	if err := json.Unmarshal(payload, &read); err != nil {
		return counted{}, err
	}
	renamed := make(map[string]int64, len(read.Counts))
	for name, held := range read.Counts {
		renamed[strings.ToUpper(name)] = held
	}
	return counted{Counts: renamed}, nil
}

type inflatingCodec struct{}

func (inflatingCodec) CanEncode() error { return nil }

func (inflatingCodec) Encode(value counted) ([]byte, error) { return json.Marshal(value) }

func (inflatingCodec) Decode(payload []byte) (counted, error) {
	var read counted
	if err := json.Unmarshal(payload, &read); err != nil {
		return counted{}, err
	}
	for name := range read.Counts {
		read.Counts[name] *= 100
	}
	return read, nil
}

// A codec that forgets a field whose own type keeps its state where no walk can
// reach it. Every field of a time.Time is unexported, so what it lost is visible
// only by asking the type its own question.
type forgetfulCodec struct{}

func (forgetfulCodec) CanEncode() error { return nil }

func (forgetfulCodec) Encode(value stamp) ([]byte, error) {
	return json.Marshal(struct{ Note string }{value.Note})
}

func (forgetfulCodec) Decode(payload []byte) (stamp, error) {
	var read struct{ Note string }
	err := json.Unmarshal(payload, &read)
	return stamp{Note: read.Note}, err
}

// What an encoding drops by design, and what an application keeps beside what
// it records: a time carries a monotonic reading and a location that no wire
// format preserves, and an unexported field is never written and never missed.
type stamp struct {
	At   time.Time
	Note string
}

// A codec that records the instant and hands it back in UTC, which is ordinary
// and is faithful by the only equality a time.Time has. Its answer re-encodes to
// bytes that are not the sample's, so a wire comparison run beside a declared
// Equal rather than behind it would call this a difference.
type utcCodec struct{}

func (utcCodec) CanEncode() error { return nil }

func (utcCodec) Encode(value stamp) ([]byte, error) {
	return json.Marshal(struct{ At, Note string }{value.At.Format(time.RFC3339Nano), value.Note})
}

func (utcCodec) Decode(payload []byte) (stamp, error) {
	var read struct{ At, Note string }
	if err := json.Unmarshal(payload, &read); err != nil {
		return stamp{}, err
	}
	at, err := time.Parse(time.RFC3339Nano, read.At)
	return stamp{At: at.UTC(), Note: read.Note}, err
}

type holding struct {
	Amount    int64
	formatted string
}

type decimalCodec struct{}

func (decimalCodec) CanEncode() error { return nil }

func (decimalCodec) Encode(value creditedV1) ([]byte, error) {
	return []byte(strconv.FormatInt(value.Minor, 10)), nil
}

func (decimalCodec) Decode(payload []byte) (creditedV1, error) {
	minor, err := strconv.ParseInt(string(payload), 10, 64)
	if err != nil {
		return creditedV1{}, err
	}
	return creditedV1{Minor: minor}, nil
}

func declareNotes(t *testing.T, family string, codec Codec[note]) *Fact[carrier, accountID, note] {
	t.Helper()
	aggregate := Define[carrier](family, accountKey)
	return Declare(aggregate, "notes.written", From(codec), carryNote)
}

func roundTripped[E any](family string, codec Codec[E], sample E) error {
	fact := Declare(Define[account](family, accountKey), "notes.reached", From(codec),
		func(this account, _ E) account { return this })
	_, err := fact.RoundTrip(sample)
	return err
}

func TestACodecThatDecodesIntoAReusedBufferIsCaught(t *testing.T) {
	t.Run("a populated sample tells the two codecs apart", func(t *testing.T) {
		reusing := declareNotes(t, "notes.reusing", &scratchCodec{})
		if _, err := reusing.RoundTrip(note{Body: []byte("one")}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec whose decoded value aliases a buffer it rewrites answered %v, so the obligation is a sentence nothing runs", err)
		}
		shipped := declareNotes(t, "notes.shipped", JSON[note]())
		carried, err := shipped.RoundTrip(note{Body: []byte("one")})
		if err != nil {
			t.Fatalf("the shipped codec was refused by the proxy that is supposed to pass it: %v", err)
		}
		if string(carried[0].Body) != "one" {
			t.Fatalf("the round trip carried %q rather than the sample", carried[0].Body)
		}
	})

	t.Run("a codec that reuses one buffer and never clears it is caught too", func(t *testing.T) {
		reusing := declareNotes(t, "notes.prefix.reusing", &prefixCodec{})
		if _, err := reusing.RoundTrip(note{Body: []byte("twelve")}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that hands out a slice of the buffer it decodes into answered %v; the zero value's encoding is shorter than the sample's and never reaches those bytes, so a proxy that disturbs the buffer passes the ordinary implementation of the obligation and catches only a fixture that zeroes its own array", err)
		}
		allocating := declareNotes(t, "notes.prefix.allocating", allocatingCodec{})
		carried, err := allocating.RoundTrip(note{Body: []byte("twelve")})
		if err != nil {
			t.Fatalf("the same format over a codec that allocates per decode was refused (%v), so the case above passes by refusing every binary codec", err)
		}
		if string(carried[0].Body) != "twelve" {
			t.Fatalf("the round trip carried %q rather than the sample", carried[0].Body)
		}
	})

	t.Run("a codec that hands out one map on every decode is caught too", func(t *testing.T) {
		reusing := Define[int]("notes.map.reusing", accountKey)
		shared := Declare(reusing, "notes.counted", From[counted](&scratchMapCodec{}),
			func(this int, event counted) int { return this + len(event.Counts) })
		sample := counted{Counts: map[string]int64{"deposit": 250}}
		if _, err := shared.RoundTrip(sample); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that hands out the map it decodes into answered %v; the next payload it reads rewrites the entries the application already folded into its state", err)
		}
		allocating := Define[int]("notes.map.allocating", accountKey)
		fresh := Declare(allocating, "notes.counted", From(JSON[counted]()),
			func(this int, event counted) int { return this + len(event.Counts) })
		if _, err := fresh.RoundTrip(sample); err != nil {
			t.Fatalf("a codec that allocates a map per decode was refused (%v), so the case above passes by refusing every payload holding a map", err)
		}
	})

	t.Run("a codec that reaches its buffer through a field no comparison walks is caught too", func(t *testing.T) {
		reusing := Define[int]("notes.sealed.reusing", accountKey)
		hidden := Declare(reusing, "notes.sealed", From[sealedNote](&sealedCodec{}),
			func(this int, event sealedNote) int { return this + len(event.body) })
		if _, err := hidden.RoundTrip(sealedNote{body: []byte("twelve")}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec whose decoded value reaches its own reused buffer through an unexported field answered %v; two answers compared by address walk the exported half only, so a zero-copy reader over a scratch buffer is found by nothing but decoding a second payload and re-encoding what the application already holds", err)
		}
		allocating := Define[int]("notes.sealed.allocating", accountKey)
		fresh := Declare(allocating, "notes.sealed", From[sealedNote](&sealingCodec{}),
			func(this int, event sealedNote) int { return this + len(event.body) })
		if _, err := fresh.RoundTrip(sealedNote{body: []byte("twelve")}); err != nil {
			t.Fatalf("the same wire format over a codec that allocates per decode was refused (%v), so the case above passes by refusing every payload whose contents no walk can see", err)
		}
	})

	t.Run("memory shared one hop in is found through a slice element, a map value and a pointer", func(t *testing.T) {
		body := []byte("twelve")
		chunks := chunked{Chunks: [][]byte{body}}
		parts := parted{Parts: map[string][]byte{"body": body}}
		pocket := pocketed{Held: &bodied{Body: body}}
		for _, reached := range []struct {
			what    string
			reusing func() error
			cloning func() error
		}{
			{"an element of a slice",
				func() error { return roundTripped("notes.chunked.reusing", &chunkingCodec{}, chunks) },
				func() error { return roundTripped("notes.chunked.cloning", chunkCloningCodec{}, chunks) }},
			{"a value of a map",
				func() error { return roundTripped("notes.parted.reusing", &partingCodec{}, parts) },
				func() error { return roundTripped("notes.parted.cloning", partCloningCodec{}, parts) }},
			{"a field behind a pointer",
				func() error { return roundTripped("notes.pocketed.reusing", &pocketingCodec{}, pocket) },
				func() error { return roundTripped("notes.pocketed.cloning", pocketCloningCodec{}, pocket) }},
		} {
			err := reached.reusing()
			if !errors.Is(err, ErrPayload) || !strings.Contains(err.Error(), "decoded into memory its codec reuses") {
				t.Fatalf("a codec whose two answers share a buffer reached through %s answered %v; every payload above reaches its bytes through one field holding a slice or a map, so a comparison that walks no further than that arm passes a codec whose chunks, whose parts or whose body the next payload rewrites", reached.what, err)
			}
			if err := reached.cloning(); err != nil {
				t.Fatalf("the same wire format over a codec that allocates per decode was refused for %s (%v), so the case above passes by refusing every payload that reaches its bytes that way; the zero value of each encodes as a count of nought and disturbs nothing, so the walk over the two answers is the whole verdict", reached.what, err)
			}
		}
	})

	t.Run("a codec that consumes the buffer it was handed is handed one of its own", func(t *testing.T) {
		written := declareNotes(t, "notes.unescaping", unescapingCodec{})
		carried, err := written.RoundTrip(note{Body: []byte(`a\b`)})
		if err != nil {
			t.Fatalf("a reader that unescapes in place was refused: %v", err)
		}
		if string(carried[0].Body) != `a\b` {
			t.Fatalf("the round trip carried %q rather than the sample; each of the three decodes is given a buffer of the kernel's own, and two of them sharing one leaves the second reading what the first consumed", carried[0].Body)
		}
	})

	t.Run("an empty slice two answers share is not memory either can write", func(t *testing.T) {
		aggregate := Define[int]("notes.listed", accountKey)
		tagged := Declare(aggregate, "notes.listed", From[listed](&emptyingCodec{}),
			func(this int, event listed) int { return this + event.N })
		if _, err := tagged.RoundTrip(listed{N: 3}); err != nil {
			t.Fatalf("a payload whose slice is empty was reported as a codec reusing its memory: %v; an empty slice has no address of its own and nothing is reachable through it", err)
		}
		if _, err := tagged.RoundTrip(listed{N: 3, Tags: []string{"acme"}}); err != nil {
			t.Fatalf("the same codec was refused for a payload that carries tags (%v), so the case above is not the codec being ignored", err)
		}
	})

	t.Run("a codec that records less than it was handed is caught", func(t *testing.T) {
		truncating := declareNotes(t, "notes.truncating", truncatingCodec{})
		if _, err := truncating.RoundTrip(note{Body: []byte("twelve")}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that wrote three bytes of a six-byte payload answered %v, and nothing else in the round trip compares what came back with what was given", err)
		}
		shipped := declareNotes(t, "notes.whole", JSON[note]())
		if _, err := shipped.RoundTrip(note{Body: []byte("twelve")}); err != nil {
			t.Fatalf("a codec that records what it was given was refused (%v), so the case above passes by refusing every codec", err)
		}

		forgotten := Define[account]("notes.forgotten", accountKey)
		forgetful := Declare(forgotten, "notes.stamped", From[stamp](forgetfulCodec{}),
			func(this account, _ stamp) account { return this })
		if _, err := forgetful.RoundTrip(stamp{At: time.Now(), Note: "x"}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that wrote the note and forgot the time answered %v; every field of a time.Time is unexported, so a comparison that walks fields alone sees nothing and the type's own Equal is what answers", err)
		}
	})

	t.Run("a scalar changed, a map entry dropped and a value behind a pointer are each a difference", func(t *testing.T) {
		credit := creditedV2{Minor: 250, Reason: "deposit"}
		counts := counted{Counts: map[string]int64{"deposit": 250, "drop": 7}}
		total := totalling{Held: &totalled{Total: 250}}
		for _, changed := range []struct {
			what     string
			refused  func() error
			faithful func() error
		}{
			{"an integer, and nothing else",
				func() error { return roundTripped("notes.credited.drifting", driftingCodec{}, credit) },
				func() error { return roundTripped("notes.credited.whole", JSON[creditedV2](), credit) }},
			{"a string, and nothing else",
				func() error { return roundTripped("notes.credited.clipping", clippingCodec{}, credit) },
				func() error { return roundTripped("notes.credited.kept", JSON[creditedV2](), credit) }},
			{"one map entry, every entry it did write being the entry it was given",
				func() error { return roundTripped("notes.counted.dropping", droppingCodec{}, counts) },
				func() error { return roundTripped("notes.counted.every", JSON[counted](), counts) }},
			{"a value reached through a pointer",
				func() error { return roundTripped("notes.totalled.slanting", slantingCodec{}, total) },
				func() error { return roundTripped("notes.totalled.held", JSON[totalling](), total) }},
		} {
			err := changed.refused()
			if !errors.Is(err, ErrPayload) || !strings.Contains(err.Error(), "does not read back") {
				t.Fatalf("a codec that changed %s answered %v; a unit mix-up, a string clipped to a column width and an entry the writer skips are the ordinary ways a codec loses what it was handed, and each is decided by one arm of the value comparison and by nothing else in the round trip", changed.what, err)
			}
			if err := changed.faithful(); err != nil {
				t.Fatalf("the same payload through a codec that kept %s was refused (%v), so the case above passes by refusing every payload of that shape", changed.what, err)
			}
		}
	})

	t.Run("a value read back somewhere else, or not at all, is a difference too", func(t *testing.T) {
		counts := counted{Counts: map[string]int64{"deposit": 250, "fee": 7}}
		reference := referring{Ref: &target{ID: 3}, Note: "hello"}
		for _, lost := range []struct {
			what     string
			refused  func() error
			faithful func() error
		}{
			{"every entry under another key, the count and every amount unchanged",
				func() error { return roundTripped("notes.counted.rekeying", rekeyingCodec{}, counts) },
				func() error { return roundTripped("notes.counted.named", JSON[counted](), counts) }},
			{"every amount multiplied, every key and the count unchanged",
				func() error { return roundTripped("notes.counted.inflating", inflatingCodec{}, counts) },
				func() error { return roundTripped("notes.counted.exact", JSON[counted](), counts) }},
			{"a set pointer field as nothing, the field beside it kept",
				func() error { return roundTripped("notes.referring.unreferring", unreferringCodec{}, reference) },
				func() error { return roundTripped("notes.referring.kept", JSON[referring](), reference) }},
		} {
			err := lost.refused()
			if !errors.Is(err, ErrPayload) || !strings.Contains(err.Error(), "does not read back") {
				t.Fatalf("a codec that read back %s answered %v; all three keep the count the length check compares, so what decides them is looking each key up in the value beside it and calling a pointer one side holds and the other does not a difference — without that, a codec that normalises its keys, one that changes an amount and one that forgets an optional field are each recorded as faithful", lost.what, err)
			}
			if err := lost.faithful(); err != nil {
				t.Fatalf("the same payload through a codec that read it back where it stood was refused (%v), so the case above passes by refusing every payload of that shape", err)
			}
		}
	})

	t.Run("what an encoding drops by design is not what it lost", func(t *testing.T) {
		stamps := Define[account]("notes.stamped", accountKey)
		stamped := Declare(stamps, "notes.stamped", From(JSON[stamp]()),
			func(this account, _ stamp) account { return this })
		for _, sample := range []struct {
			what   string
			sample stamp
		}{
			{"a monotonic reading and a local zone, which no encoding preserves", stamp{At: time.Now(), Note: "x"}},
			{"the same instant with neither", stamp{At: time.Now().UTC(), Note: "x"}},
		} {
			if _, err := stamped.RoundTrip(sample.sample); err != nil {
				t.Fatalf("a payload carrying %s was accused of losing data: %v; time.Time answers its own Equal and the comparison must ask it", sample.what, err)
			}
		}

		holdings := Define[account]("notes.held", accountKey)
		held := Declare(holdings, "notes.held", From(JSON[holding]()),
			func(this account, _ holding) account { return this })
		if _, err := held.RoundTrip(holding{Amount: 5, formatted: "$0.05"}); err != nil {
			t.Fatalf("a payload carrying a populated unexported field was accused of losing data: %v; encoding/json never wrote it and no load was ever going to read it", err)
		}

		rates := Define[float64]("notes.rates", accountKey)
		rate := Declare(rates, "notes.read", From[reading](floatCodec{}),
			func(this float64, event reading) float64 { return this + event.Rate })
		if _, err := rate.RoundTrip(reading{Rate: math.NaN()}); err != nil {
			t.Fatalf("a payload carrying a NaN was accused of losing data: %v; a value that is not equal to itself is what the application recorded and what it reads back", err)
		}
		rounded := Define[float64]("notes.rounded", accountKey)
		whole := Declare(rounded, "notes.read", From[reading](roundingCodec{}),
			func(this float64, event reading) float64 { return this + event.Rate })
		if _, err := whole.RoundTrip(reading{Rate: 2.5}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that wrote 2.5 as 2 answered %v, so the case above passes by comparing no float at all", err)
		}

		weights := Define[account]("notes.weighed", accountKey)
		weight := Declare(weights, "notes.weighed", From[weighed](weighingCodec{}),
			func(this account, _ weighed) account { return this })
		if _, err := weight.RoundTrip(weighed{amount: 5}); err != nil {
			t.Fatalf("a payload whose Equal answers a different question was refused: %v; every field of it is unexported, so the comparison does reach the name, and it has to check the signature before it calls it", err)
		}
		coarse := Define[account]("notes.weighed.coarse", accountKey)
		lost := Declare(coarse, "notes.weighed", From[weighed](coarseCodec{}),
			func(this account, _ weighed) account { return this })
		if _, err := lost.RoundTrip(weighed{amount: 55}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that dropped the units digit on both sides answered %v; what it read back re-encodes to the bytes it was given, so its own wire says nothing is wrong and == on the two values is the only thing that does — which is why the wire is asked behind the walk and never instead of it", err)
		}

		normalising := Define[account]("notes.stamped.utc", accountKey)
		utc := Declare(normalising, "notes.stamped", From[stamp](utcCodec{}),
			func(this account, _ stamp) account { return this })
		if _, err := utc.RoundTrip(stamp{At: time.Now(), Note: "x"}); err != nil {
			t.Fatalf("a codec that recorded the instant and handed it back in UTC was accused of losing data: %v; time.Time says a location is not the difference and its own Equal is the answer at that position, so nothing may be asked beside it — the same instant in another zone encodes to other bytes", err)
		}
	})

	t.Run("a pointer field is not asked its own type's Equal through a nil", func(t *testing.T) {
		aggregate := Define[account]("notes.referring", accountKey)
		referred := Declare(aggregate, "notes.referring", From(JSON[referring]()),
			func(this account, _ referring) account { return this })
		carried, err := referred.RoundTrip(referring{Note: "hello"})
		if err != nil {
			t.Fatalf("a payload whose pointer field is unset answered %v; Equal on a pointer receiver is the ordinary Go idiom, and asking it through a nil pointer is a runtime panic out of an exported method of the kernel with no diagnosis at all", err)
		}
		if carried[0].Ref != nil || carried[0].Note != "hello" {
			t.Fatalf("the round trip carried %+v rather than the sample", carried[0])
		}
		if _, err := referred.RoundTrip(referring{Ref: &target{ID: 3}, Note: "hello"}); err != nil {
			t.Fatalf("the same payload with the pointer set answered %v", err)
		}

		dropping := Define[account]("notes.referring.dropping", accountKey)
		unnoted := Declare(dropping, "notes.referring", From[referring](unnotedCodec{}),
			func(this account, _ referring) account { return this })
		if _, err := unnoted.RoundTrip(referring{Note: "hello"}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that dropped the note beside the unset pointer answered %v, so the two cases above pass by comparing nothing at all", err)
		}
	})

	t.Run("an application's own Equal does not decide whether the codec kept what it was handed", func(t *testing.T) {
		titles := Define[account]("notes.titled", accountKey)
		titled := Declare(titles, "notes.titled", From[document](titlingCodec{}),
			func(this account, _ document) account { return this })
		if _, err := titled.RoundTrip(document{Title: "t", Body: "the body the codec drops"}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that dropped the body answered %v; the payload declares an Equal that compares titles, an application writes Equal for its own question, and given the verdict it disables the one check that says a codec recorded what it was given", err)
		}
		whole := Define[account]("notes.titled.whole", accountKey)
		kept := Declare(whole, "notes.titled", From(JSON[document]()),
			func(this account, _ document) account { return this })
		if _, err := kept.RoundTrip(document{Title: "t", Body: "the body the codec keeps"}); err != nil {
			t.Fatalf("a codec that kept both fields was refused (%v), so the case above passes by refusing every payload that declares an Equal", err)
		}

		stamps := Define[account]("notes.titled.stamped", accountKey)
		stamped := Declare(stamps, "notes.stamped", From(JSON[stamp]()),
			func(this account, _ stamp) account { return this })
		if _, err := stamped.RoundTrip(stamp{At: time.Now(), Note: "x"}); err != nil {
			t.Fatalf("a time.Time carrying a monotonic reading and a local zone was accused of losing data: %v; every field of it is unexported, so its own Equal is the only answer and narrowing where Equal is asked must not stop asking it there", err)
		}
		forgotten := Define[account]("notes.titled.forgotten", accountKey)
		forgetful := Declare(forgotten, "notes.stamped", From[stamp](forgetfulCodec{}),
			func(this account, _ stamp) account { return this })
		if _, err := forgetful.RoundTrip(stamp{At: time.Now(), Note: "x"}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that wrote the note and forgot the time answered %v, so the pair above is closed by not asking Equal anywhere", err)
		}
	})

	t.Run("an Equal that panics refuses the sample rather than unwinding", func(t *testing.T) {
		dropping := Define[account]("notes.spanned.dropping", accountKey)
		spans := Declare(dropping, "notes.spanned", From[spanned](unmarkedCodec{}),
			func(this account, _ spanned) account { return this })
		_, err := spans.RoundTrip(spanned{marks: []int64{5}})
		if !errors.Is(err, ErrSample) {
			t.Fatalf("a payload whose own Equal panicked when the comparison asked it answered %v; Equal is a call into code the framework was promised nothing about, and unrecovered it reaches the caller as a runtime panic out of the kernel", err)
		}
		if !strings.Contains(err.Error(), "event.spanned") {
			t.Fatalf("the refusal is %q and never names the type whose Equal panicked, which is the only thing that tells the caller where to look", err)
		}
		keeping := Define[account]("notes.spanned.keeping", accountKey)
		marked := Declare(keeping, "notes.spanned", From[spanned](markedCodec{}),
			func(this account, _ spanned) account { return this })
		if _, err := marked.RoundTrip(spanned{marks: []int64{5}}); err != nil {
			t.Fatalf("the same type over a codec that reads its marks back was refused (%v), so the case above passes by refusing every payload whose Equal is asked", err)
		}
	})

	t.Run("a sample larger than the walk is refused rather than reported as a pass", func(t *testing.T) {
		for _, slipped := range []struct {
			family string
			n, at  int
		}{
			{"notes.listing.near", 10, 5},
			{"notes.listing.far", 2000, 1500},
		} {
			aggregate := Define[account](slipped.family, accountKey)
			listed := Declare(aggregate, "notes.listing", From[listing](slippingCodec{at: slipped.at}),
				func(this account, _ listing) account { return this })
			if _, err := listed.RoundTrip(marksOf(slipped.n)); !errors.Is(err, ErrPayload) {
				t.Fatalf("a codec that read element %d of %d back wrong answered %v; the comparison ran on the type graph's budget of 1024 values and reported a pass for everything past it, which is a different answer from the one it says it gives", slipped.at, slipped.n, err)
			}
		}

		beyond := Define[account]("notes.listing.beyond", accountKey)
		big := Declare(beyond, "notes.listing", From(JSON[listing]()),
			func(this account, _ listing) account { return this })
		if _, err := big.RoundTrip(marksOf(valueWalkNodes + 8)); !errors.Is(err, ErrSample) {
			t.Fatalf("a sample holding more values than the walks visit answered %v, so a green round trip means either that the payload survives its codec or that it is too large to compare, and the caller cannot tell which", err)
		}
		if _, err := big.RoundTrip(marksOf(codecGraphNodes + 8)); err != nil {
			t.Fatalf("a sample of %d values through the same codec was refused (%v), so the case above passes by refusing every sample; a sample's values are counted against a budget of its own and not against the %d a type graph is walked on, which is a bound on shapes a person writes rather than on values one carries", codecGraphNodes+8, err, codecGraphNodes)
		}
	})

	t.Run("a struct nothing can compare is answered at the wire rather than reported as a pass", func(t *testing.T) {
		dropping := Define[account]("notes.located.dropping", accountKey)
		unlocated := Declare(dropping, "notes.located", From[located](unlocatingCodec{}),
			func(this account, _ located) account { return this })
		if _, err := unlocated.RoundTrip(located{IP: netip.MustParseAddr("10.0.0.1"), Note: "n"}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that dropped a netip.Addr and kept the note answered %v; every field of a netip.Addr is unexported and it declares no Equal, so a comparison that asks for that name and gives up when it is missing reports a pass having compared nothing", err)
		}
		keeping := Define[account]("notes.located.keeping", accountKey)
		kept := Declare(keeping, "notes.located", From(JSON[located]()),
			func(this account, _ located) account { return this })
		if _, err := kept.RoundTrip(located{IP: netip.MustParseAddr("10.0.0.1"), Note: "n"}); err != nil {
			t.Fatalf("a codec that kept the address was refused (%v), so the case above passes by refusing every payload whose fields are all unexported", err)
		}
		stamps := Define[account]("notes.located.stamped", accountKey)
		stamped := Declare(stamps, "notes.stamped", From(JSON[stamp]()),
			func(this account, _ stamp) account { return this })
		if _, err := stamped.RoundTrip(stamp{At: time.Now(), Note: "x"}); err != nil {
			t.Fatalf("a time.Time carrying a monotonic reading and a local zone was refused (%v); == says those are a difference and its own Equal says they are not, so the type's own answer has to be asked before ==", err)
		}

		hushes := Declare(Define[account]("notes.hushed", accountKey), "notes.hushed",
			From[hushed](hushingCodec{}), func(this account, _ hushed) account { return this })
		_, err := hushes.RoundTrip(hushed{marks: []int64{1, 2, 3}})
		if !errors.Is(err, ErrPayload) || !strings.Contains(err.Error(), "event.hushed") {
			t.Fatalf("a struct with no exported field, no Equal and a slice inside answered %v for a codec that read two other marks back; nothing in the value compared it, and a green round trip that compared nothing says the codec kept what it was handed when nobody looked", err)
		}
		murmurs := Declare(Define[account]("notes.murmured", accountKey), "notes.hushed",
			From[hushed](murmuringCodec{}), func(this account, _ hushed) account { return this })
		if _, err := murmurs.RoundTrip(hushed{marks: []int64{1, 2, 3}}); err != nil {
			t.Fatalf("the same wire format over a codec that reads the marks back was refused (%v), so the case above passes by refusing every payload nothing in the value can compare", err)
		}
		held := Declare(Define[account]("notes.heldtight", accountKey), "notes.heldtight",
			From[heldTight](looseningCodec{}), func(this account, _ heldTight) account { return this })
		_, err = held.RoundTrip(heldTight{marks: []int64{7}})
		if !errors.Is(err, ErrPayload) || !strings.Contains(err.Error(), "event.heldTight") {
			t.Fatalf("a struct whose Equal sits on the pointer receiver answered %v for a codec that dropped everything; that method is not in the value's method set, so it is never found, and taking its absence for agreement is the same pass as above through a type that looks like it declared one", err)
		}
	})

	t.Run("a payload the caller does not own the type of is provable all the same", func(t *testing.T) {
		for _, faithful := range []struct {
			what   string
			family string
			codec  Codec[ledgered]
		}{
			{"the shipped codec", "notes.ledgered.shipped", JSON[ledgered]()},
			{"a codec of the caller's own", "notes.ledgered.own", tallyingCodec{}},
		} {
			recorded := Declare(Define[account](faithful.family, accountKey), "notes.ledgered",
				From(faithful.codec), func(this account, _ ledgered) account { return this })
			carried, err := recorded.RoundTrip(ledgered{Amount: big.NewInt(1234), Note: "n"})
			if err != nil {
				t.Fatalf("%s round-tripped a *big.Int perfectly and was refused anyway: %v; every field of a big.Int is unexported, it declares no Equal and it forbids ==, and neither repair a refusal there could name — declare the method, export the field — is one a consumer can perform on a type the standard library owns", faithful.what, err)
			}
			if carried[0].Amount.Cmp(big.NewInt(1234)) != 0 || carried[0].Note != "n" {
				t.Fatalf("%s carried %v rather than the sample", faithful.what, carried[0])
			}
		}

		dropping := Declare(Define[account]("notes.ledgered.dropping", accountKey), "notes.ledgered",
			From[ledgered](untalliedCodec{}), func(this account, _ ledgered) account { return this })
		_, err := dropping.RoundTrip(ledgered{Amount: big.NewInt(1234), Note: "n"})
		if !errors.Is(err, ErrPayload) || !strings.Contains(err.Error(), "big.Int") {
			t.Fatalf("a codec that read the amount back as nothing and kept the note answered %v; the two rows above must not be bought by widening the pass, and what came back re-encodes to bytes that are not the ones the sample encoded to", err)
		}
	})

	t.Run("a leaf no wire format records is refused rather than reported as a pass", func(t *testing.T) {
		routes := Declare(Define[account]("notes.routed", accountKey), "notes.routed",
			From[routed](routingCodec{reads: func() int { return 2 }}),
			func(this account, _ routed) account { return this })
		_, err := routes.RoundTrip(routed{Do: func() int { return 1 }, Note: "n"})
		if !errors.Is(err, ErrSample) || !strings.Contains(err.Error(), "func() int") {
			t.Fatalf("a payload holding a function, read back as another function, answered %v; Go compares no two of them and no wire format records one, so asking whether the value was comparable at all and taking no for agreement passes every codec that loses it", err)
		}

		losing := Declare(Define[account]("notes.routed.losing", accountKey), "notes.routed",
			From[routed](unroutedCodec{reads: func() int { return 2 }}),
			func(this account, _ routed) account { return this })
		_, err = losing.RoundTrip(routed{Do: func() int { return 1 }, Note: "n"})
		if !errors.Is(err, ErrSample) || !strings.Contains(err.Error(), "record what identifies the behaviour") {
			t.Fatalf("the same payload through a codec that dropped the note as well answered %v; both conditions are true of it, the walk stops at the first one it carried out, and the function is the only one of the two with a repair the caller can make — a walk that keeps going reports the lost note and says nothing about the function at all", err)
		}

		pipes := Declare(Define[account]("notes.piped", accountKey), "notes.piped",
			From[piped](pipingCodec{}), func(this account, _ piped) account { return this })
		if _, err := pipes.RoundTrip(piped{Feed: make(chan int), Note: "n"}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a channel read back as another channel answered %v; a channel is comparable, so == answers for it and the difference is an ordinary one — refusing every leaf that arm reaches would close the case above and say the wrong thing here", err)
		}

		names := Declare(Define[account]("notes.named", accountKey), "notes.named",
			From(JSON[named]()), func(this account, _ named) account { return this })
		if _, err := names.RoundTrip(named{Do: "credit", Note: "n"}); err != nil {
			t.Fatalf("the repair the refusal names — record what identifies the behaviour and choose the function from it — was itself refused (%v)", err)
		}
	})

	t.Run("a value substituted for another behind an interface is answered, not walked", func(t *testing.T) {
		for _, substituted := range []struct {
			what    string
			family  string
			sample  any
			reads   func() any
			refused bool
		}{
			{"a struct read back as a wider one", "notes.boxed.wide", narrow{A: 1}, func() any { return wide{1, 2, 3} }, true},
			{"a struct read back as itself", "notes.boxed.narrow", narrow{A: 1}, func() any { return narrow{A: 1} }, false},
			{"a map read back under another key type", "notes.boxed.rekeyed", map[string]int{"a": 1}, func() any { return map[int]string{1: "a"} }, true},
			{"a map read back as itself", "notes.boxed.keyed", map[string]int{"a": 1}, func() any { return map[string]int{"a": 1} }, false},
			{"a map read back as a string", "notes.boxed.mangled", map[string]any{"a": "b"}, func() any { return "dropped" }, true},
			{"an array of ints read back as an array of strings", "notes.boxed.restrung", [3]int{1, 2, 3}, func() any { return [3]string{"x", "y", "z"} }, true},
			{"a number read back as the float every wire format has one of", "notes.boxed.widened", 1, func() any { return float64(1) }, false},
			{"a number read back as a different number", "notes.boxed.drifted", 1, func() any { return float64(2) }, true},
			{"an id larger than a float holds exactly, read back as the nearest float", "notes.boxed.rounded", int64(1)<<62 + 1, func() any { return float64(int64(1)<<62 + 1) }, true},
			{"the same unsigned", "notes.boxed.unsigned", uint64(1)<<63 + 1, func() any { return float64(uint64(1)<<63 + 1) }, true},
			{"an id a float does hold exactly, read back as that float", "notes.boxed.exact", int64(1) << 52, func() any { return float64(int64(1) << 52) }, false},
			{"two answers of different types, which the aliasing walk compares first", "notes.boxed.turned", wide{1, 2, 3}, inTurn(wide{1, 2, 3}, narrow{A: 1}), false},
			{"two answers whose maps are keyed differently", "notes.boxed.turnedmap", map[string]int{"a": 1}, inTurn(map[string]int{"a": 1}, map[int]string{1: "a"}), false},
		} {
			aggregate := Define[account](substituted.family, accountKey)
			boxes := Declare(aggregate, "notes.boxed", From[boxed](boxingCodec{reads: substituted.reads}),
				func(this account, _ boxed) account { return this })
			_, err := boxes.RoundTrip(boxed{Meta: substituted.sample})
			if substituted.refused && !errors.Is(err, ErrPayload) {
				t.Fatalf("%s answered %v; two values reached through an interface have different types, and a comparison that walks one by the other's shape either panics out of an exported method or calls a whole substituted value the same value — the one difference that is not a substitution is a number the wire widened, and it stops being one where the float no longer holds the integer, which is an id or an amount above 2^53 read back rounded", substituted.what, err)
			}
			if !substituted.refused && err != nil {
				t.Fatalf("%s was refused (%v), so the rows above pass by refusing every payload behind an interface, which is the one shape a codec of the caller's own exists for", substituted.what, err)
			}
		}
	})

	t.Run("a fact whose whole content is that it happened round trips", func(t *testing.T) {
		aggregate := Define[account]("notes.voided", accountKey)
		voiding := Declare(aggregate, "notes.voided", From(JSON[voided]()),
			func(this account, _ voided) account { return this })
		carried, err := voiding.RoundTrip(voided{})
		if err != nil {
			t.Fatalf("a marker fact answered %v; its reader type holds one value, so that is the only sample a caller could pass, and an obligation to round trip every declared fact could never be met for one", err)
		}
		if len(carried) != 1 {
			t.Fatalf("the round trip carried %d values for one sample", len(carried))
		}
		forbidding := Define[account]("notes.witnessed", accountKey)
		witnessing := Declare(forbidding, "notes.witnessed", From[witnessed](witnessCodec{}),
			func(this account, _ witnessed) account { return this })
		if _, err := witnessing.RoundTrip(witnessed{}); err != nil {
			t.Fatalf("a marker fact whose type forbids == answered %v; it holds one value like every other zero-sized type, so nothing it carries can come back different and neither == nor an Equal is needed to say so", err)
		}

		beside := Define[account]("notes.witnessed.beside", accountKey)
		trimming := Declare(beside, "notes.witnessed", From[witnessedNote](trimmingCodec{}),
			func(this account, _ witnessedNote) account { return this })
		if _, err := trimming.RoundTrip(witnessedNote{Body: "twelve"}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a codec that recorded three bytes of six answered %v for a payload carrying a marker beside the body; a type holding one value is answered where it stands, and taking it for something nothing can compare stops the walk there and leaves the field next to it to a wire its own codec agrees with", err)
		}
		if _, err := trimming.RoundTrip(witnessedNote{Body: "two"}); err != nil {
			t.Fatalf("the same codec over a body it records whole was refused (%v), so the case above passes by refusing every payload that carries a marker", err)
		}
		written := declareNotes(t, "notes.voided.carrying", JSON[note]())
		if _, err := written.RoundTrip(note{}); !errors.Is(err, ErrSample) {
			t.Fatalf("a payload-carrying fact whose sample happens to be its own zero value answered %v; that one is a caller mistake, and the non-aliasing half has nothing to disturb for it either", err)
		}
	})

	t.Run("a sample that encodes as its own zero value is refused rather than reported as a pass", func(t *testing.T) {
		for _, codec := range []struct {
			what   string
			family string
			codec  Codec[note]
		}{
			{"a codec that reuses a buffer", "notes.zero.reusing", &scratchCodec{}},
			{"the shipped codec", "notes.zero.shipped", JSON[note]()},
		} {
			written := declareNotes(t, codec.family, codec.codec)
			if _, err := written.RoundTrip(note{}); !errors.Is(err, ErrSample) {
				t.Fatalf("%s round-tripped a zero sample with %v, and a zero sample cannot disturb a reused buffer nor prove fidelity", codec.what, err)
			}
		}
	})

	t.Run("a retained revision behind a copying upcaster is under test too", func(t *testing.T) {
		copying := func(from note) (note, error) { return note{Body: bytes.Clone(from.Body)}, nil }

		reusing := Define[carrier]("notes.upcast.reusing", accountKey)
		behind := Declare(reusing, "notes.written",
			Then(From[note](&scratchCodec{}), JSON[note](), copying), carryNote)
		if _, err := behind.RoundTrip(note{Body: []byte("one")}, note{Body: []byte("two")}); !errors.Is(err, ErrPayload) {
			t.Fatalf("a revision-1 codec that reuses its decode buffer answered %v behind an upcaster: every upcaster converts and therefore copies, so a verdict taken after one tests the last revision of the chain and nothing else", err)
		}

		allocating := Define[carrier]("notes.upcast.allocating", accountKey)
		fine := Declare(allocating, "notes.written",
			Then(From(JSON[note]()), JSON[note](), copying), carryNote)
		if _, err := fine.RoundTrip(note{Body: []byte("one")}, note{Body: []byte("two")}); err != nil {
			t.Fatalf("the same chain over a codec that allocates was refused (%v), so the case above passes by refusing every chain of two", err)
		}
	})

	t.Run("a sample answers for the revision it is given for", func(t *testing.T) {
		aggregate := Define[account]("accounts.mixedcodec", accountKey)
		credited := Declare(aggregate, "accounts.credited",
			Then(From[creditedV1](decimalCodec{}), JSON[creditedV2](), toCreditedV2), creditAccount)

		carried, err := credited.RoundTrip(creditedV1{Minor: 250}, creditedV2{Minor: 7, Reason: "now"})
		if err != nil {
			t.Fatalf("two revisions written by two formats did not round trip: %v", err)
		}
		if carried[0] != (creditedV2{Minor: 250, Reason: "migrated"}) || carried[1] != (creditedV2{Minor: 7, Reason: "now"}) {
			t.Fatalf("the round trip carried %+v, so a revision was not read by the codec that wrote it", carried)
		}

		if _, err := credited.RoundTrip(creditedV2{Minor: 250}, creditedV2{Minor: 7, Reason: "now"}); !errors.Is(err, ErrSample) {
			t.Fatalf("a sample of the wrong reader type was accepted for revision 1: %v", err)
		}
		if _, err := credited.RoundTrip(creditedV1{Minor: 250}); !errors.Is(err, ErrSample) {
			t.Fatalf("a fact retaining two revisions round-tripped one sample: %v", err)
		}
	})

	t.Run("a refusal names the repair, because ten of them share two sentinels", func(t *testing.T) {
		aggregate := Define[account]("accounts.diagnosed", accountKey)
		credited := Declare(aggregate, "accounts.credited",
			Then(From[creditedV1](decimalCodec{}), JSON[creditedV2](), toCreditedV2), creditAccount)
		reusing := declareNotes(t, "notes.diagnosed.reusing", &scratchCodec{})
		truncating := declareNotes(t, "notes.diagnosed.truncating", truncatingCodec{})
		panicking := Declare(Define[account]("notes.diagnosed.panicking", accountKey), "notes.spanned",
			From[spanned](unmarkedCodec{}), func(this account, _ spanned) account { return this })
		oversized := Declare(Define[account]("notes.diagnosed.oversized", accountKey), "notes.listing",
			From(JSON[listing]()), func(this account, _ listing) account { return this })
		opaque := Declare(Define[account]("notes.diagnosed.opaque", accountKey), "notes.hushed",
			From[hushed](hushingCodec{}), func(this account, _ hushed) account { return this })
		unrecordable := Declare(Define[account]("notes.diagnosed.routed", accountKey), "notes.routed",
			From[routed](routingCodec{reads: func() int { return 2 }}),
			func(this account, _ routed) account { return this })
		substituted := Declare(Define[account]("notes.diagnosed.substituted", accountKey), "notes.boxed",
			From[boxed](boxingCodec{reads: func() any { return wide{1, 2, 3} }}),
			func(this account, _ boxed) account { return this })
		for _, refused := range []struct {
			what  string
			fails func() error
			names string
		}{
			{"a sample of the wrong reader type", func() error {
				_, err := credited.RoundTrip(creditedV2{Minor: 1}, creditedV2{Minor: 2, Reason: "now"})
				return err
			}, `revision 1 of "accounts.credited" reads event.creditedV1 and the sample is a event.creditedV2`},
			{"one sample for a fact retaining two", func() error {
				_, err := credited.RoundTrip(creditedV1{Minor: 1})
				return err
			}, "retains 2 revisions and 1 samples were given"},
			{"a sample that encodes as its own zero value", func() error {
				_, err := reusing.RoundTrip(note{})
				return err
			}, "encodes its sample exactly as its own zero value"},
			{"a codec that hands back memory it reuses", func() error {
				_, err := reusing.RoundTrip(note{Body: []byte("one")})
				return err
			}, "decoded into memory its codec reuses"},
			{"a codec that records less than it was handed", func() error {
				_, err := truncating.RoundTrip(note{Body: []byte("twelve")})
				return err
			}, "does not read back the event.note it was given"},
			{"a payload whose own Equal panicked when it was asked", func() error {
				_, err := panicking.RoundTrip(spanned{marks: []int64{5}})
				return err
			}, "carries a event.spanned whose own Equal panicked"},
			{"a sample holding more values than the walks visit", func() error {
				_, err := oversized.RoundTrip(marksOf(valueWalkNodes + 8))
				return err
			}, "round trip a smaller sample of the same type"},
			{"a payload nothing in the value can compare, answered at the wire", func() error {
				_, err := opaque.RoundTrip(hushed{marks: []int64{1}})
				return err
			}, "nothing can compare the event.hushed it carries"},
			{"a payload holding what no wire format records", func() error {
				_, err := unrecordable.RoundTrip(routed{Do: func() int { return 1 }, Note: "n"})
				return err
			}, "record what identifies the behaviour"},
			{"a value substituted for another behind an interface", func() error {
				_, err := substituted.RoundTrip(boxed{Meta: narrow{A: 1}})
				return err
			}, "does not read back the event.boxed it was given"},
		} {
			err := refused.fails()
			if err == nil {
				t.Fatalf("%s was accepted, so nothing here says which repair it needs", refused.what)
			}
			if !strings.Contains(err.Error(), refused.names) {
				t.Fatalf("%s was refused with %q, which never says %q; the message is the whole deliverable of this helper, the repairs behind two sentinels are a different one each, and the wrong-type row is what keeps a retained revision reporting its own reader type rather than the current one", refused.what, err, refused.names)
			}
		}
	})
}
