package main

import (
	"context"
	"fmt"
	"io"

	"github.com/frostgrove/vv/i18n"
)

func runMerge(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := commandFlags("merge", stderr)
	limitsPath := bindCommandLimits(flags)
	source := flags.String("source", "", "new canonical source JSON path, or - for stdin")
	previous := flags.String("previous", "", "previous canonical source JSON path, or - for stdin")
	out := flags.String("out", "", "merged source path, or - for stdout")
	prune := flags.Bool("prune-obsolete", false, "discard translations and overrides that no longer have a target")
	drift := flags.Bool("check", false, "fail if the output file is stale")
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	for _, required := range [][2]string{{"source", *source}, {"previous", *previous}, {"out", *out}} {
		if err := requireFlag(required[0], required[1]); err != nil {
			return err
		}
	}
	if *source == "-" && *previous == "-" {
		return fmt.Errorf("%w: -source and -previous cannot both read stdin", errCommandUsage)
	}
	limits, err := readCommandLimits(ctx, *limitsPath)
	if err != nil {
		return err
	}
	newSource, err := readSourceWithLimits(ctx, *source, stdin, limits)
	if err != nil {
		return err
	}
	oldSource, err := readSourceWithLimits(ctx, *previous, stdin, limits)
	if err != nil {
		return err
	}
	merged, err := (i18n.SourceMerger{CatalogLimits: limits.Catalog, SourceLimits: limits.Source}).MergeContext(ctx, newSource, oldSource, i18n.SourceMergePolicy{PruneObsolete: *prune})
	if err != nil {
		return err
	}
	raw, err := limits.sourceCodec().EncodeContext(ctx, merged)
	if err != nil {
		return err
	}
	return writeCommandOutputBounded(ctx, *out, raw, *drift, stdout, limits.sourceBytes())
}
