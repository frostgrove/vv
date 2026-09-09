//go:build integration

package eventpg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/frostgrove/vv/event"
)

const (
	mutationVariable = "EVENTPG_MUTATION"
	mutations        = 7
)

// The suite detects 27 of its own 165 section-level assertions, so "eventpg
// passes eventtest" is worth exactly as much as the suite's ability to fail. Six
// decorators over the real store, each run through the real conformance test in
// a subprocess of this binary, and each must be reported by the section named
// beside it. A mutation the suite does not catch is a finding against the suite
// and never a pass.
type defect struct {
	name    string
	section string
	over    func(*Store) event.Store
}

func defects() []defect {
	return []defect{
		{"short-stream-page", "stream paging", func(store *Store) event.Store { return shortPage{store} }},
		{"reversed-log-page", "global order", func(store *Store) event.Store { return reversedLog{store} }},
		{"stale-admission", "expected version", func(store *Store) event.Store { return staleAdmission{store} }},
		{"newest-position-cursor", "resumption", func(store *Store) event.Store { return newestCursor{store} }},
		{"pooled-payload-buffer", "payload ownership", func(store *Store) event.Store { return &pooledPayloads{Store: store} }},
		{"crossed-stream", "stream identity", func(store *Store) event.Store { return crossedStream{store} }},
		{"in-flight-newest-position", "resumption", func(store *Store) event.Store { return newestFetched{store} }},
	}
}

// What the store the factory hands the suite actually is. In an ordinary run it
// is the store; in a subprocess the harness started it is the store with one
// thing wrong with it, and the run has to go red.
func mutated(t *testing.T, store *Store) event.Store {
	t.Helper()
	name := os.Getenv(mutationVariable)
	if name == "" {
		return store
	}
	for _, held := range defects() {
		if held.name == name {
			return held.over(store)
		}
	}
	t.Fatalf("%s names %q and this harness holds no mutation of that name", mutationVariable, name)
	return store
}

func TestTheConformanceSuiteCatchesADefectiveStore(t *testing.T) {
	inventory := defects()
	if err := sized(inventory, mutations, "store defects"); err != nil {
		t.Fatal(err)
	}
	if err := sized(inventory[:mutations-1], mutations, "store defects"); err == nil {
		t.Fatal("an inventory one row shorter passed the assertion that pins the size, so what is pinned is whatever the slice holds")
	}

	output, code := runsTheSuite(t, os.Environ())
	if code != 0 {
		t.Fatalf("the unmutated store fails the suite in a subprocess, so nothing below is about the mutations:\n%s", output)
	}

	caught := 0
	for _, held := range inventory {
		if t.Run(held.name, func(t *testing.T) {
			output, code := runsTheSuite(t, append(os.Environ(), mutationVariable+"="+held.name))
			if code == 0 {
				t.Fatalf("a store that %s passed the conformance suite, so the suite certifies a defect the %s section is supposed to report:\n%s",
					held.name, held.section, output)
			}
			if !strings.Contains(output, reportedAs+held.section+": failed") {
				t.Fatalf("a store that %s failed the suite and the %s section did not report it — the sections that did are %v:\n%s",
					held.name, held.section, sectionsThatFailed(output), output)
			}
		}) {
			caught++
		}
	}
	if caught != mutations {
		t.Fatalf("%d of the %d defects this harness names were driven through the suite and reported by their section, and a loop over an inventory nobody counted runs as many times as the slice is long", caught, mutations)
	}
}

// A row dropped from a harness takes a section's only control with it, and the
// test that ranges over it then loops that many times fewer and reports ok. The
// control is the same assertion over an inventory one row shorter.
func sized[T any](held []T, at int, what string) error {
	if len(held) != at {
		return fmt.Errorf("this harness holds %d %s where it names %d", len(held), what, at)
	}
	return nil
}

// The gate itself, driven rather than described: the child runs no test at all,
// so the only thing that can fail it is TestMain's own refusal, and the same
// command with the variable set is the control that says the refusal is the
// variable's.
func TestTheGateFailsWhenTheDSNIsUnset(t *testing.T) {
	t.Run("an unset DSN fails the run", func(t *testing.T) {
		output, code := runs(t, "^$", without(os.Environ(), testDSN))
		if code == 0 {
			t.Fatalf("a run with no %s exited 0, and a suite that proves nothing without a database must not print ok:\n%s", testDSN, output)
		}
		if !strings.Contains(output, testDSN) {
			t.Fatalf("the refusal does not name the variable a person has to set:\n%s", output)
		}
		if strings.Contains(output, "SKIP") {
			t.Fatalf("the run skipped rather than failed, which is the shape that reports a run that measured nothing as a pass:\n%s", output)
		}
	})

	t.Run("the same command with the DSN set passes", func(t *testing.T) {
		output, code := runs(t, "^$", os.Environ())
		if code != 0 {
			t.Fatalf("a run with %s set and no test selected exited %d, so the refusal above is not about the variable:\n%s", testDSN, code, output)
		}
	})
}

func runsTheSuite(t *testing.T, environment []string) (string, int) {
	t.Helper()
	return runs(t, narrowRun, environment)
}

func runs(t *testing.T, pattern string, environment []string) (string, int) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run", pattern, "-test.v", "-test.count=1", "-test.timeout=10m")
	command.Env = environment
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	exit, is := err.(*exec.ExitError)
	if !is {
		t.Fatalf("this binary could not be run as a child of itself: %v\n%s", err, output)
	}
	return string(output), exit.ExitCode()
}

