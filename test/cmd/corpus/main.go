// Command corpus recaptures the SQL error corpus from the live databases.
//
//	make corpus
//
// It writes one file per engine under errs/sqlerr/testdata/corpus. Recapturing
// an unchanged set of servers produces byte-identical files, so a diff is always
// a real change — a server upgrade, a driver upgrade, or a case that stopped
// violating what it was written to violate.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/shardit-io/vv/errs/sqlerr"
	"github.com/shardit-io/vv/test/corpus"
)

func main() {
	only := flag.String("engine", "", "capture one engine only")
	out := flag.String("out", "", "where to write (default: the checked-in corpus)")
	flag.Parse()

	if err := run(*out, *only); err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		fmt.Fprintln(os.Stderr, "the databases must be up: docker compose up -d --wait")
		os.Exit(1)
	}
}

func run(out, only string) error {
	if out == "" {
		var err error
		if out, err = corpus.Dir(); err != nil {
			return err
		}
	}
	tmp, err := os.MkdirTemp("", "vv-corpus")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for _, e := range corpus.Engines(tmp) {
		if only != "" && e.Name != only {
			continue
		}
		c, err := corpus.Capture(ctx, e)
		if err != nil {
			return err
		}
		fmt.Printf("%-9s %d cases  %s\n", c.Engine, len(c.Cases), c.Server)
		report(out, c)
		if err := sqlerr.Save(out, c); err != nil {
			return err
		}
	}
	return nil
}

// report says what moved, because a rewritten file that nobody reads is how a
// corpus stops describing anything.
//
// It does not refuse the change. A server upgrade legitimately moves a key, and
// a generator that failed on one could not be used for the thing it is for. The
// judgement belongs to TestTheCorpusStillDescribesTheseServers, which fails on
// the next `make integration`; this is here so the person running the capture
// sees it first, and sees a case that started violating something other than
// what it is named for.
func report(dir string, c *sqlerr.Corpus) {
	old, err := sqlerr.Load(dir, c.Engine)
	if err != nil {
		return // the first capture of this engine: everything is new
	}
	if old.Server != c.Server {
		fmt.Printf("          server was %s\n", old.Server)
	}
	for _, got := range c.Cases {
		was, ok := old.Case(got.Name)
		switch {
		case !ok:
			fmt.Printf("          + %-16s %s\n", got.Name, got.Err.Key())
		case (was.Unreachable == "") != (got.Unreachable == ""):
			fmt.Printf("          ! %-16s reachable %v -> %v\n",
				got.Name, was.Unreachable == "", got.Unreachable == "")
		case !was.Err.SameKey(got.Err):
			fmt.Printf("          ! %-16s %s\n", got.Name, was.Err.Key())
			fmt.Printf("            %-16s %s\n", "", got.Err.Key())
		}
	}
}
