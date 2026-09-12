package event

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"
)

// A payload with a field no codec records: encoding/json writes the exported
// one and cannot reach the other, so two commands a whole note apart are one
// append and must be one fingerprint.
type levied struct {
	Minor int64
	note  string
}

func levyAccount(this account, event levied) account {
	if this.Applied == nil {
		this.Applied = map[string]bool{}
	}
	this.Balance += event.Minor
	return this
}

type stampedCredit struct {
	Minor  int64
	Reason string
	Stamp  int64
}

// The defect INV-125 names, in the form that cannot flake: a codec that writes
// a value it minted rather than one the command carried. A clock at nanosecond
// resolution is the same defect and the same arithmetic.
type stampingCodec struct {
	inner Codec[creditedV2]
	fresh *atomic.Int64
}

func (this stampingCodec) CanEncode() error { return this.inner.CanEncode() }

func (this stampingCodec) Encode(value creditedV2) ([]byte, error) {
	return json.Marshal(stampedCredit{Minor: value.Minor, Reason: value.Reason, Stamp: this.fresh.Add(1)})
}

func (this stampingCodec) Decode(payload []byte) (creditedV2, error) {
	var read stampedCredit
	if err := json.Unmarshal(payload, &read); err != nil {
		return creditedV2{}, err
	}
	return creditedV2{Minor: read.Minor, Reason: read.Reason}, nil
}

func bindRates(t *testing.T, store Store) (*Repo[account, accountID], *Fact[account, accountID, reading], At[account]) {
	t.Helper()
	aggregate := Define[account]("digest.rate", accountKey)
	accrued := Declare(aggregate, "digest.accrued", From(JSON[reading]()),
		func(this account, event reading) account {
			this.Balance += int64(event.Rate)
			return this
		})
	repo, err := Bind(Open(store), aggregate)
	if err != nil {
		t.Fatalf("the declaration the unencodable rows are decided on was not bound: %v", err)
	}
	_, at, err := repo.Load(context.Background(), accountID{tenant: "acme", number: "A-17"})
	if err != nil {
		t.Fatalf("the token the unencodable rows are digested over was refused with %v", err)
	}
	return repo, accrued, at
}

