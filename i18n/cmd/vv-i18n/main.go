package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"unicode"

	"github.com/frostgrove/vv/i18n"
)

const maximumCommandInput = 128 << 20

var errCommandUsage = errors.New("invalid command usage")

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }

func (values *stringList) Set(value string) error {
	if value == "" {
		return fmt.Errorf("value is empty")
	}
	*values = append(*values, value)
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(runCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runCLI(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if ctx == nil || stdin == nil || stdout == nil || stderr == nil {
		return 2
	}
	if len(arguments) == 0 {
		writeUsage(stderr)
		return 2
	}
	if arguments[0] == "help" || arguments[0] == "-h" || arguments[0] == "--help" {
		writeUsage(stdout)
		return 0
	}
	var err error
	switch arguments[0] {
	case "check":
		err = runCheck(ctx, arguments[1:], stdin, stdout, stderr)
	case "compile":
		err = runCompile(ctx, arguments[1:], stdin, stdout, stderr)
	case "pseudo":
		err = runPseudo(ctx, arguments[1:], stdin, stdout, stderr)
	case "generate-go":
		err = runGenerateGo(ctx, arguments[1:], stdin, stdout, stderr)
	case "export-ts":
		err = runExportTS(ctx, arguments[1:], stdin, stdout, stderr)
	case "extract":
		err = runExtract(ctx, arguments[1:], stdout, stderr)
	case "review":
		err = runReview(ctx, arguments[1:], stdin, stdout, stderr)
	case "merge":
		err = runMerge(ctx, arguments[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "vv-i18n: unknown command %q\n", arguments[0])
		writeUsage(stderr)
		return 2
	}
	if err == nil {
		return 0
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintf(stderr, "vv-i18n: %v\n", err)
	if errors.Is(err, errCommandUsage) {
		return 2
	}
	return 1
}

func writeUsage(destination io.Writer) {
	fmt.Fprintln(destination, "Usage: vv-i18n <check|compile|pseudo|generate-go|export-ts|extract|review|merge> [options]")
}

func commandFlags(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func parseFlags(flags *flag.FlagSet, arguments []string) error {
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return flag.ErrHelp
		}
		return fmt.Errorf("%w: %v", errCommandUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: unexpected argument %q", errCommandUsage, flags.Arg(0))
	}
	return nil
}

func requireFlag(name, value string) error {
	if value == "" {
		return fmt.Errorf("%w: -%s is required", errCommandUsage, name)
	}
	return nil
}

func runCheck(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := commandFlags("check", stderr)
	limitsPath := bindCommandLimits(flags)
	source := flags.String("source", "", "canonical source JSON path, or - for stdin")
	usage := flags.String("usage", "", "optional extracted usage manifest")
	out := flags.String("out", "-", "report path, or - for stdout")
	strict := flags.Bool("strict", false, "treat optional-locale gaps as errors")
	maximum := flags.Int("max-findings", 0, "maximum reported findings")
	drift := flags.Bool("check", false, "fail if the output file is stale")
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	if err := requireFlag("source", *source); err != nil {
		return err
	}
	limits, err := readCommandLimits(ctx, *limitsPath)
	if err != nil {
		return err
	}
	spec, err := readSourceWithLimits(ctx, *source, stdin, limits)
	if err != nil {
		return err
	}
	policy := i18n.CheckPolicy{StrictOptional: *strict, MaxFindings: *maximum}
	if *usage != "" {
		raw, readErr := readRegularFile(ctx, *usage, maximumCommandInput)
		if readErr != nil {
			return readErr
		}
		policy.Usage, err = decodeUsageContext(ctx, raw)
		if err != nil {
			return err
		}
	}
	report := i18n.CheckContext(ctx, spec, policy)
	encoded, err := encodeReportBoundedContext(ctx, report, limits.outputBytes())
	if err != nil {
		return err
	}
	if err := writeCommandOutputBounded(ctx, *out, encoded, *drift, stdout, limits.outputBytes()); err != nil {
		return err
	}
	return report.Err()
}

func runCompile(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := commandFlags("compile", stderr)
	limitsPath := bindCommandLimits(flags)
	source := flags.String("source", "", "canonical source JSON path, or - for stdin")
	out := flags.String("out", "", "compiled artifact path, or - for stdout")
	drift := flags.Bool("check", false, "fail if the output file is stale")
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	if err := requireFlag("source", *source); err != nil {
		return err
	}
	if err := requireFlag("out", *out); err != nil {
		return err
	}
	limits, err := readCommandLimits(ctx, *limitsPath)
	if err != nil {
		return err
	}
	spec, err := readSourceWithLimits(ctx, *source, stdin, limits)
	if err != nil {
		return err
	}
	raw, err := limits.compiler().CompileContext(ctx, spec)
	if err != nil {
		return err
	}
	return writeCommandOutputBounded(ctx, *out, raw, *drift, stdout, limits.compiledBytes())
}

func runPseudo(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := commandFlags("pseudo", stderr)
	limitsPath := bindCommandLimits(flags)
	source := flags.String("source", "", "canonical source JSON path, or - for stdin")
	out := flags.String("out", "", "updated source path, or - for stdout")
	locale := flags.String("locale", "", "pseudolocale tag")
	mode := flags.String("mode", "accent", "accent or rtl")
	addSupported := flags.Bool("add-supported", true, "add the pseudolocale to supported locales")
	drift := flags.Bool("check", false, "fail if the output file is stale")
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	for _, required := range [][2]string{{"source", *source}, {"out", *out}, {"locale", *locale}} {
		if err := requireFlag(required[0], required[1]); err != nil {
			return err
		}
	}
	limits, err := readCommandLimits(ctx, *limitsPath)
	if err != nil {
		return err
	}
	spec, err := readSourceWithLimits(ctx, *source, stdin, limits)
	if err != nil {
		return err
	}
	canonicalLocale, err := canonicalCommandLocale(*locale)
	if err != nil {
		return err
	}
	if *addSupported && !slices.Contains(spec.Supported, canonicalLocale) {
		spec.Supported = append(spec.Supported, canonicalLocale)
	}
	pseudoMode := i18n.PseudoAccent
	switch *mode {
	case "accent":
	case "rtl":
		pseudoMode = i18n.PseudoRTL
	default:
		return fmt.Errorf("%w: unknown pseudolocale mode %q", errCommandUsage, *mode)
	}
	updated, err := i18n.PseudoContext(ctx, spec, i18n.PseudoSpec{Locale: canonicalLocale, Mode: pseudoMode, MaxOutputBytes: limits.sourceBytes()})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := limits.sourceCodec().EncodeContext(ctx, updated)
	if err != nil {
		return err
	}
	return writeCommandOutputBounded(ctx, *out, raw, *drift, stdout, limits.sourceBytes())
}

func runGenerateGo(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := commandFlags("generate-go", stderr)
	limitsPath := bindCommandLimits(flags)
	source := flags.String("source", "", "canonical source JSON path, or - for stdin")
	out := flags.String("out", "", "generated Go path, or - for stdout")
	packageName := flags.String("package", "", "generated Go package")
	drift := flags.Bool("check", false, "fail if the output file is stale")
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	for _, required := range [][2]string{{"source", *source}, {"out", *out}, {"package", *packageName}} {
		if err := requireFlag(required[0], required[1]); err != nil {
			return err
		}
	}
	limits, err := readCommandLimits(ctx, *limitsPath)
	if err != nil {
		return err
	}
	snapshot, err := readSnapshotWithLimits(ctx, *source, stdin, limits)
	if err != nil {
		return err
	}
	raw, err := i18n.GenerateGoContext(ctx, snapshot, i18n.GoGeneratorSpec{Package: *packageName, MaxOutputBytes: limits.outputBytes()})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeCommandOutputBounded(ctx, *out, raw, *drift, stdout, limits.outputBytes())
}

func runExportTS(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := commandFlags("export-ts", stderr)
	limitsPath := bindCommandLimits(flags)
	source := flags.String("source", "", "canonical source JSON path, or - for stdin")
	out := flags.String("out", "", "generated TypeScript path")
	manifest := flags.String("manifest", "", "public contract manifest path")
	address := flags.String("address-out", "", "optional content-address output path, or - for stdout")
	publicationRoot := flags.String("publication-root", "", "optional crash-safe generational publication root")
	drift := flags.Bool("check", false, "fail if any output file is stale")
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	if err := requireFlag("source", *source); err != nil {
		return err
	}
	if *publicationRoot != "" {
		if *out != "" || *manifest != "" || *address != "" || *publicationRoot == "-" {
			return fmt.Errorf("%w: -publication-root is mutually exclusive with -out, -manifest and -address-out and must be a directory path", errCommandUsage)
		}
	} else {
		for _, required := range [][2]string{{"out", *out}, {"manifest", *manifest}} {
			if err := requireFlag(required[0], required[1]); err != nil {
				return err
			}
		}
		if *out == "-" || *manifest == "-" {
			return fmt.Errorf("%w: export-ts requires file paths for -out and -manifest", errCommandUsage)
		}
	}
	limits, err := readCommandLimits(ctx, *limitsPath)
	if err != nil {
		return err
	}
	snapshot, err := readSnapshotWithLimits(ctx, *source, stdin, limits)
	if err != nil {
		return err
	}
	exported, err := i18n.ExportPublicContext(ctx, snapshot, i18n.PublicExportSpec{MaxOutputBytes: limits.outputBytes()})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if *publicationRoot != "" {
		return writePublicPublicationBounded(ctx, *publicationRoot, exported, *drift, publicationHooks{}, limits.outputBytes())
	}
	outputs := []outputFile{
		{path: *out, content: exported.TypeScript},
		{path: *manifest, content: exported.Manifest},
	}
	if *address != "" && *address != "-" {
		outputs = append(outputs, outputFile{path: *address, content: []byte(exported.Address + "\n")})
	}
	if err := writeOutputSetBounded(ctx, outputs, *drift, limits.outputBytes()); err != nil {
		return err
	}
	if *address == "-" {
		return writeCommandOutputBounded(ctx, *address, []byte(exported.Address+"\n"), *drift, stdout, limits.outputBytes())
	}
	return nil
}

func runExtract(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	flags := commandFlags("extract", stderr)
	limitsPath := bindCommandLimits(flags)
	var roots stringList
	flags.Var(&roots, "root", "Go source file or directory; repeatable")
	out := flags.String("out", "", "usage manifest path, or - for stdout")
	tagList := flags.String("tags", "", "comma-separated build tags; defaults to GOFLAGS -tags")
	complete := flags.Bool("complete", false, "assert complete effective-build module-root coverage; partial roots and unresolved imports downgrade it")
	drift := flags.Bool("check", false, "fail if the output file is stale")
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	if err := requireFlag("out", *out); err != nil {
		return err
	}
	limits, err := readCommandLimits(ctx, *limitsPath)
	if err != nil {
		return err
	}
	tagsSet := false
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "tags" {
			tagsSet = true
		}
	})
	var buildTags []string
	if tagsSet {
		var err error
		buildTags, err = extractBuildTags(*tagList, "", true)
		if err != nil {
			return fmt.Errorf("%w: %v", errCommandUsage, err)
		}
	}
	usage, err := extractUsageWithTags(ctx, roots, *complete, buildTags)
	if err != nil {
		return err
	}
	raw, err := encodeUsageBoundedContext(ctx, usage, limits.outputBytes())
	if err != nil {
		return err
	}
	return writeCommandOutputBounded(ctx, *out, raw, *drift, stdout, limits.outputBytes())
}

func usageGOFLAGSModeled(value string) bool {
	fields, err := splitGoFlags(value)
	if err != nil {
		return false
	}
	for index := 0; index < len(fields); index++ {
		name, value, consumed := goFlagValue(fields, index)
		if consumed {
			index++
		}
		if (name == "overlay" || name == "modfile" || name == "tags" || name == "mod" || name == "toolexec") && value == "" {
			return false
		}
		if name == "toolexec" || name == "mod" && value == "mod" {
			return false
		}
	}
	return true
}

func extractBuildTags(explicit, goFlags string, explicitSet bool) ([]string, error) {
	value := explicit
	if !explicitSet {
		fields := strings.Fields(goFlags)
		for index := 0; index < len(fields); index++ {
			field := fields[index]
			switch {
			case field == "-tags":
				if index+1 >= len(fields) {
					return nil, fmt.Errorf("GOFLAGS -tags has no value")
				}
				index++
				value = fields[index]
			case strings.HasPrefix(field, "-tags="):
				value = strings.TrimPrefix(field, "-tags=")
			}
		}
	}
	if value == "" && explicitSet {
		return []string{}, nil
	}
	if value == "" {
		return nil, nil
	}
	if len(value) > maximumExtractTagBytes {
		return nil, fmt.Errorf("build tags exceed %d bytes", maximumExtractTagBytes)
	}
	if strings.ContainsAny(value, "'\"") {
		return nil, fmt.Errorf("quoted build tags are ambiguous; pass -tags explicitly")
	}
	tags := strings.FieldsFunc(value, func(value rune) bool { return value == ',' || unicode.IsSpace(value) })
	if len(tags) == 0 {
		return nil, fmt.Errorf("build tags are empty")
	}
	if len(tags) > maximumExtractTags {
		return nil, fmt.Errorf("build tags exceed %d entries", maximumExtractTags)
	}
	seen := make(map[string]bool, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		for _, value := range tag {
			if value != '_' && value != '.' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
				return nil, fmt.Errorf("invalid build tag %q", tag)
			}
		}
		if !seen[tag] {
			seen[tag] = true
			result = append(result, tag)
		}
	}
	slices.Sort(result)
	return result, nil
}

func runReview(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := commandFlags("review", stderr)
	limitsPath := bindCommandLimits(flags)
	source := flags.String("source", "", "canonical source JSON path, or - for stdin")
	out := flags.String("out", "", "updated source path, or - for stdout")
	locale := flags.String("locale", "", "translation locale")
	key := flags.String("key", "", "optional qualified message key")
	scope := flags.String("scope", "all", "translations, overrides, or all")
	stateName := flags.String("state", "", "approved, required, or rejected")
	drift := flags.Bool("check", false, "fail if the output file is stale")
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	for _, required := range [][2]string{{"source", *source}, {"out", *out}, {"locale", *locale}, {"state", *stateName}} {
		if err := requireFlag(required[0], required[1]); err != nil {
			return err
		}
	}
	state, err := parseReviewState(*stateName)
	if err != nil {
		return fmt.Errorf("%w: %v", errCommandUsage, err)
	}
	limits, err := readCommandLimits(ctx, *limitsPath)
	if err != nil {
		return err
	}
	spec, err := readSourceWithLimits(ctx, *source, stdin, limits)
	if err != nil {
		return err
	}
	updated, _, err := reviewCatalogContext(ctx, spec, reviewSelector{locale: *locale, key: i18n.Key(*key), scope: *scope, state: state})
	if err != nil {
		return err
	}
	raw, err := limits.sourceCodec().EncodeContext(ctx, updated)
	if err != nil {
		return err
	}
	return writeCommandOutputBounded(ctx, *out, raw, *drift, stdout, limits.sourceBytes())
}

func readSource(ctx context.Context, path string, stdin io.Reader) (i18n.CatalogSpec, error) {
	return readSourceWithLimits(ctx, path, stdin, commandLimits{})
}

func readSourceWithLimits(ctx context.Context, path string, stdin io.Reader, limits commandLimits) (i18n.CatalogSpec, error) {
	codec := limits.sourceCodec()
	if path == "-" {
		return codec.Decode(ctx, stdin)
	}
	file, err := openRegularFile(ctx, path, int64(limits.sourceBytes()))
	if err != nil {
		return i18n.CatalogSpec{}, err
	}
	defer file.Close()
	return codec.Decode(ctx, file)
}

func readSnapshot(ctx context.Context, path string, stdin io.Reader) (*i18n.Snapshot, error) {
	return readSnapshotWithLimits(ctx, path, stdin, commandLimits{})
}

func readSnapshotWithLimits(ctx context.Context, path string, stdin io.Reader, limits commandLimits) (*i18n.Snapshot, error) {
	spec, err := readSourceWithLimits(ctx, path, stdin, limits)
	if err != nil {
		return nil, err
	}
	return i18n.NewContext(ctx, spec)
}

func writeCommandOutput(ctx context.Context, path string, content []byte, check bool, stdout io.Writer) error {
	return writeCommandOutputBounded(ctx, path, content, check, stdout, maximumCommandInput)
}

func writeCommandOutputBounded(ctx context.Context, path string, content []byte, check bool, stdout io.Writer, maximum int) error {
	if ctx == nil {
		return errors.New("output context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return fmt.Errorf("output limit %d is outside supported bounds", maximum)
	}
	if len(content) > maximum {
		return fmt.Errorf("output %q exceeds %d bytes", path, maximum)
	}
	if path != "-" {
		return writeOutputBounded(ctx, path, content, check, maximum)
	}
	if check {
		return fmt.Errorf("%w: -check requires a file output", errCommandUsage)
	}
	return writeWriterContext(ctx, stdout, content)
}

func writeWriterContext(ctx context.Context, destination io.Writer, content []byte) error {
	if ctx == nil {
		return errors.New("write context is nil")
	}
	for len(content) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		written, err := destination.Write(content)
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		if written <= 0 || written > len(content) {
			return io.ErrShortWrite
		}
		content = content[written:]
	}
	return ctx.Err()
}
