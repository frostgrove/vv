package event

import (
	"os"
	"path/filepath"
	"testing"
)

// The deliberately broken sources the three structural checks beside them are
// falsified against. Each is a package of its own written into a temporary
// directory and type-checked like any other, so what a check reports about it
// is what it would report about this tree.

func writeFixture(t *testing.T, written string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(written), 0o600); err != nil {
		t.Fatalf("cannot write the fixture: %v", err)
	}
	return root
}

const renderingFixture = `package fixture

import (
	"errors"
	"fmt"
)

type Key string
type Cursor string
type Version uint64
type Position uint64

func (this Key) String() string { return string(this) }

type Stream struct {
	Family string
	Key    Key
}

func (this Stream) String() string { return "[stream " + this.Family + "]" }

type Envelope struct {
	Stream  Stream
	Version Version
	Payload []byte
}

type Limits struct{ MaxPayload int }

var ErrKey = errors.New("fixture: the key is not one this store admits")

func quote(value Key) string { return string(value) }

func renderedByItsOwnMethod(key Key) error {
	return fmt.Errorf("%w: %s", ErrKey, key.String())
}

func renderedByConversion(stream Stream) error {
	return fmt.Errorf("%w: %s", ErrKey, string(stream.Key))
}

func renderedWholesale(envelope Envelope) error {
	return fmt.Errorf("%w: %v", ErrKey, envelope)
}

func renderedAsBytes(raw []byte) error {
	return errors.New("fixture: the payload " + string(raw) + " could not be read")
}

func renderedThroughAHelper(key Key) error {
	return fmt.Errorf("%w: %s", ErrKey, quote(key))
}

func renderedAsAField(at Version, cursor Cursor) error {
	return fmt.Errorf("%w: version %d and cursor %s", ErrKey, at, cursor)
}

func renderedThroughAnIndexedVerb(key Key) error {
	return fmt.Errorf("%w: %[2]s", ErrKey, key)
}

func renderedThroughAFlaggedIndexedVerb(key Key, sample any) error {
	return fmt.Errorf("%w: %+[3]T", ErrKey, key, sample)
}

func renderedThroughAStarredWidth(key Key, width int) error {
	return fmt.Errorf("%w: %-*s", ErrKey, width, key)
}

func renderedThroughALocalConversion(stream Stream) error {
	detail := string(stream.Key)
	return fmt.Errorf("%w: %s", ErrKey, detail)
}

func renderedThroughALocalSprintf(key Key, at Version) error {
	detail := fmt.Sprintf("%s at %d", key, at)
	return fmt.Errorf("%w: %s", ErrKey, detail)
}

func renderedThroughALocalConcatenation(cursor Cursor) error {
	text := "fixture: the cursor " + string(cursor)
	return errors.New(text)
}

func renderedThroughALocalPayload(raw []byte) error {
	body := string(raw)
	return fmt.Errorf("%w: %s", ErrKey, body)
}

func permittedLocalCount(payload []byte) error {
	detail := fmt.Sprintf("%d bytes", len(payload))
	return fmt.Errorf("%w: %s", ErrKey, detail)
}

func permittedLocalAppendedToItself(name string, revision int) error {
	detail := name
	detail = detail + fmt.Sprintf(" at revision %d", revision)
	return fmt.Errorf("%w: %s", ErrKey, detail)
}

func permittedFamily(stream Stream) error {
	return fmt.Errorf("%w: %s names no aggregate", ErrKey, stream)
}

func permittedCount(payload []byte, limits Limits) error {
	return fmt.Errorf("%w: %d bytes is over a bound of %d", ErrKey, len(payload), limits.MaxPayload)
}

func permittedTypeName(sample any) error {
	return fmt.Errorf("%w: the sample is a %T", ErrKey, sample)
}

func permittedBound(name string, revision int) error {
	return fmt.Errorf("%w: %q at revision %d", ErrKey, name, revision)
}
`

const mutableFixture = `package fixture

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var sentinel = errors.New("fixture: the idiom this repository is written in")

var typeToken = reflect.TypeFor[error]()

type ledger struct{ entries int }

func (this *ledger) add() { this.entries++ }

func (this ledger) read() int { return this.entries }

type stated struct{ family string }

func (this stated) String() string { return this.family }

type writable struct{ said int }

func (this *writable) String() string { this.said++; return "written" }

var (
	registry = map[string]int{}
	guard    sync.Mutex
	counter  int
	seen     = make(map[string]int)
	pool     = new(sync.Pool)
	tally    = new(int64)
	kept     ledger
	anything any          = map[string]int{}
	rendered fmt.Stringer = &writable{}
	frozen   stated       = stated{family: "orders"}
)

func remember(name string) {
	guard.Lock()
	registry[name] = counter
	counter++
	delete(registry, name)
	guard.Unlock()
	record(seen, name)
	pool.Put(name)
	bump(tally)
	kept.add()
}

func record(counts map[string]int, name string) { counts[name]++ }

func bump(count *int64) { *count++ }

func reported() error { return sentinel }

func reflected() reflect.Type { return typeToken }

func spoken() string { return frozen.String() + rendered.String() + fmt.Sprint(anything) }

func read() int { return kept.read() }
`

const transactionControlFixture = `package fixture

type log struct{}

func (this log) Append(stream string, records []int) error { return nil }

func (this log) ReadStream(stream string, page int) []int { return nil }

type builder struct{ written []int }

func (this *builder) Append(value int) { this.written = append(this.written, value) }

type handle struct{}

func (this handle) Begin() {}

func (this handle) Commit() {}

func (this handle) Rollback() {}

func (this handle) commit() {}

func commit(tx handle) {}

func beginsATransaction(tx handle) { tx.Begin() }

func commitsThroughAMethod(tx handle) { tx.commit() }

func commitsThroughAFreeFunction(tx handle) { commit(tx) }

func rollsBack(tx handle) { tx.Rollback() }

func commitsThroughABoundMethod(tx handle) {
	finish := tx.Commit
	finish()
}

func commitsThroughAHandedOffFunction() { runs(commit) }

func runs(step func(handle)) { step(handle{}) }

func handsOffAnAppend(store log, stream string, records []int) error {
	write := store.Append
	return write(stream, records)
}

func retriesInALoop(store log, stream string, records []int) {
	for attempt := 0; attempt < 3; attempt++ {
		_ = store.Append(stream, records)
	}
}

func retriesByRecursion(store log, stream string, records []int) {
	if store.Append(stream, records) != nil {
		retriesByRecursion(store, stream, records)
	}
}

func jumpsBack() {
retry:
	goto retry
}

func pagesARead(store log, stream string) []int {
	var records []int
	for page := 0; page < 4; page++ {
		records = append(records, store.ReadStream(stream, page)...)
	}
	return records
}

func appendsToABuilder(written *builder, values []int) {
	for _, value := range values {
		written.Append(value)
	}
}
`

const quotedTestingFixture = `package fixture

// A note about the "testing" package, which this one does not import.
const named = "testing"

func Named() string { return named }
`

const importedTestingFixture = `package fixture

import "testing"

func Certify(t *testing.T) { t.Helper() }
`
