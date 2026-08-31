package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/frostgrove/vv/errs/sqlerr"
	"github.com/frostgrove/vv/test/corpus"
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

func report(dir string, c *sqlerr.Corpus) {
	old, err := sqlerr.Load(dir, c.Engine)
	if err != nil {
		return
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
