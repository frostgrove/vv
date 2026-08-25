package sqlerr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A Corpus is every violation one engine was asked to produce, with the error
// it produced recorded verbatim.
//
// It exists because the engine matrix this replaced was written from memory and
// half of it was wrong. MySQL answers a failed CHECK with 3819 and SQLSTATE
// HY000, which no reading of the specification predicts and which meant the
// shipped classifier returned 500 where the documentation promised 409. A table
// nobody provoked is a guess with a citation.
type Corpus struct {
	// Engine is the parser this file selects: postgres, mysql, mariadb, sqlite.
	// MariaDB is separate from MySQL because it is not the same: a failed CHECK
	// is 4025 with SQLSTATE 23000 here and 3819 with HY000 there.
	Engine string `json:"engine"`
	// Server is what the server calls itself. Recorded for the human reading a
	// diff, never asserted — see Err.SameKey.
	Server string `json:"server"`
	Driver string `json:"driver"`
	Cases  []Case `json:"cases"`
}

// The kinds a case can belong to. They are the contract between the corpus and
// what the shipped classifier does with it, which is what lets the corpus test
// code that exists rather than only code that is planned.
const (
	// KindIntegrity is a constraint the database refused to break. These, and
	// only these, reach a caller as crud.ErrConflict.
	KindIntegrity = "integrity"
	// KindData is a value the column could not hold. It is the caller's input
	// to fix, but it is not a conflict, and [[D-015]] refuses to widen the
	// conflict classifier to cover it.
	KindData = "data"
	// KindRetryable is a lock failure, a serialisation failure, or a transaction
	// the engine aborted out from under the caller. Nothing the caller sent is
	// wrong, so answering 4xx would tell them to fix something they cannot.
	KindRetryable = "retryable"
	// KindNone is an error that must stay unclassified. Without these the corpus
	// only proves that a classifier says yes, never that it knows when to say
	// no — and a parser that classifies everything is worse than one that
	// classifies nothing.
	KindNone = "none"
)

// A Case is one violation, deliberately provoked.
type Case struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Want is the class a parser must produce, or "" when the case must stay
	// unclassified.
	Want string `json:"want"`
	// Stmt is what was run. Kept so a reader can see the entry is real, and so
	// the capture can be repeated without reading the generator.
	Stmt string `json:"stmt"`
	// Unreachable says this engine will not produce this violation at all, and
	// why. SQLite does not enforce a declared VARCHAR width, so the same payload
	// is 422 on PostgreSQL and 200 there. That is an observable dialect
	// difference and belongs in the file rather than in somebody's memory.
	Unreachable string `json:"unreachable,omitempty"`
	Err         *Err   `json:"err"`
}

// An Err is a driver error, flattened. It is the corpus's record of what a
// server said and it is also what [Classify] takes: the parsers are written
// against these entries and then run on them, so nothing separates the thing
// tested from the thing shipped.
//
// Message is carried and never read. It is here so a test can prove that — a
// parser handed an Err whose Message says something else entirely must answer
// the same ([[D-039]]).
type Err struct {
	// Type is the driver's own type name. An error carrying no SQLSTATE at all
	// is a legitimate corpus entry, and the type is then the only thing that
	// distinguishes it from a bug in the capture.
	Type string `json:"type"`
	// SQLState is empty for SQLite, which has none, and for a failure that never
	// reached a server.
	SQLState string `json:"sqlstate"`
	// Native is the engine's own number: MySQL's 1062, SQLite's extended result
	// code. Zero when the driver reports none.
	Native uint64 `json:"native"`
	// Message is recorded and never asserted. PostgreSQL, MySQL and MariaDB all
	// localise it through the session's language setting, so a parser that reads
	// it works until somebody deploys a server in another locale.
	Message string `json:"message"`
	// Fields are the driver's structured extras — constraint, table, schema,
	// column, detail. This is where a column name may honestly come from.
	//
	// A value may be a fixed marker where the server's own changes every run:
	// PostgreSQL's deadlock detail names the backend pids. The field name is
	// what SameKey compares, so redacting the value costs the guard nothing.
	Fields map[string]string `json:"fields,omitempty"`
}

// SameKey answers whether two captures would classify the same way.
//
// It compares the type, the SQLSTATE, the native number and which structured
// fields the driver populated — and deliberately not the message, nor the
// server version. The compose file tracks floating tags, so a patch release
// that rewords one sentence would otherwise turn the whole suite red over a
// change no parser can see. What a parser is keyed on is asserted; what it may
// not read is recorded for a human.
func (e *Err) SameKey(o *Err) bool {
	if e == nil || o == nil {
		return e == o
	}
	if e.Type != o.Type || e.SQLState != o.SQLState || e.Native != o.Native {
		return false
	}
	return strings.Join(fieldNames(e.Fields), ",") == strings.Join(fieldNames(o.Fields), ",")
}

// Key renders the tuple a classifier dispatches on, for a test failure that
// says what changed rather than printing two structs.
func (e *Err) Key() string {
	if e == nil {
		return "<no error>"
	}
	return fmt.Sprintf("%s sqlstate=%q native=%d fields=[%s]",
		e.Type, e.SQLState, e.Native, strings.Join(fieldNames(e.Fields), " "))
}

func fieldNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Case finds one by name.
func (c *Corpus) Case(name string) (Case, bool) {
	for _, cs := range c.Cases {
		if cs.Name == name {
			return cs, true
		}
	}
	return Case{}, false
}

// Path is where one engine's corpus lives under dir.
func Path(dir, engine string) string { return filepath.Join(dir, engine+".json") }

// Load reads one engine's corpus.
func Load(dir, engine string) (*Corpus, error) {
	b, err := os.ReadFile(Path(dir, engine))
	if err != nil {
		return nil, err
	}
	var c Corpus
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(dir, engine), err)
	}
	if c.Engine != engine {
		return nil, fmt.Errorf("%s: says it is %q", Path(dir, engine), c.Engine)
	}
	return &c, nil
}

// Save writes one engine's corpus so that recapturing an unchanged database
// produces a byte-identical file. Nothing here is ordered by a map iteration or
// stamped with a time: a regeneration that diffs against itself is noise, and
// noise is how a real change goes unread.
func Save(dir string, c *Corpus) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(dir, c.Engine), append(b, '\n'), 0o644)
}
