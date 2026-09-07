package event

import (
	"errors"
	"strconv"
	"testing"
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
}