func TestADigestIsTheBytesThisAppendWouldWrite(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	other := accountID{tenant: "acme", number: "A-99"}
	ctx := context.Background()

	t.Run("two commands apart only in what the codec never records are one fingerprint", func(t *testing.T) {
		store := newRecordingStore(t)
		aggregate := Define[account]("digest.levied", accountKey)
		charge := Declare(aggregate, "digest.levied", From(JSON[levied]()), levyAccount)
		repo, err := Bind(Open(store), aggregate)
		if err != nil {
			t.Fatalf("the declaration whose codec drops a field was not bound: %v", err)
		}
		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the token these digests are taken over was refused with %v", err)
		}

		first, err := repo.Digest(at, charge.New(acme, levied{Minor: 5, note: "the operator typed this"}))
		if err != nil {
			t.Fatalf("the first of two commands was refused with %v", err)
		}
		second, err := repo.Digest(at, charge.New(acme, levied{Minor: 5, note: "and then typed something else"}))
		if err != nil {
			t.Fatalf("the second of two commands was refused with %v", err)
		}
		if first != second {
			t.Fatal("two commands whose encoded records are byte-identical fingerprinted differently, so the digest is over the Change values' Go representation and a retry of an operation the store cannot tell apart is refused as another one")
		}

		recorded, err := repo.Digest(at, charge.New(acme, levied{Minor: 6, note: "the operator typed this"}))
		if err != nil {
			t.Fatalf("the control command was refused with %v", err)
		}
		if first == recorded {
			t.Fatal("two commands differing in the field the codec does record fingerprinted alike, so the pair above passes against a digest that is constant")
		}
	})

	t.Run("one command a stamping codec encodes twice is two fingerprints", func(t *testing.T) {
		store := newRecordingStore(t)
		aggregate := Define[account]("digest.stamped", accountKey)
		credited := Declare(aggregate, "digest.credited",
			From(stampingCodec{inner: JSON[creditedV2](), fresh: &atomic.Int64{}}), creditAccount)
		repo, err := Bind(Open(store), aggregate)
		if err != nil {
			t.Fatalf("the declaration whose codec stamps every encode was not bound: %v", err)
		}
		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the token these digests are taken over was refused with %v", err)
		}

		decided := creditedV2{Minor: 5, Reason: "one"}
		first, err := repo.Digest(at, credited.New(acme, decided))
		if err != nil {
			t.Fatalf("the first encoding of one decision was refused with %v", err)
		}
		second, err := repo.Digest(at, credited.New(acme, decided))
		if err != nil {
			t.Fatalf("the second encoding of one decision was refused with %v", err)
		}
		if first == second {
			t.Fatal("one decision encoded twice by a codec that stamps each encode fingerprinted alike, so the digest is not over the bytes the store would hold; the two fingerprints this asserts are the arithmetic working and the idempotence mechanism DISABLED for this aggregate — a retry of it is refused as a collision rather than answered, which is the loud failure INV-125 chooses and is not behaviour to build on")
		}
	})

	t.Run("a digest issues no store call of any kind", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the token this digest is taken over was refused with %v", err)
		}
		store.forget()
		if _, err := repo.Digest(at, declared.credited.New(acme, creditedV2{Minor: 5, Reason: "one"})); err != nil {
			t.Fatalf("a digest over a legal batch was refused with %v", err)
		}
		store.exactly(t, "a digest", map[string]int{})
		if made := store.made(); made != 0 {
			t.Fatalf("a digest issued %d store calls, so a caller that fingerprints before it claims reaches a decorator that may refuse it and a backend that may be down", made)
		}
	})

	t.Run("a malformed batch is refused with the sentinel an append gives it", func(t *testing.T) {
		store := newRecordingStore(t)
		store.limits.MaxPayload = 64
		repo, declared := bindAccounts(t, store)
		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the token these digests are taken over was refused with %v", err)
		}
		_, elsewhere, err := repo.Load(ctx, other)
		if err != nil {
			t.Fatalf("the token the crossed-stream row needs was refused with %v", err)
		}
		rates, accrued, measured := bindRates(t, store)
		toward, refusedToward := rates.Digest(measured, accrued.New(acme, reading{Rate: math.Inf(1)}))
		beyond, refusedBeyond := rates.Digest(measured, accrued.New(acme, reading{Rate: math.NaN()}))
		if refusedToward == nil && refusedBeyond == nil && toward == beyond {
			t.Fatalf("two different decisions of one fact, neither of which its codec could encode, were answered rather than refused and are one fingerprint %x: a change carrying its own refusal carries no payload at all, so every unencodable decision of this fact digests to the record's shape alone and an operation key spent on one of them answers the other already done",
				toward)
		}
		if !errors.Is(refusedToward, ErrEncode) || !errors.Is(refusedBeyond, ErrEncode) {
			t.Fatalf("two decisions their codec could not encode were answered %v and %v, where a digest answers the refusal the change already carries", refusedToward, refusedBeyond)
		}

		oversized := declared.credited.New(acme, creditedV2{Minor: 5, Reason: strings.Repeat("f", 64)})
		overlong := make([]Change[account], 0, store.limits.MaxBatch+1)
		for range cap(overlong) {
			overlong = append(overlong, declared.credited.New(acme, creditedV2{Minor: 1, Reason: "one"}))
		}

		for _, one := range []struct {
			what    string
			repo    *Repo[account, accountID]
			at      At[account]
			changes []Change[account]
			want    error
		}{
			{"a token no load minted", repo, At[account]{}, []Change[account]{declared.credited.New(acme, creditedV2{Minor: 5, Reason: "one"})}, ErrKey},
			{"a change decided for another stream", repo, at, []Change[account]{declared.credited.New(other, creditedV2{Minor: 5, Reason: "one"})}, ErrWrongStream},
			{"a change decided on this stream against another stream's token", repo, elsewhere, []Change[account]{declared.credited.New(acme, creditedV2{Minor: 5, Reason: "one"})}, ErrWrongStream},
			{"a decision its declared codec could not encode", rates, measured, []Change[account]{accrued.New(acme, reading{Rate: math.Inf(1)})}, ErrEncode},
			{"an encoded payload over the bound the store published", repo, at, []Change[account]{oversized}, ErrTooLarge},
			{"a batch over the count the store published", repo, at, overlong, ErrTooLarge},
		} {
			digest, refused := one.repo.Digest(one.at, one.changes...)
			if !errors.Is(refused, one.want) {
				t.Fatalf("%s answered %v where a digest answers the refusal an append answers", one.what, refused)
			}
			if digest != ([32]byte{}) {
				t.Fatalf("%s answered a fingerprint beside a refusal, and a caller that claims under one has claimed under a digest of nothing", one.what)
			}
			_, _, appended := one.repo.Append(ctx, one.at, one.changes...)
			if !errors.Is(appended, one.want) || appended.Error() != refused.Error() {
				t.Fatalf("%s read %q through a digest and %q through an append, so a caller that digests first learns something other than what the append it is about to make would say",
					one.what, refused, appended)
			}
		}
	})
}