func without(environment []string, name string) []string {
	kept := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, name+"=") {
			kept = append(kept, entry)
		}
	}
	return kept
}

func sectionsThatFailed(output string) []string {
	var failed []string
	for section, given := range censusOf(output) {
		for _, said := range given {
			if strings.HasPrefix(said, "failed") {
				failed = append(failed, section)
				break
			}
		}
	}
	slices.Sort(failed)
	return failed
}

type shortPage struct{ *Store }

func (this shortPage) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil || len(page) < this.limits.StreamPage {
		return page, err
	}
	return page[:len(page)-1], nil
}

type reversedLog struct{ *Store }

func (this reversedLog) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil {
		return nil, "", err
	}
	slices.Reverse(page)
	return page, cursor, nil
}

type staleAdmission struct{ *Store }

func (this staleAdmission) Append(ctx context.Context, request event.AppendRequest) error {
	if request.Expected > 0 {
		request.Expected--
	}
	return this.Store.Append(ctx, request)
}

// The naive store's cursor: the newest position its log holds rather than the
// highest one every position below which has settled. It delivers exactly what
// the real store delivered and hands back a checkpoint past everything else,
// which is how a consumer loses an event with no error anywhere.
type newestCursor struct{ *Store }

func (this newestCursor) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil {
		return page, cursor, err
	}
	held := this.state.Load()
	if held == nil {
		return page, cursor, nil
	}
	at, unreadable := readCursor(after, held.log)
	if unreadable != nil {
		return page, cursor, nil
	}
	newest, err := this.newestIn(ctx, at.from)
	if err != nil || newest <= at.from {
		return page, cursor, err
	}
	return page, mintCursor(held.log, walk{from: newest}), nil
}

func (this newestCursor) newestIn(ctx context.Context, from uint64) (uint64, error) {
	var newest int64
	row := this.db.QueryRowContext(ctx, "SELECT COALESCE(max(position), 0) FROM "+
		quoteIdentifier(this.schema.Name)+"."+eventsTable+" WHERE position > $1", int64(from))
	if err := row.Scan(&newest); err != nil {
		return 0, err
	}
	return uint64(newest), nil
}

// The naive cursor, and the one a store author writes without meaning to: the
// highest position the read's own query returned, rather than the highest one
// every position below which has settled. In a quiescent log the two numbers are
// the same, so this store is behaviourally identical to a correct one everywhere
// except across a gap a writer still holds — where it hands back a checkpoint
// past a position it did not deliver, and the walk resumed from it never returns
// that event.
type newestFetched struct{ *Store }

func (this newestFetched) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil {
		return page, cursor, err
	}
	held := this.state.Load()
	if held == nil {
		return page, cursor, nil
	}
	at, unreadable := readCursor(after, held.log)
	if unreadable != nil {
		return page, cursor, nil
	}
	newest, err := this.newestFetchedIn(ctx, at.from)
	if err != nil || newest <= at.from {
		return page, cursor, err
	}
	return page, mintCursor(held.log, walk{from: newest}), nil
}

func (this newestFetched) newestFetchedIn(ctx context.Context, from uint64) (uint64, error) {
	var newest int64
	row := this.db.QueryRowContext(ctx, "SELECT COALESCE(max(position), 0) FROM (SELECT position FROM "+
		quoteIdentifier(this.schema.Name)+"."+eventsTable+" WHERE position > $1 ORDER BY position LIMIT "+
		strconv.Itoa(this.limits.MaxRead)+") AS fetched", int64(from))
	if err := row.Scan(&newest); err != nil {
		return 0, err
	}
	return uint64(newest), nil
}

// One buffer for every page this store value ever hands out, which satisfies
// every assertion about a single page and loses the one about two.
type pooledPayloads struct {
	*Store

	mutex  sync.Mutex
	buffer []byte
}

func (this *pooledPayloads) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil {
		return nil, err
	}
	return this.pool(page), nil
}

func (this *pooledPayloads) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil {
		return nil, "", err
	}
	return this.pool(page), cursor, nil
}

func (this *pooledPayloads) pool(page []event.Envelope) []event.Envelope {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	total := 0
	for _, envelope := range page {
		total += len(envelope.Payload)
	}
	if cap(this.buffer) < total {
		this.buffer = make([]byte, total)
	}
	this.buffer = this.buffer[:total]
	at := 0
	for index, envelope := range page {
		copy(this.buffer[at:], envelope.Payload)
		page[index].Payload = this.buffer[at : at+len(envelope.Payload) : at+len(envelope.Payload)]
		at += len(envelope.Payload)
	}
	return page
}

type crossedStream struct{ *Store }

func (this crossedStream) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil {
		return nil, err
	}
	for index := range page {
		page[index].Stream = elsewhere(page[index].Stream)
	}
	return page, nil
}

func elsewhere(stream event.Stream) event.Stream {
	key := []byte(stream.Key)
	if len(key) == 0 {
		return stream
	}
	if key[len(key)-1] == 'z' {
		key[len(key)-1] = 'y'
	} else {
		key[len(key)-1] = 'z'
	}
	return event.Stream{Family: stream.Family, Key: event.Key(key)}
}
