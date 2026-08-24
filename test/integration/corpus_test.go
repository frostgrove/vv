//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/shardit-io/vv/adapter/crudsql"
	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs/sqlerr"
	"github.com/shardit-io/vv/test/corpus"
)

// The corpus is the checked-in record of what each engine says when it refuses
// a statement, and the dialect parsers are written against it rather than
// against anybody's reading of the SQLSTATE specification. Two things have to
// stay true of it, and they are different things.
//
// One: it still describes these servers. Two: what it claims about a violation
// matches what the shipped adapter does with that violation. The first would
// pass on a corpus nothing reads; the second would pass on a corpus captured
// from a server nobody runs.

// The servers have not changed under the corpus.
//
// What is compared is the tuple a classifier dispatches on — driver type,
// SQLSTATE, native number, and which structured fields the driver filled in.
// The message is deliberately not compared. docker-compose.yml tracks floating
// tags, so a patch release that rewords one sentence would otherwise turn this
// red over a change no parser can see, and the fix would be to stop reading the
// failure. The text is still captured, and phase 2 is where a parser proves it
// never reads it.
func TestTheCorpusStillDescribesTheseServers(t *testing.T) {
	ctx := context.Background()
	dir, err := corpus.Dir()
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range corpus.Engines(t.TempDir()) {
		t.Run(e.Name, func(t *testing.T) {
			want, err := sqlerr.Load(dir, e.Name)
			if err != nil {
				t.Fatalf("reading the checked-in corpus: %v", err)
			}
			got, err := corpus.Capture(ctx, e)
			if err != nil {
				t.Fatalf("recapturing: %v", err)
			}
			if len(got.Cases) != len(want.Cases) {
				t.Fatalf("captured %d cases, the corpus has %d — run make corpus",
					len(got.Cases), len(want.Cases))
			}
			for i, g := range got.Cases {
				w := want.Cases[i]
				if g.Name != w.Name {
					t.Fatalf("case %d is %q, the corpus has %q — run make corpus", i, g.Name, w.Name)
				}
				if (g.Unreachable == "") != (w.Unreachable == "") {
					t.Errorf("%s: the corpus says reachable=%v and this server says %v",
						g.Name, w.Unreachable == "", g.Unreachable == "")
					continue
				}
				if !g.Err.SameKey(w.Err) {
					t.Errorf("%s now reports %s\n            the corpus has %s\nrun make corpus, and read the diff before committing it",
						g.Name, g.Err.Key(), w.Err.Key())
				}
			}
		})
	}
}

// Every corpus case reaches a caller as what the corpus says it is.
//
// This is the half with teeth. The adapter answers exactly one question today —
// is this a conflict — and the corpus's Kind is the answer it must give:
// integrity yes, everything else no. Both directions matter and the second one
// more, because a classifier that widened until it caught everything would pass
// every positive here and turn a missing table into a 409.
//
// It also settles two things that were guesses. MySQL reports a failed CHECK as
// 3819 with SQLSTATE HY000 and a missing default as 1364, neither of them class
// 23, so both were unclassified 500s until the number list was added — this is
// what holds that fix down. MariaDB answers the same CHECK with 4025 and
// SQLSTATE 23000, so class 23 already covers it and no number is needed, which
// is why [[D-015]] was right to leave 4025 out and wrong about why.
func TestEveryCorpusCaseClassifiesAsTheCorpusSays(t *testing.T) {
	ctx := context.Background()
	for _, e := range corpus.Engines(t.TempDir()) {
		t.Run(e.Name, func(t *testing.T) {
			db, err := e.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			for _, p := range e.Cases {
				t.Run(p.Name, func(t *testing.T) {
					err := replay(ctx, t, e, db, p)
					if p.Unreachable != "" {
						if err != nil {
							t.Fatalf("the corpus says this engine accepts it — %s — and it did not: %v",
								p.Unreachable, err)
						}
						return
					}
					if err == nil {
						t.Fatal("the database accepted it; the corpus says it refuses it")
					}
					conflict := errors.Is(err, crud.ErrConflict)
					want := p.Kind == sqlerr.KindIntegrity
					if conflict != want {
						t.Fatalf("errors.Is(err, crud.ErrConflict) = %v, want %v for a %q case\ngot %T: %v",
							conflict, want, p.Kind, err, err)
					}
					// A violation must not be mistaken for something else
					// either: a transport turns these into 404 and 400.
					for _, s := range []error{crud.ErrNotFound, crud.ErrMissingID, crud.ErrReadOnly, crud.ErrForbidden} {
						if errors.Is(err, s) {
							t.Fatalf("came back as %v", s)
						}
					}
				})
			}
		})
	}
}

// replay runs one corpus case through the adapter rather than the raw handle,
// so what comes back is what a repository would have handed its caller.
func replay(ctx context.Context, t *testing.T, e corpus.Engine, db *sql.DB, p corpus.Probe) error {
	t.Helper()
	switch {
	case p.Contend:
		// The waiting statement has to run on the connection whose patience was
		// cut, so the source is built over that connection rather than the pool.
		return e.Contend(ctx, db, func(wait *sql.Conn) error {
			_, err := crudsql.Source(wait, e.Dialect).Exec(ctx, e.Lock)
			return err
		})
	case p.Connect != "":
		// A repository bound to a database it cannot reach. The refusal arrives
		// on the first statement and through the same classifier as any other.
		bad, err := sql.Open(e.Driver, p.Connect)
		if err != nil {
			return err
		}
		defer bad.Close()
		_, err = crudsql.Source(bad, e.Dialect).Exec(ctx, "SELECT 1")
		return err
	case p.Stmt != "":
		_, err := crudsql.Source(db, e.Dialect).Exec(ctx, p.Stmt)
		return err
	default:
		// Nothing to run: a case this engine cannot reach at all, which says so
		// in Unreachable and is checked as an absence rather than an error.
		return nil
	}
}