func TestTwoAttemptsAtDifferentVersionsDigestEqual(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	decide := func() []Change[account] {
		return []Change[account]{
			declared.credited.New(acme, creditedV2{Minor: 5, Reason: "one"}),
			declared.credited.New(acme, creditedV2{Minor: 2, Reason: "two"}),
		}
	}

	_, first, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the first attempt's load was refused with %v", err)
	}
	attempted, err := repo.Digest(first, decide()...)
	if err != nil {
		t.Fatalf("the first attempt's digest was refused with %v", err)
	}
	if _, _, err := repo.Append(ctx, first, decide()...); err != nil {
		t.Fatalf("the first attempt's append was refused with %v", err)
	}

	_, again, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the retry's load was refused with %v", err)
	}
	retried, err := repo.Digest(again, decide()...)
	if err != nil {
		t.Fatalf("the retry's digest was refused with %v", err)
	}

	if first.Version() == again.Version() {
		t.Fatalf("both attempts loaded at version %d, so this case never reaches the state a retry in a new process is in and proves nothing about it", first.Version())
	}
	if attempted != retried {
		t.Fatalf("one operation digested differently at version %d and at version %d, so the expected version is inside the fingerprint — and the retry that loads what the first attempt committed can never reproduce it, which refuses the caller's own operation under a sentinel that says the key was spent on another one",
			first.Version(), again.Version())
	}
}

func TestADigestCollidesOnAByteAStreamAndAnOrder(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	other := accountID{tenant: "acme", number: "A-99"}
	ctx := context.Background()
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)

	credits := func(id accountID, first, second creditedV2) []Change[account] {
		return []Change[account]{declared.credited.New(id, first), declared.credited.New(id, second)}
	}
	one, two := creditedV2{Minor: 5, Reason: "one"}, creditedV2{Minor: 2, Reason: "two"}

	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the token this table digests over was refused with %v", err)
	}
	_, elsewhere, err := repo.Load(ctx, other)
	if err != nil {
		t.Fatalf("the token the different-stream row needs was refused with %v", err)
	}
	baseline, err := repo.Digest(at, credits(acme, one, two)...)
	if err != nil {
		t.Fatalf("the batch every row below is compared against was refused with %v", err)
	}

	for _, differs := range []struct {
		what    string
		at      At[account]
		changes []Change[account]
		why     string
	}{
		{"one byte of one payload", at, credits(acme, creditedV2{Minor: 5, Reason: "onf"}, two),
			"a batch whose bytes differ is another operation, and a key spent on it must not answer this one done"},
		{"the same records on another stream", elsewhere, credits(other, one, two),
			"a key covers one append to the stream the claim names, so two streams under one key are two operations"},
		{"the same records in another order", at, credits(acme, two, one),
			"the fold is order-dependent, so a batch the same events reordered is a different history and a different operation"},
	} {
		digest, err := repo.Digest(differs.at, differs.changes...)
		if err != nil {
			t.Fatalf("the %s row was refused with %v", differs.what, err)
		}
		if digest == baseline {
			t.Fatalf("a batch differing in %s fingerprinted alike: %s", differs.what, differs.why)
		}
	}

	if _, _, err := repo.Append(ctx, at, credits(acme, one, two)...); err != nil {
		t.Fatalf("the append that moves the stream under the fourth row was refused with %v", err)
	}
	_, moved, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the load after the stream moved was refused with %v", err)
	}
	atAnotherVersion, err := repo.Digest(moved, credits(acme, one, two)...)
	if err != nil {
		t.Fatalf("the fourth row was refused with %v", err)
	}
	if moved.Version() == at.Version() {
		t.Fatalf("both tokens name version %d, so the row that must not differ never varied the thing it is about", at.Version())
	}
	if atAnotherVersion != baseline {
		t.Fatalf("the same records under the same key at version %d and at version %d fingerprinted differently, and that fourth row is what makes the three above mean the content decided it rather than something else did",
			at.Version(), moved.Version())
	}

	t.Run("the preimage is length-prefixed and covers the type, the revision and the payload", func(t *testing.T) {
		stream := accountsAt("acme", "A-17")
		records := func(firstType string, firstRevision int, firstPayload, secondType string) []Record {
			return []Record{
				{Type: firstType, Revision: firstRevision, Payload: []byte(firstPayload)},
				{Type: secondType, Revision: 1, Payload: []byte("x")},
			}
		}
		held := records("t", 1, "ab", "c")
		for _, apart := range []struct {
			what  string
			other []Record
			why   string
		}{
			{"one split of the same concatenation", records("t", 1, "a", "bc"),
				`without a length before each field "a"+"bc" and "ab"+"c" are one preimage, and two different operations share a fingerprint`},
			{"one revision", records("t", 2, "ab", "c"),
				"a revision decides which codec reads the payload back, so two revisions of one byte string are two records"},
			{"one wire type name", records("u", 1, "ab", "c"),
				"a type name decides which fold the payload reaches, so two names over one payload are two records"},
		} {
			if digestOf(stream, held) == digestOf(stream, apart.other) {
				t.Fatalf("two batches %s apart fingerprinted alike: %s", apart.what, apart.why)
			}
		}
		if digestOf(stream, held) != digestOf(stream, records("t", 1, "ab", "c")) {
			t.Fatal("one batch digested twice gave two fingerprints, so the rows above pass against a digest that answers something fresh every time")
		}
		if digestOf(stream, held) == digestOf(accountsAt("acme", "A-99"), held) {
			t.Fatal("one batch over two streams fingerprinted alike, so the composed stream is not in the preimage")
		}
	})
}

