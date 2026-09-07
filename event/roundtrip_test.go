package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"math"
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

type sealedNote struct{ body []byte }

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

// A payload type whose Equal answers a question of its own. Asking it would
// panic rather than compare, so the comparison has to check the signature before
// it trusts the name.
type weighed struct{ Amount int64 }

func (this weighed) Equal(first, second int64) bool { return first == second }

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
		weight := Declare(weights, "notes.weighed", From(JSON[weighed]()),
			func(this account, _ weighed) account { return this })
		if _, err := weight.RoundTrip(weighed{Amount: 5}); err != nil {
			t.Fatalf("a payload whose Equal answers a different question was refused: %v; the comparison reads the name and has to check the signature before it calls it", err)
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

	t.Run("a refusal names the repair, because five of them share two sentinels", func(t *testing.T) {
		aggregate := Define[account]("accounts.diagnosed", accountKey)
		credited := Declare(aggregate, "accounts.credited",
			Then(From[creditedV1](decimalCodec{}), JSON[creditedV2](), toCreditedV2), creditAccount)
		reusing := declareNotes(t, "notes.diagnosed.reusing", &scratchCodec{})
		truncating := declareNotes(t, "notes.diagnosed.truncating", truncatingCodec{})
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
		} {
			err := refused.fails()
			if err == nil {
				t.Fatalf("%s was accepted, so nothing here says which repair it needs", refused.what)
			}
			if !strings.Contains(err.Error(), refused.names) {
				t.Fatalf("%s was refused with %q, which never says %q; the message is the whole deliverable of this helper, the five repairs are a different one each, and the wrong-type row is what keeps a retained revision reporting its own reader type rather than the current one", refused.what, err, refused.names)
			}
		}
	})
}