func TestTheDigestPreimageIsFrozen(t *testing.T) {
	for _, frozen := range []struct {
		what    string
		stream  Stream
		records []Record
		sum     string
	}{
		{"a batch of no records at all", accountsAt("acme", "A-17"), nil,
			"d332cf614d19de6711247ceaa4eb39470d447954710668676153ca760d5209d3"},
		{"one record at the revision its chain is on", accountsAt("acme", "A-17"),
			[]Record{{Type: "accounts.credited", Revision: 2, Payload: []byte(`{"Minor":5,"Reason":"one"}`)}},
			"204bf6a4a985a605f1f03bdf2a0eec5456c638aa4e8c03011499a369fef2f6fd"},
		{"two records in the order they would be written", accountsAt("acme", "A-17"),
			[]Record{{Type: "t", Revision: 1, Payload: []byte("ab")}, {Type: "c", Revision: 1, Payload: []byte("x")}},
			"1d005cd85207e7fbd0f0859e0a59fed34a7c311bee64e3b042dcdfebf4bcc9a5"},
		{"an empty payload at revision zero", accountsAt("acme", "A-17"),
			[]Record{{Type: "accounts.closed", Revision: 0, Payload: nil}},
			"8ca32f9b7a228088612945b72fcd40af63a4275e76e3d6d0f64357785145be75"},
		{"a key whose own rendering is escaped again", Stream{Family: "accounts.account", Key: Compose("acme", "evil/A-17")},
			[]Record{{Type: "t", Revision: 1, Payload: []byte("ab")}},
			"f45d4b59791d28420086ced27dd56d3c5230856ab179d34fadab340f128a4666"},
		{"a revision that does not fit in one byte", accountsAt("acme", "A-17"),
			[]Record{{Type: "t", Revision: 258, Payload: []byte("ab")}},
			"951f6766b13dc7add05d3f487d9f3c6575ee433dc0334711e97fe4de58b27f18"},
	} {
		sum := digestOf(frozen.stream, frozen.records)
		if got := hex.EncodeToString(sum[:]); got != frozen.sum {
			t.Fatalf("%s digests to %s where every fingerprint this framework has written was %s: the field order, the eight big-endian bytes of each length and of each revision, and the composed stream that opens the preimage are all frozen, so a build that tidies any of them recomputes a different value for every operation key still inside its retention window and answers each legitimate retry as a collision on an operation nobody performed",
				frozen.what, got, frozen.sum)
		}
	}
}
