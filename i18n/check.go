package i18n

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	pathpkg "path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrCheckFailed = errors.New("i18n: source check failed")

const maximumUsageCoordinate = 1 << 30

type UsageLimits struct {
	MaxKeys          int
	MaxDynamic       int
	MaxOccurrences   int
	MaxRoots         int
	MaxFiles         int
	MaxMetadata      int
	MaxTags          int
	MaxEnvironment   int
	MaxStringBytes   int
	MaxMaterialBytes int
}

func DefaultUsageLimits() UsageLimits {
	limits, err := checkedUsageLimits(UsageLimits{})
	if err != nil {
		panic(err)
	}
	return limits
}

type CheckStatus string

const (
	CheckMissing        CheckStatus = "missing"
	CheckStale          CheckStatus = "stale"
	CheckReviewRequired CheckStatus = "review_required"
	CheckRejected       CheckStatus = "rejected"
	CheckUnused         CheckStatus = "unused"
	CheckInvalid        CheckStatus = "invalid"
)

func (s CheckStatus) String() string { return string(s) }

func (s CheckStatus) Valid() bool {
	switch s {
	case CheckMissing, CheckStale, CheckReviewRequired, CheckRejected, CheckUnused, CheckInvalid:
		return true
	default:
		return false
	}
}

type CheckSeverity string

const (
	SeverityError   CheckSeverity = "error"
	SeverityWarning CheckSeverity = "warning"
)

func (s CheckSeverity) String() string { return string(s) }

func (s CheckSeverity) Valid() bool { return s == SeverityError || s == SeverityWarning }

type DynamicUsage struct {
	Domain string `json:"domain"`
	Prefix string `json:"prefix"`
}

type UsageOccurrence struct {
	Key    Key    `json:"key,omitempty"`
	Domain string `json:"domain,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type UsageRootKind string

const (
	UsageRootDirectory UsageRootKind = "directory"
	UsageRootFile      UsageRootKind = "file"
)

func (k UsageRootKind) String() string { return string(k) }

func (k UsageRootKind) Valid() bool { return k == UsageRootDirectory || k == UsageRootFile }

type UsageRoot struct {
	Path string        `json:"path"`
	Kind UsageRootKind `json:"kind"`
}

const GoUsageAnalyzerV1 = "frostgrove.vv-i18n/go-ast/v1"
const GoUsageAnalyzerV2 = "frostgrove.vv-i18n/go-list/v2"

type UsageFile struct {
	Root        string `json:"root"`
	Path        string `json:"path"`
	LogicalPath string `json:"logical_path"`
	SHA256      string `json:"sha256"`
	Selected    bool   `json:"selected"`
}

type UsageMetadata struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type UsageSetting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type GoUsageScope struct {
	Analyzer      string          `json:"analyzer"`
	GOOS          string          `json:"goos"`
	GOARCH        string          `json:"goarch"`
	Compiler      string          `json:"compiler"`
	CgoEnabled    bool            `json:"cgo_enabled"`
	GoVersion     string          `json:"go_version,omitempty"`
	Toolchain     string          `json:"toolchain,omitempty"`
	GoExperiment  string          `json:"go_experiment,omitempty"`
	GoFlags       string          `json:"go_flags,omitempty"`
	GoWork        string          `json:"go_work,omitempty"`
	GoEnv         string          `json:"go_env,omitempty"`
	Environment   []UsageSetting  `json:"environment,omitempty"`
	BuildTags     []string        `json:"build_tags"`
	ToolTags      []string        `json:"tool_tags"`
	ReleaseTags   []string        `json:"release_tags"`
	Roots         []UsageRoot     `json:"roots"`
	Files         []UsageFile     `json:"files,omitempty"`
	Metadata      []UsageMetadata `json:"metadata,omitempty"`
	SourceDigest  string          `json:"source_digest"`
	SelectedFiles int             `json:"selected_files"`
	ExcludedFiles int             `json:"excluded_files"`
}

type UsageManifest struct {
	Keys           []Key             `json:"keys"`
	Dynamic        []DynamicUsage    `json:"dynamic"`
	Occurrences    []UsageOccurrence `json:"occurrences,omitempty"`
	GoScope        *GoUsageScope     `json:"go_scope,omitempty"`
	ManifestDigest string            `json:"manifest_digest,omitempty"`
	Complete       bool              `json:"complete"`
}

func ExpectedUsageSourceDigest(scope GoUsageScope) string {
	digest, _ := ExpectedUsageSourceDigestContext(context.Background(), scope)
	return digest
}

func ExpectedUsageSourceDigestContext(ctx context.Context, scope GoUsageScope) (string, error) {
	if ctx == nil {
		return "", errors.New("i18n: usage source digest context is nil")
	}
	digest := sha256.New()
	writer := usageDigestWriter{ctx: ctx, write: digest.Write}
	if err := writer.field("domain", "frostgrove.i18n.go-usage-source/v2"); err != nil {
		return "", err
	}
	if err := writeUsageScopeDigest(&writer, scope); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func ExpectedUsageManifestDigest(manifest UsageManifest) string {
	digest, _ := ExpectedUsageManifestDigestContext(context.Background(), manifest)
	return digest
}

func ExpectedUsageManifestDigestContext(ctx context.Context, manifest UsageManifest) (string, error) {
	if ctx == nil {
		return "", errors.New("i18n: usage manifest digest context is nil")
	}
	digest := sha256.New()
	writer := usageDigestWriter{ctx: ctx, write: digest.Write}
	if err := writer.field("domain", "frostgrove.i18n.usage-manifest/v1"); err != nil {
		return "", err
	}
	if err := writer.field("complete", strconv.FormatBool(manifest.Complete)); err != nil {
		return "", err
	}
	if manifest.GoScope != nil {
		if err := writer.field("source-digest", manifest.GoScope.SourceDigest); err != nil {
			return "", err
		}
	}
	for _, key := range manifest.Keys {
		if err := writer.field("key", string(key)); err != nil {
			return "", err
		}
	}
	for _, dynamic := range manifest.Dynamic {
		if err := writer.field("dynamic-domain", dynamic.Domain); err != nil {
			return "", err
		}
		if err := writer.field("dynamic-prefix", dynamic.Prefix); err != nil {
			return "", err
		}
	}
	for _, occurrence := range manifest.Occurrences {
		for _, field := range []struct{ name, value string }{
			{"occurrence-key", string(occurrence.Key)}, {"occurrence-domain", occurrence.Domain},
			{"occurrence-prefix", occurrence.Prefix}, {"occurrence-path", occurrence.Path},
			{"occurrence-line", strconv.Itoa(occurrence.Line)}, {"occurrence-column", strconv.Itoa(occurrence.Column)},
		} {
			if err := writer.field(field.name, field.value); err != nil {
				return "", err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

type usageDigestWriter struct {
	ctx   context.Context
	write func([]byte) (int, error)
	work  uint64
}

func (w *usageDigestWriter) field(name, value string) error {
	if w.work&255 == 0 {
		if err := w.ctx.Err(); err != nil {
			return err
		}
	}
	w.work++
	_, _ = w.write([]byte(strconv.Itoa(len(name))))
	_, _ = w.write([]byte{':'})
	_, _ = w.write([]byte(name))
	_, _ = w.write([]byte(strconv.Itoa(len(value))))
	_, _ = w.write([]byte{':'})
	_, _ = w.write([]byte(value))
	return nil
}

func writeUsageScopeDigest(writer *usageDigestWriter, scope GoUsageScope) error {
	for _, value := range []struct{ name, value string }{
		{"analyzer", scope.Analyzer}, {"goos", scope.GOOS}, {"goarch", scope.GOARCH}, {"compiler", scope.Compiler},
		{"cgo", strconv.FormatBool(scope.CgoEnabled)}, {"go-version", scope.GoVersion}, {"toolchain", scope.Toolchain},
		{"go-experiment", scope.GoExperiment}, {"go-flags", scope.GoFlags}, {"go-work", scope.GoWork}, {"go-env", scope.GoEnv},
	} {
		if err := writer.field(value.name, value.value); err != nil {
			return err
		}
	}
	for _, tag := range scope.BuildTags {
		if err := writer.field("build-tag", tag); err != nil {
			return err
		}
	}
	for _, tag := range scope.ToolTags {
		if err := writer.field("tool-tag", tag); err != nil {
			return err
		}
	}
	for _, tag := range scope.ReleaseTags {
		if err := writer.field("release-tag", tag); err != nil {
			return err
		}
	}
	for _, setting := range scope.Environment {
		if err := writer.field("environment-name", setting.Name); err != nil {
			return err
		}
		if err := writer.field("environment-value", setting.Value); err != nil {
			return err
		}
	}
	for _, root := range scope.Roots {
		if err := writer.field("root-kind", root.Kind.String()); err != nil {
			return err
		}
		if err := writer.field("root-path", root.Path); err != nil {
			return err
		}
	}
	for _, metadata := range scope.Metadata {
		for _, field := range []struct{ name, value string }{
			{"metadata-kind", metadata.Kind}, {"metadata-path", metadata.Path}, {"metadata-sha256", metadata.SHA256},
		} {
			if err := writer.field(field.name, field.value); err != nil {
				return err
			}
		}
	}
	for _, file := range scope.Files {
		for _, field := range []struct{ name, value string }{
			{"file-root", file.Root}, {"file-path", file.Path}, {"file-logical-path", file.LogicalPath},
			{"file-selected", strconv.FormatBool(file.Selected)}, {"file-sha256", file.SHA256},
		} {
			if err := writer.field(field.name, field.value); err != nil {
				return err
			}
		}
	}
	return nil
}

type CheckPolicy struct {
	Usage          UsageManifest `json:"usage"`
	UsageLimits    UsageLimits   `json:"usage_limits"`
	StrictOptional bool          `json:"strict_optional"`
	MaxFindings    int           `json:"max_findings"`
}

type Finding struct {
	Status   CheckStatus   `json:"status"`
	Severity CheckSeverity `json:"severity"`
	Path     string        `json:"path"`
	Key      Key           `json:"key"`
	Locale   string        `json:"locale"`
	Detail   string        `json:"detail"`
}

type LocaleCoverage struct {
	Locale         string `json:"locale"`
	Required       bool   `json:"required"`
	Total          int    `json:"total"`
	Approved       int    `json:"approved"`
	Missing        int    `json:"missing"`
	Stale          int    `json:"stale"`
	ReviewRequired int    `json:"review_required"`
	Rejected       int    `json:"rejected"`
	Invalid        int    `json:"invalid"`
}

type Report struct {
	SourceDigest     string           `json:"source_digest"`
	Findings         []Finding        `json:"findings"`
	Coverage         []LocaleCoverage `json:"coverage"`
	Errors           int              `json:"errors"`
	Warnings         int              `json:"warnings"`
	StructuralErrors int              `json:"structural_errors"`
	FirstStructural  *Finding         `json:"first_structural,omitempty"`
	Truncated        bool             `json:"truncated"`
}

func (r Report) OK() bool { return r.Errors == 0 }

func (r Report) Error() string {
	if r.OK() {
		return ""
	}
	return fmt.Sprintf("%s: %d error(s), %d warning(s)", ErrCheckFailed, r.Errors, r.Warnings)
}

func (r Report) Unwrap() error {
	if r.OK() {
		return nil
	}
	return ErrCheckFailed
}

func (r Report) Err() error {
	if r.OK() {
		return nil
	}
	return r
}

func (r Report) FirstStructuralError() (Finding, bool) {
	if r.FirstStructural == nil {
		return Finding{}, false
	}
	return *r.FirstStructural, true
}

type checkCollector struct {
	maximum          int
	findings         []Finding
	errors           int
	warnings         int
	structuralErrors int
	firstStructural  Finding
	hasStructural    bool
	truncated        bool
	ctx              context.Context
	work             uint64
	stopped          bool
	warningsAt       int
}

func (c *checkCollector) add(finding Finding) {
	finding.Path = boundedProblemText(finding.Path)
	finding.Detail = boundedProblemText(finding.Detail)
	finding.Key = Key(boundedProblemText(string(finding.Key)))
	finding.Locale = boundedProblemText(finding.Locale)
	switch finding.Severity {
	case SeverityError:
		c.errors++
	case SeverityWarning:
		c.warnings++
	default:
		finding.Severity = SeverityError
		c.errors++
	}
	if finding.Status == CheckInvalid {
		c.structuralErrors++
		if !c.hasStructural || compareFinding(finding, c.firstStructural) < 0 {
			c.firstStructural = finding
			c.hasStructural = true
		}
	}
	if len(c.findings) >= c.maximum {
		c.truncated = true
		if finding.Severity == SeverityError && c.hasRetainedWarning() {
			c.findings[c.warningsAt] = finding
			c.warningsAt++
		}
		return
	}
	c.findings = append(c.findings, finding)
}

func (c *checkCollector) retains(severity CheckSeverity) bool {
	if len(c.findings) < c.maximum {
		return true
	}
	if severity != SeverityError {
		return false
	}
	return c.hasRetainedWarning()
}

func (c *checkCollector) hasRetainedWarning() bool {
	for c.warningsAt < len(c.findings) && c.findings[c.warningsAt].Severity != SeverityWarning {
		c.warningsAt++
	}
	return c.warningsAt < len(c.findings)
}

func (c *checkCollector) addCount(severity CheckSeverity, count int) {
	if count < 1 {
		return
	}
	switch severity {
	case SeverityError:
		c.errors += count
	case SeverityWarning:
		c.warnings += count
	default:
		c.errors += count
	}
	c.truncated = true
}

func (c *checkCollector) canceled() bool {
	if c.stopped {
		return true
	}
	if c.ctx == nil {
		c.stopped = true
		return true
	}
	select {
	case <-c.ctx.Done():
		c.stopped = true
		return true
	default:
		return false
	}
}

func (c *checkCollector) pollCancellation() bool {
	c.work++
	if c.work&255 != 0 {
		return c.stopped
	}
	return c.canceled()
}

type checkedMessage struct {
	moduleIndex  int
	messageIndex int
	module       string
	key          Key
	message      MessageSpec
	expected     string
	invalid      bool
}

type checkMessageLocale struct {
	message int
	locale  string
}

type checkOverrideLocale struct {
	key    Key
	locale string
}

type indexedCheckOverride struct {
	index    int
	override Override
}

type checkInvalidPathIndex map[string]bool

func newCheckInvalidPathIndex(paths []string) checkInvalidPathIndex {
	index := make(checkInvalidPathIndex, len(paths))
	for _, path := range paths {
		index.add(path)
	}
	return index
}

func (i checkInvalidPathIndex) add(path string) {
	for path != "" {
		i[path] = true
		separator := strings.LastIndexByte(path, '.')
		if separator < 0 {
			return
		}
		path = path[:separator]
	}
}

func Check(spec CatalogSpec, policy CheckPolicy) Report {
	return CheckContext(context.Background(), spec, policy)
}

func checkedUsageLimits(limits UsageLimits) (UsageLimits, error) {
	defaultInt(&limits.MaxKeys, 1<<18)
	defaultInt(&limits.MaxDynamic, 1<<18)
	defaultInt(&limits.MaxOccurrences, 1<<18)
	defaultInt(&limits.MaxRoots, 1024)
	defaultInt(&limits.MaxFiles, 100000)
	defaultInt(&limits.MaxMetadata, 100000)
	defaultInt(&limits.MaxTags, 256)
	defaultInt(&limits.MaxEnvironment, 256)
	defaultInt(&limits.MaxStringBytes, 4<<20)
	defaultInt(&limits.MaxMaterialBytes, 64<<20)
	defaults := UsageLimits{
		MaxKeys: 1 << 18, MaxDynamic: 1 << 18, MaxOccurrences: 1 << 18,
		MaxRoots: 1024, MaxFiles: 100000, MaxMetadata: 100000,
		MaxTags: 256, MaxEnvironment: 256, MaxStringBytes: 4 << 20, MaxMaterialBytes: 64 << 20,
	}
	values := []struct {
		value   int
		maximum int
	}{
		{limits.MaxKeys, defaults.MaxKeys}, {limits.MaxDynamic, defaults.MaxDynamic},
		{limits.MaxOccurrences, defaults.MaxOccurrences}, {limits.MaxRoots, defaults.MaxRoots},
		{limits.MaxFiles, defaults.MaxFiles}, {limits.MaxMetadata, defaults.MaxMetadata},
		{limits.MaxTags, defaults.MaxTags}, {limits.MaxEnvironment, defaults.MaxEnvironment},
		{limits.MaxStringBytes, defaults.MaxStringBytes}, {limits.MaxMaterialBytes, defaults.MaxMaterialBytes},
	}
	for _, value := range values {
		if value.value < 1 || value.value > value.maximum {
			return UsageLimits{}, fmt.Errorf("%w: usage limits are outside their supported bounds", ErrLimitExceeded)
		}
	}
	return limits, nil
}

func CheckContext(ctx context.Context, spec CatalogSpec, policy CheckPolicy) Report {
	maximum := policy.MaxFindings
	if maximum == 0 {
		maximum = 4096
	}
	collector := checkCollector{maximum: maximum, ctx: ctx}
	if maximum < 1 || maximum > 65536 {
		collector.maximum = 1
		collector.add(Finding{
			Status: CheckInvalid, Severity: SeverityError, Path: "policy.max_findings",
			Detail: "maximum findings must be between 1 and 65536",
		})
		return collector.report(nil, "")
	}
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	limits, err := checkedLimits(spec.Limits)
	if err != nil {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "limits", Detail: err.Error()})
		return collector.report(nil, "")
	}
	usageLimits, err := checkedUsageLimits(policy.UsageLimits)
	if err != nil {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "policy.usage_limits", Detail: err.Error()})
		return collector.report(nil, "")
	}
	if err := preflightCatalogContext(ctx, spec, limits); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return canceledCheckReport(maximum)
		}
		addCheckError(&collector, err)
		return collector.report(nil, "")
	}
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	canonical, err := cloneCatalogSpecContext(ctx, spec)
	if err != nil {
		return canceledCheckReport(maximum)
	}
	canonical.Observer = nil
	canonical.Limits = limits
	if canonical.Profile == "" {
		canonical.Profile = GrammarProfile
	}
	if canonical.DefaultLocale == "" {
		canonical.DefaultLocale = canonical.SourceLocale
	}
	if canonical.DefaultTimeZone == "" {
		canonical.DefaultTimeZone = "UTC"
	}
	messages := collectCheckedMessages(canonical, &collector)
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	structural := normalizedCheckSpec(canonical, messages, &collector)
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	invalidPaths := make([]string, 0)
	structuralValid := true
	if _, err := NewContext(ctx, structural); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return canceledCheckReport(maximum)
		}
		structuralValid = false
		invalidPaths = addCheckError(&collector, err)
	}
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	markStructurallyInvalid(messages, invalidPaths, &collector)
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	invalidPathIndex := newCheckInvalidPathIndex(invalidPaths)
	if !structuralValid && !checkOverlayWasValidated(invalidPaths) {
		validateIsolatedOverrides(canonical, messages, invalidPathIndex, &collector)
	}
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	classifyTranslationFindings(canonical, messages, policy, &collector)
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	classifyOverrideFindings(canonical, messages, policy, invalidPathIndex, &collector)
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	used, usageValid := checkUsage(messages, policy.Usage, usageLimits, &collector)
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	if usageValid && policy.Usage.Complete && policy.Usage.GoScope != nil && policy.Usage.GoScope.Analyzer == GoUsageAnalyzerV2 {
		for _, message := range messages {
			if collector.pollCancellation() {
				return canceledCheckReport(maximum)
			}
			if !used[message.key] {
				if collector.retains(SeverityWarning) {
					collector.add(Finding{
						Status: CheckUnused, Severity: SeverityWarning,
						Path: "messages." + string(message.key), Key: message.key,
						Detail: "message is absent from the complete usage manifest",
					})
				} else {
					collector.addCount(SeverityWarning, 1)
				}
			}
		}
	}
	coverage := buildCoverage(canonical, messages, policy, invalidPathIndex, &collector)
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	digest, err := checkSourceDigestContext(ctx, canonical, limits)
	if err != nil {
		collector.add(Finding{
			Status: CheckInvalid, Severity: SeverityError, Path: "source_digest",
			Detail: "canonical source identity could not be computed: " + err.Error(),
		})
	}
	if collector.canceled() {
		return canceledCheckReport(maximum)
	}
	return collector.report(coverage, digest)
}

func canceledCheckReport(maximum int) Report {
	collector := checkCollector{maximum: maximum}
	collector.add(Finding{
		Status: CheckInvalid, Severity: SeverityError, Path: "check",
		Detail: "check was canceled",
	})
	return collector.report(nil, "")
}

func checkOverlayWasValidated(paths []string) bool {
	for _, path := range paths {
		if strings.HasPrefix(path, "overlay.") {
			return true
		}
	}
	return false
}

func (c *checkCollector) report(coverage []LocaleCoverage, digest string) Report {
	slices.SortFunc(c.findings, compareFinding)
	var firstStructural *Finding
	if c.hasStructural {
		first := c.firstStructural
		firstStructural = &first
	}
	return Report{
		SourceDigest: digest, Findings: slices.Clone(c.findings), Coverage: slices.Clone(coverage),
		Errors: c.errors, Warnings: c.warnings, StructuralErrors: c.structuralErrors, Truncated: c.truncated,
		FirstStructural: firstStructural,
	}
}

func compareFinding(a, b Finding) int {
	if value := strings.Compare(a.Path, b.Path); value != 0 {
		return value
	}
	if value := strings.Compare(string(a.Status), string(b.Status)); value != 0 {
		return value
	}
	if value := strings.Compare(string(a.Key), string(b.Key)); value != 0 {
		return value
	}
	if value := strings.Compare(a.Locale, b.Locale); value != 0 {
		return value
	}
	if value := strings.Compare(string(a.Severity), string(b.Severity)); value != 0 {
		return value
	}
	return strings.Compare(a.Detail, b.Detail)
}

func addCheckError(collector *checkCollector, err error) []string {
	paths := make([]string, 0)
	var problems *Problems
	if errors.As(err, &problems) {
		for _, problem := range problems.Items() {
			paths = append(paths, problem.Path)
			collector.add(Finding{
				Status: CheckInvalid, Severity: SeverityError, Path: strings.TrimPrefix(problem.Path, "overlay."),
				Detail: string(problem.Code) + checkProblemDetail(problem.Detail),
			})
		}
		return paths
	}
	collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "catalog", Detail: err.Error()})
	return []string{"catalog"}
}

func checkProblemDetail(detail string) string {
	if detail == "" {
		return ""
	}
	return ": " + detail
}

func collectCheckedMessages(spec CatalogSpec, collector *checkCollector) []checkedMessage {
	messages := make([]checkedMessage, 0)
	seen := make(map[Key]string)
	for moduleIndex, module := range spec.Modules {
		for messageIndex, message := range module.Messages {
			if collector.pollCancellation() {
				return messages
			}
			key := message.Key
			derived := Qualify(module.Name, message.ID)
			if key == "" {
				key = derived
			}
			path := fmt.Sprintf("modules[%d].messages[%d]", moduleIndex, messageIndex)
			entry := checkedMessage{
				moduleIndex: moduleIndex, messageIndex: messageIndex, module: module.Name,
				key: key, message: message,
			}
			if key != derived || !validKey(key, spec.Limits.MaxIdentifierBytes) {
				entry.invalid = true
				collector.add(Finding{
					Status: CheckInvalid, Severity: SeverityError, Path: path + ".key", Key: key,
					Detail: "message key must be derived from module and id",
				})
			}
			if previous, exists := seen[key]; exists {
				entry.invalid = true
				collector.add(Finding{
					Status: CheckInvalid, Severity: SeverityError, Path: path + ".key", Key: key,
					Detail: "message key duplicates " + previous,
				})
			} else {
				seen[key] = path
			}
			digestMessage := message
			digestMessage.Key = key
			expected, err := ExpectedSourceDigestForLocale(spec.Profile, spec.SourceLocale, module.Name, digestMessage)
			if err != nil {
				entry.invalid = true
				collector.add(Finding{
					Status: CheckInvalid, Severity: SeverityError, Path: path, Key: key,
					Detail: err.Error(),
				})
			} else {
				entry.expected = expected
			}
			messages = append(messages, entry)
		}
	}
	slices.SortFunc(messages, func(a, b checkedMessage) int {
		if value := strings.Compare(string(a.key), string(b.key)); value != 0 {
			return value
		}
		if a.moduleIndex != b.moduleIndex {
			return a.moduleIndex - b.moduleIndex
		}
		return a.messageIndex - b.messageIndex
	})
	return messages
}

func normalizedCheckSpec(spec CatalogSpec, messages []checkedMessage, collector *checkCollector) CatalogSpec {
	normalized := cloneCatalogSpec(spec)
	if collector.canceled() {
		return normalized
	}
	normalized.Required = nil
	expected := make(map[Key]string, len(messages))
	for _, message := range messages {
		if collector.pollCancellation() {
			return normalized
		}
		expected[message.key] = message.expected
	}
	for moduleIndex := range normalized.Modules {
		module := &normalized.Modules[moduleIndex]
		for messageIndex := range module.Messages {
			if collector.pollCancellation() {
				return normalized
			}
			message := &module.Messages[messageIndex]
			key := message.Key
			if key == "" {
				key = Qualify(module.Name, message.ID)
			}
			for translationIndex := range message.Translations {
				if collector.pollCancellation() {
					return normalized
				}
				translation := &message.Translations[translationIndex]
				translation.Review = ReviewApproved
				translation.ContractRevision = message.Revision
				translation.SourceDigest = expected[key]
				translation.ReviewDigest, _ = ExpectedReviewDigest(expected[key], translation.Locale, translation.Text)
			}
		}
	}
	messageRevision := make(map[Key]string, len(messages))
	for _, message := range messages {
		if collector.pollCancellation() {
			return normalized
		}
		messageRevision[message.key] = message.message.Revision
	}
	for index := range normalized.Overrides {
		if collector.pollCancellation() {
			return normalized
		}
		override := &normalized.Overrides[index]
		override.Review = ReviewApproved
		override.ContractRevision = messageRevision[override.Key]
		override.SourceDigest = expected[override.Key]
		override.ReviewDigest, _ = ExpectedReviewDigest(expected[override.Key], override.Locale, override.Text)
	}
	return normalized
}

func markStructurallyInvalid(messages []checkedMessage, paths []string, collector *checkCollector) {
	messageIndexes := make(map[string]int, len(messages))
	for messageIndex := range messages {
		if collector.pollCancellation() {
			return
		}
		message := &messages[messageIndex]
		prefix := fmt.Sprintf("modules[%d].messages[%d]", message.moduleIndex, message.messageIndex)
		messageIndexes[prefix] = messageIndex
	}
	for _, path := range paths {
		if collector.pollCancellation() {
			return
		}
		prefix, ok := checkMessagePathPrefix(path)
		if !ok {
			continue
		}
		messageIndex, exists := messageIndexes[prefix]
		if !exists {
			continue
		}
		relative := strings.TrimPrefix(path, prefix)
		if strings.HasPrefix(relative, ".source") || strings.HasPrefix(relative, ".translations[") {
			continue
		}
		messages[messageIndex].invalid = true
	}
}

func checkMessagePathPrefix(path string) (string, bool) {
	const messageMarker = "].messages["
	if !strings.HasPrefix(path, "modules[") {
		return "", false
	}
	marker := strings.Index(path, messageMarker)
	if marker < 0 {
		return "", false
	}
	messageStart := marker + len(messageMarker)
	messageEnd := strings.IndexByte(path[messageStart:], ']')
	if messageEnd < 0 {
		return "", false
	}
	return path[:messageStart+messageEnd+1], true
}

func validateIsolatedOverrides(spec CatalogSpec, messages []checkedMessage, invalidPaths checkInvalidPathIndex, collector *checkCollector) {
	byKey := make(map[Key]checkedMessage, len(messages))
	for _, message := range messages {
		if _, exists := byKey[message.key]; !exists {
			byKey[message.key] = message
		}
	}
	allowedLocales := canonicalCheckLocaleSet(spec.Supported, spec.Limits.Locale.MaxTagBytes)
	if locale := canonicalCheckLocale(spec.SourceLocale, spec.Limits.Locale.MaxTagBytes); locale != "" {
		allowedLocales[locale] = true
	}
	if locale := canonicalCheckLocale(spec.DefaultLocale, spec.Limits.Locale.MaxTagBytes); locale != "" {
		allowedLocales[locale] = true
	}
	for _, edge := range spec.Parents {
		if locale := canonicalCheckLocale(edge.Locale, spec.Limits.Locale.MaxTagBytes); locale != "" {
			allowedLocales[locale] = true
		}
		if locale := canonicalCheckLocale(edge.Parent, spec.Limits.Locale.MaxTagBytes); locale != "" {
			allowedLocales[locale] = true
		}
	}
	capabilities := make(map[Capability]bool, len(spec.Capabilities))
	for _, capability := range spec.Capabilities {
		capabilities[capability] = true
	}
	seen := make(map[string]bool, len(spec.Overrides))
	for index, override := range spec.Overrides {
		if collector.pollCancellation() {
			return
		}
		path := fmt.Sprintf("overrides[%d]", index)
		internalPath := "overlay." + path
		if invalidPaths[internalPath] {
			continue
		}
		message, exists := byKey[override.Key]
		locale := canonicalCheckLocale(override.Locale, spec.Limits.Locale.MaxTagBytes)
		invalid := false
		if locale == "" {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path + ".locale", Key: override.Key, Detail: "override locale is invalid"})
			invalid = true
		} else if !allowedLocales[locale] {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path + ".locale", Key: override.Key, Locale: locale, Detail: "locale is outside the declared graph"})
			invalid = true
		}
		identity := string(override.Key) + "\x00" + locale
		if seen[identity] {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Key: override.Key, Locale: locale, Detail: "override is repeated"})
			invalid = true
		}
		seen[identity] = true
		if !exists || message.invalid || message.expected == "" || locale == "" {
			if invalid {
				invalidPaths.add(internalPath)
			}
			continue
		}
		problems := &problemSet{}
		arguments := validateArgumentSpecs(message.message.Arguments, capabilities, spec.Limits, path+".arguments", problems)
		markup := validateMarkupAllowlist(message.message.Markup, message.message.Output, spec.Limits.MaxIdentifierBytes, spec.Limits.MaxMarkupNames, path+".markup", problems)
		descriptor := Descriptor{
			Key: message.key, Revision: message.message.Revision, Description: message.message.Description,
			Arguments: arguments, Output: message.message.Output, Markup: markup,
			Override: message.message.Override, AllowEmpty: message.message.AllowEmpty, Public: message.message.Public,
		}
		if override.Text == "" && !message.message.AllowEmpty {
			problems.add(ProblemMissing, path+".text", "empty override is not allowed")
		} else if !utf8.ValidString(override.Text) {
			problems.add(ProblemInvalid, path+".text", "override is not valid UTF-8")
		} else if hasUnsafeAuthoredBidiControls(override.Text) {
			problems.add(ProblemSecurity, path+".text", "template contains directional controls")
		} else {
			compileTranslation(locale, LayerApplication, override.Text, descriptor, capabilities, spec.Limits, path+".text", problems)
		}
		if err := problems.err(); err != nil {
			var values *Problems
			if errors.As(err, &values) {
				for _, problem := range values.Items() {
					collector.add(Finding{
						Status: CheckInvalid, Severity: SeverityError, Path: problem.Path,
						Key: override.Key, Locale: locale, Detail: string(problem.Code) + checkProblemDetail(problem.Detail),
					})
				}
			}
			invalid = true
		}
		if invalid {
			invalidPaths.add(internalPath)
		}
	}
}

func classifyTranslationFindings(spec CatalogSpec, messages []checkedMessage, policy CheckPolicy, collector *checkCollector) {
	required := canonicalCheckLocaleSet(spec.Required, spec.Limits.Locale.MaxTagBytes)
	for _, message := range messages {
		for translationIndex, translation := range message.message.Translations {
			if collector.pollCancellation() {
				return
			}
			status, detail := checkReviewStatus(
				translation.Review,
				translation.ContractRevision,
				message.message.Revision,
				translation.SourceDigest,
				message.expected,
				translation.Locale,
				translation.Text,
				translation.ReviewDigest,
			)
			if status == "" {
				continue
			}
			locale := canonicalCheckLocale(translation.Locale, spec.Limits.Locale.MaxTagBytes)
			severity := checkLocaleSeverity(required[locale], policy.StrictOptional)
			if status == CheckInvalid {
				severity = SeverityError
			}
			collector.add(Finding{
				Status: status, Severity: severity,
				Path: fmt.Sprintf("modules[%d].messages[%d].translations[%d]", message.moduleIndex, message.messageIndex, translationIndex),
				Key:  message.key, Locale: locale, Detail: detail,
			})
		}
	}
}

func classifyOverrideFindings(spec CatalogSpec, messages []checkedMessage, policy CheckPolicy, invalidPaths checkInvalidPathIndex, collector *checkCollector) {
	required := canonicalCheckLocaleSet(spec.Required, spec.Limits.Locale.MaxTagBytes)
	byKey := make(map[Key]checkedMessage, len(messages))
	for _, message := range messages {
		if _, exists := byKey[message.key]; !exists {
			byKey[message.key] = message
		}
	}
	for index, override := range spec.Overrides {
		if collector.pollCancellation() {
			return
		}
		path := fmt.Sprintf("overrides[%d]", index)
		message, exists := byKey[override.Key]
		locale := canonicalCheckLocale(override.Locale, spec.Limits.Locale.MaxTagBytes)
		if !exists {
			collector.add(Finding{
				Status: CheckInvalid, Severity: SeverityError, Path: path + ".key",
				Key: override.Key, Locale: locale, Detail: "override key is not declared",
			})
			continue
		}
		if message.message.Override&OverrideApplication == 0 {
			collector.add(Finding{
				Status: CheckInvalid, Severity: SeverityError, Path: path + ".key",
				Key: override.Key, Locale: locale, Detail: "message denies application overrides",
			})
		}
		status, detail := checkReviewStatus(
			override.Review,
			override.ContractRevision,
			message.message.Revision,
			override.SourceDigest,
			message.expected,
			override.Locale,
			override.Text,
			override.ReviewDigest,
		)
		if invalidPaths["overlay."+path] {
			status = CheckInvalid
			detail = "override is structurally invalid"
		}
		if status == "" {
			continue
		}
		severity := checkLocaleSeverity(required[locale], policy.StrictOptional)
		if status == CheckInvalid {
			severity = SeverityError
		}
		collector.add(Finding{Status: status, Severity: severity, Path: path, Key: override.Key, Locale: locale, Detail: detail})
	}
}

func checkReviewStatus(
	review ReviewState,
	actualRevision, expectedRevision, actualSourceDigest, expectedSourceDigest string,
	locale, text, actualReviewDigest string,
) (CheckStatus, string) {
	switch review {
	case ReviewRejected:
		return CheckRejected, "translation was rejected"
	case ReviewRequired, ReviewUnset:
		return CheckReviewRequired, "translation requires review"
	case ReviewApproved:
	default:
		return CheckInvalid, "translation has an unknown review state"
	}
	if actualRevision != expectedRevision {
		return CheckStale, "contract revision does not match the message"
	}
	if expectedSourceDigest == "" || actualSourceDigest != expectedSourceDigest {
		return CheckStale, "source digest does not match the message"
	}
	expectedReviewDigest, err := ExpectedReviewDigest(expectedSourceDigest, locale, text)
	if err != nil {
		return CheckInvalid, "translation review identity cannot be computed"
	}
	if actualReviewDigest != expectedReviewDigest {
		return CheckStale, "review digest does not match the translation content"
	}
	return "", ""
}

func checkUsage(messages []checkedMessage, usage UsageManifest, limits UsageLimits, collector *checkCollector) (map[Key]bool, bool) {
	if len(usage.Keys) > limits.MaxKeys || len(usage.Dynamic) > limits.MaxDynamic || len(usage.Occurrences) > limits.MaxOccurrences ||
		usage.GoScope != nil && (len(usage.GoScope.Roots) > limits.MaxRoots || len(usage.GoScope.Files) > limits.MaxFiles ||
			len(usage.GoScope.Metadata) > limits.MaxMetadata || len(usage.GoScope.BuildTags) > limits.MaxTags ||
			len(usage.GoScope.ToolTags) > limits.MaxTags || len(usage.GoScope.ReleaseTags) > limits.MaxTags ||
			len(usage.GoScope.Environment) > limits.MaxEnvironment) {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage", Detail: "usage manifest exceeds configured item bounds"})
		return map[Key]bool{}, false
	}
	valid := true
	if usage.Complete && usage.GoScope == nil {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope", Detail: "complete usage requires scope provenance"})
		valid = false
	}
	used := make(map[Key]bool, len(usage.Keys))
	declared := make(map[Key]bool, len(messages))
	domains := make(map[string]bool)
	for _, message := range messages {
		declared[message.key] = true
		domains[message.module] = true
	}
	materialBytes := 0
	v4Scope := usage.GoScope != nil && usage.GoScope.Analyzer == GoUsageAnalyzerV2
	legacyScope := usage.GoScope != nil && usage.GoScope.Analyzer == GoUsageAnalyzerV1
	if usage.GoScope != nil && !v4Scope && !legacyScope {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.analyzer", Detail: "Go usage analyzer is unsupported"})
		valid = false
	}
	if v4Scope {
		if !slices.IsSorted(usage.Keys) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.keys", Detail: "usage keys must be sorted"})
			valid = false
		}
		if !slices.IsSortedFunc(usage.Dynamic, compareDynamicUsage) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.dynamic", Detail: "dynamic usage must be sorted"})
			valid = false
		}
		if !slices.IsSortedFunc(usage.Occurrences, compareCoreUsageOccurrence) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.occurrences", Detail: "usage occurrences must be sorted"})
			valid = false
		}
	}
	seenKeys := make(map[Key]bool, len(usage.Keys))
	for index, key := range usage.Keys {
		if collector.pollCancellation() {
			return used, false
		}
		path := fmt.Sprintf("usage.keys[%d]", index)
		if len(key) > limits.MaxStringBytes || len(key) > limits.MaxMaterialBytes-materialBytes {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage", Detail: "usage manifest exceeds configured byte bounds"})
			return used, false
		}
		materialBytes += len(key)
		if !validKey(key, min(limits.MaxStringBytes, maximumIdentifierBytes)) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Key: key, Detail: "usage key is invalid"})
			valid = false
			continue
		}
		if seenKeys[key] {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Key: key, Detail: "usage key is repeated"})
			valid = false
			continue
		}
		seenKeys[key] = true
		if !declared[key] {
			collector.add(Finding{Status: CheckMissing, Severity: SeverityError, Path: path, Key: key, Detail: "used key is not declared"})
			continue
		}
		used[key] = true
	}
	type dynamicRecord struct {
		usage   DynamicUsage
		path    string
		matched bool
		valid   bool
	}
	records := make([]dynamicRecord, len(usage.Dynamic))
	byDomain := make(map[string]map[string][]int)
	seenDynamic := make(map[string]bool, len(usage.Dynamic))
	for index, dynamic := range usage.Dynamic {
		if collector.pollCancellation() {
			return used, false
		}
		path := fmt.Sprintf("usage.dynamic[%d]", index)
		records[index] = dynamicRecord{usage: dynamic, path: path}
		if len(dynamic.Domain) > limits.MaxStringBytes || len(dynamic.Prefix) > limits.MaxStringBytes ||
			len(dynamic.Domain) > limits.MaxMaterialBytes-materialBytes || len(dynamic.Prefix) > limits.MaxMaterialBytes-materialBytes-len(dynamic.Domain) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage", Detail: "usage manifest exceeds configured byte bounds"})
			return used, false
		}
		materialBytes += len(dynamic.Domain) + len(dynamic.Prefix)
		identity := dynamic.Domain + "\x00" + dynamic.Prefix
		if seenDynamic[identity] {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "dynamic usage is repeated"})
			valid = false
			continue
		}
		seenDynamic[identity] = true
		if !validModuleName(dynamic.Domain, min(limits.MaxStringBytes, maximumIdentifierBytes)) || !validDynamicPrefix(dynamic.Prefix, min(limits.MaxStringBytes, maximumIdentifierBytes)) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "dynamic domain or prefix is invalid"})
			valid = false
			continue
		}
		if !domains[dynamic.Domain] {
			collector.add(Finding{Status: CheckMissing, Severity: SeverityError, Path: path, Detail: "dynamic domain is not declared"})
			continue
		}
		records[index].valid = true
		prefixes := byDomain[dynamic.Domain]
		if prefixes == nil {
			prefixes = make(map[string][]int)
			byDomain[dynamic.Domain] = prefixes
		}
		prefixes[dynamic.Prefix] = append(prefixes[dynamic.Prefix], index)
	}
	for _, message := range messages {
		if collector.pollCancellation() {
			return used, false
		}
		prefixes := byDomain[message.module]
		if len(prefixes) == 0 {
			continue
		}
		id := strings.TrimPrefix(string(message.key), message.module+".")
		for length := 0; length <= len(id); length++ {
			indices := prefixes[id[:length]]
			if len(indices) == 0 {
				continue
			}
			used[message.key] = true
			for _, index := range indices {
				records[index].matched = true
			}
		}
	}
	for _, record := range records {
		if collector.pollCancellation() {
			return used, false
		}
		if record.valid && record.usage.Prefix != "" && !record.matched {
			collector.add(Finding{Status: CheckMissing, Severity: SeverityError, Path: record.path, Detail: "dynamic prefix matches no declared message"})
		}
	}
	seenOccurrences := make(map[UsageOccurrence]struct{}, len(usage.Occurrences))
	seenCoordinates := make(map[string]struct{}, len(usage.Occurrences))
	occurrenceKeys := make(map[Key]bool, len(usage.Keys))
	occurrenceDynamic := make(map[string]bool, len(usage.Dynamic))
	for index, occurrence := range usage.Occurrences {
		if collector.pollCancellation() {
			return used, false
		}
		path := fmt.Sprintf("usage.occurrences[%d]", index)
		if len(occurrence.Key) > limits.MaxStringBytes || len(occurrence.Domain) > limits.MaxStringBytes ||
			len(occurrence.Prefix) > limits.MaxStringBytes || len(occurrence.Path) > limits.MaxStringBytes ||
			len(occurrence.Key) > limits.MaxMaterialBytes-materialBytes ||
			len(occurrence.Domain) > limits.MaxMaterialBytes-materialBytes-len(occurrence.Key) ||
			len(occurrence.Prefix) > limits.MaxMaterialBytes-materialBytes-len(occurrence.Key)-len(occurrence.Domain) ||
			len(occurrence.Path) > limits.MaxMaterialBytes-materialBytes-len(occurrence.Key)-len(occurrence.Domain)-len(occurrence.Prefix) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage", Detail: "usage manifest exceeds configured byte bounds"})
			return used, false
		}
		materialBytes += len(occurrence.Key) + len(occurrence.Domain) + len(occurrence.Prefix) + len(occurrence.Path)
		exact := occurrence.Key != ""
		dynamic := occurrence.Domain != ""
		if exact == dynamic || (!dynamic && occurrence.Prefix != "") {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "usage occurrence must identify exactly one static key or dynamic domain"})
			valid = false
		} else if exact {
			if !validKey(occurrence.Key, min(limits.MaxStringBytes, maximumIdentifierBytes)) {
				collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Key: occurrence.Key, Detail: "usage occurrence key is invalid"})
				valid = false
			} else if !seenKeys[occurrence.Key] {
				collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Key: occurrence.Key, Detail: "usage occurrence key is absent from usage keys"})
				valid = false
			}
			occurrenceKeys[occurrence.Key] = true
		} else {
			identity := occurrence.Domain + "\x00" + occurrence.Prefix
			if !validModuleName(occurrence.Domain, min(limits.MaxStringBytes, maximumIdentifierBytes)) || !validDynamicPrefix(occurrence.Prefix, min(limits.MaxStringBytes, maximumIdentifierBytes)) {
				collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "usage occurrence dynamic domain or prefix is invalid"})
				valid = false
			} else if !seenDynamic[identity] {
				collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "usage occurrence is absent from dynamic usage"})
				valid = false
			}
			occurrenceDynamic[identity] = true
		}
		locationValid := validUsageSourcePath(occurrence.Path, limits.MaxStringBytes) &&
			occurrence.Line > 0 && occurrence.Line <= maximumUsageCoordinate &&
			occurrence.Column > 0 && occurrence.Column <= maximumUsageCoordinate
		if !locationValid {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "usage occurrence source location is invalid"})
			valid = false
			continue
		}
		if _, exists := seenOccurrences[occurrence]; exists {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "usage occurrence is repeated"})
			valid = false
			continue
		}
		seenOccurrences[occurrence] = struct{}{}
		coordinate := occurrence.Path + "\x00" + strconv.Itoa(occurrence.Line) + "\x00" + strconv.Itoa(occurrence.Column)
		if _, exists := seenCoordinates[coordinate]; exists {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "usage occurrence source coordinate is repeated"})
			valid = false
		}
		seenCoordinates[coordinate] = struct{}{}
	}
	if v4Scope {
		for index, key := range usage.Keys {
			if !occurrenceKeys[key] {
				collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: fmt.Sprintf("usage.keys[%d]", index), Key: key, Detail: "scoped usage key has no source occurrence"})
				valid = false
			}
		}
		for index, dynamic := range usage.Dynamic {
			if !occurrenceDynamic[dynamic.Domain+"\x00"+dynamic.Prefix] {
				collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: fmt.Sprintf("usage.dynamic[%d]", index), Detail: "scoped dynamic usage has no source occurrence"})
				valid = false
			}
		}
		if !checkGoUsageScope(usage.GoScope, usage.Complete, limits, collector, &materialBytes) {
			valid = false
		}
		if usage.GoScope.Analyzer == GoUsageAnalyzerV2 {
			if len(usage.ManifestDigest) > limits.MaxStringBytes || len(usage.ManifestDigest) > limits.MaxMaterialBytes-materialBytes {
				collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage", Detail: "usage manifest exceeds configured byte bounds"})
				return used, false
			}
			materialBytes += len(usage.ManifestDigest)
			selected := make(map[string]bool, usage.GoScope.SelectedFiles)
			for _, file := range usage.GoScope.Files {
				if file.Selected {
					selected[file.LogicalPath] = true
				}
			}
			for index, occurrence := range usage.Occurrences {
				if !selected[occurrence.Path] {
					collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: fmt.Sprintf("usage.occurrences[%d].path", index), Detail: "usage occurrence is not backed by a selected source file"})
					valid = false
				}
			}
			expected, err := ExpectedUsageManifestDigestContext(collector.ctx, usage)
			if err != nil {
				collector.stopped = true
				return used, false
			}
			if !validUsageSHA256(usage.ManifestDigest) || usage.ManifestDigest != expected {
				collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.manifest_digest", Detail: "usage manifest digest does not match its canonical content"})
				valid = false
			}
		}
	}
	return used, valid
}

func checkGoUsageScope(scope *GoUsageScope, complete bool, limits UsageLimits, collector *checkCollector, materialBytes *int) bool {
	valid := true
	addMaterial := func(path, value string, maximum int) bool {
		if len(value) > maximum || maximum < 0 || len(value) > limits.MaxMaterialBytes-*materialBytes || !utf8.ValidString(value) || hasUsageControl(value) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage scope value is invalid or exceeds configured bounds"})
			valid = false
			return false
		}
		*materialBytes += len(value)
		return true
	}
	for _, value := range []struct {
		path  string
		value string
	}{
		{path: "usage.go_scope.goos", value: scope.GOOS},
		{path: "usage.go_scope.goarch", value: scope.GOARCH},
		{path: "usage.go_scope.compiler", value: scope.Compiler},
	} {
		if addMaterial(value.path, value.value, min(limits.MaxStringBytes, maximumIdentifierBytes)) && !validUsageScopeAtom(value.value) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: value.path, Detail: "Go usage scope value is invalid"})
			valid = false
		}
	}
	if !addMaterial("usage.go_scope.analyzer", scope.Analyzer, min(limits.MaxStringBytes, maximumIdentifierBytes)) || scope.Analyzer != GoUsageAnalyzerV2 {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.analyzer", Detail: "Go usage analyzer is unsupported"})
		valid = false
	}
	for _, value := range []struct {
		path  string
		value string
	}{
		{path: "usage.go_scope.go_version", value: scope.GoVersion},
		{path: "usage.go_scope.toolchain", value: scope.Toolchain},
		{path: "usage.go_scope.go_experiment", value: scope.GoExperiment},
		{path: "usage.go_scope.go_flags", value: scope.GoFlags},
		{path: "usage.go_scope.go_work", value: scope.GoWork},
		{path: "usage.go_scope.go_env", value: scope.GoEnv},
	} {
		if !addMaterial(value.path, value.value, limits.MaxStringBytes) {
			valid = false
		}
	}
	if !validGoUsagePlatform(scope.GOOS, scope.GOARCH) || (scope.Compiler != "gc" && scope.Compiler != "gccgo") {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope", Detail: "Go usage target or compiler is unsupported"})
		valid = false
	}
	if scope.GoVersion == "" || scope.Toolchain == "" || (scope.GoWork != "off" && scope.GoWork != "active" && scope.GoWork != "mixed") ||
		(scope.GoEnv != "off" && scope.GoEnv != "active") {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope", Detail: "Go usage environment provenance is incomplete"})
		valid = false
	}
	for _, values := range []struct {
		path   string
		values []string
	}{
		{path: "usage.go_scope.build_tags", values: scope.BuildTags},
		{path: "usage.go_scope.tool_tags", values: scope.ToolTags},
		{path: "usage.go_scope.release_tags", values: scope.ReleaseTags},
	} {
		if !slices.IsSorted(values.values) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: values.path, Detail: "Go usage scope values must be sorted"})
			valid = false
		}
		for index, value := range values.values {
			path := fmt.Sprintf("%s[%d]", values.path, index)
			if addMaterial(path, value, min(limits.MaxStringBytes, maximumIdentifierBytes)) && !validUsageScopeAtom(value) {
				collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage scope value is invalid"})
				valid = false
			}
			if index != 0 && value == values.values[index-1] {
				collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage scope value is repeated"})
				valid = false
			}
		}
	}
	if !slices.IsSortedFunc(scope.Environment, compareUsageSetting) {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.environment", Detail: "Go usage environment must be sorted"})
		valid = false
	}
	for index, setting := range scope.Environment {
		if collector.pollCancellation() {
			return false
		}
		path := fmt.Sprintf("usage.go_scope.environment[%d]", index)
		if !validUsageScopeAtom(setting.Name) || !addMaterial(path+".name", setting.Name, min(limits.MaxStringBytes, maximumIdentifierBytes)) || !addMaterial(path+".value", setting.Value, limits.MaxStringBytes) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage environment setting is invalid"})
			valid = false
		}
		if index != 0 && setting.Name == scope.Environment[index-1].Name {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage environment setting is repeated"})
			valid = false
		}
	}
	if len(scope.Environment) == 0 {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.environment", Detail: "Go usage environment ledger is empty"})
		valid = false
	}
	settings := make(map[string]string, len(scope.Environment))
	for _, setting := range scope.Environment {
		settings[setting.Name] = setting.Value
	}
	if settings["GOOS"] != scope.GOOS || settings["GOARCH"] != scope.GOARCH || settings["GOVERSION"] != scope.GoVersion ||
		settings["GOEXPERIMENT"] != scope.GoExperiment || settings["GOFLAGS"] != scope.GoFlags || settings["GOWORK"] != scope.GoWork ||
		settings["GOENV"] != scope.GoEnv || settings["CGO_ENABLED"] != strconv.FormatBool(scope.CgoEnabled) {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.environment", Detail: "Go usage environment contradicts its indexed fields"})
		valid = false
	}
	if !slices.IsSortedFunc(scope.Roots, func(left, right UsageRoot) int {
		if value := strings.Compare(left.Path, right.Path); value != 0 {
			return value
		}
		return strings.Compare(left.Kind.String(), right.Kind.String())
	}) {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.roots", Detail: "Go usage roots must be sorted"})
		valid = false
	}
	for index, root := range scope.Roots {
		path := fmt.Sprintf("usage.go_scope.roots[%d]", index)
		if !root.Kind.Valid() || !validUsageRootPath(root.Path, limits.MaxStringBytes) || !addMaterial(path+".path", root.Path, limits.MaxStringBytes) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage root is invalid"})
			valid = false
		}
		if index != 0 && root == scope.Roots[index-1] {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage root is repeated"})
			valid = false
		}
		if complete && root.Kind != UsageRootDirectory {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path + ".kind", Detail: "complete usage requires directory roots"})
			valid = false
		}
	}
	if len(scope.Roots) == 0 {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.roots", Detail: "Go usage scope has no roots"})
		valid = false
	}
	rootKinds := make(map[string]UsageRootKind, len(scope.Roots))
	for _, root := range scope.Roots {
		rootKinds[root.Path] = root.Kind
	}
	if !slices.IsSortedFunc(scope.Files, compareUsageFile) {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.files", Detail: "Go usage files must be sorted"})
		valid = false
	}
	selectedFiles := 0
	excludedFiles := 0
	for index, file := range scope.Files {
		if collector.pollCancellation() {
			return false
		}
		path := fmt.Sprintf("usage.go_scope.files[%d]", index)
		_, rootExists := rootKinds[file.Root]
		logical := file.Root + "/" + file.Path
		if !rootExists || !validUsageRootPath(file.Root, limits.MaxStringBytes) || !validUsageLogicalPath(file.Path, limits.MaxStringBytes) ||
			file.LogicalPath != logical || !validUsageSourcePath(file.LogicalPath, limits.MaxStringBytes) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage file is outside its declared root"})
			valid = false
		}
		if !addMaterial(path+".root", file.Root, limits.MaxStringBytes) || !addMaterial(path+".path", file.Path, limits.MaxStringBytes) ||
			!addMaterial(path+".logical_path", file.LogicalPath, limits.MaxStringBytes) || !validUsageSHA256(file.SHA256) ||
			!addMaterial(path+".sha256", file.SHA256, sha256.Size*2) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage file identity is invalid"})
			valid = false
		}
		if index != 0 && file.Root == scope.Files[index-1].Root && file.Path == scope.Files[index-1].Path {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage file identity is repeated"})
			valid = false
		}
		if file.Selected {
			selectedFiles++
		} else {
			excludedFiles++
		}
	}
	if !slices.IsSortedFunc(scope.Metadata, compareUsageMetadata) {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.metadata", Detail: "Go usage metadata must be sorted"})
		valid = false
	}
	for index, metadata := range scope.Metadata {
		if collector.pollCancellation() {
			return false
		}
		path := fmt.Sprintf("usage.go_scope.metadata[%d]", index)
		if !validUsageScopeAtom(metadata.Kind) || !validUsageLogicalPath(metadata.Path, limits.MaxStringBytes) || !validUsageSHA256(metadata.SHA256) ||
			!addMaterial(path+".kind", metadata.Kind, min(limits.MaxStringBytes, maximumIdentifierBytes)) || !addMaterial(path+".path", metadata.Path, limits.MaxStringBytes) ||
			!addMaterial(path+".sha256", metadata.SHA256, sha256.Size*2) {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage metadata identity is invalid"})
			valid = false
		}
		if index != 0 && metadata.Kind == scope.Metadata[index-1].Kind && metadata.Path == scope.Metadata[index-1].Path {
			collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: path, Detail: "Go usage metadata identity is repeated"})
			valid = false
		}
	}
	if len(scope.Metadata) == 0 {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.metadata", Detail: "Go usage metadata ledger is empty"})
		valid = false
	}
	expectedSourceDigest, err := ExpectedUsageSourceDigestContext(collector.ctx, *scope)
	if err != nil {
		collector.stopped = true
		return false
	}
	if !addMaterial("usage.go_scope.source_digest", scope.SourceDigest, sha256.Size*2) || !validUsageSHA256(scope.SourceDigest) || scope.SourceDigest != expectedSourceDigest {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.source_digest", Detail: "Go usage source digest does not match its canonical ledger"})
		valid = false
	}
	if scope.SelectedFiles < 0 || scope.ExcludedFiles < 0 || scope.SelectedFiles > limits.MaxFiles || scope.ExcludedFiles > limits.MaxFiles-scope.SelectedFiles {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope", Detail: "Go usage source counts are invalid"})
		valid = false
	}
	if scope.SelectedFiles != selectedFiles || scope.ExcludedFiles != excludedFiles {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope", Detail: "Go usage source counts do not match the file ledger"})
		valid = false
	}
	if complete && selectedFiles == 0 {
		collector.add(Finding{Status: CheckInvalid, Severity: SeverityError, Path: "usage.go_scope.selected_files", Detail: "complete usage has no selected Go sources"})
		valid = false
	}
	return valid
}

func compareDynamicUsage(left, right DynamicUsage) int {
	if value := strings.Compare(left.Domain, right.Domain); value != 0 {
		return value
	}
	return strings.Compare(left.Prefix, right.Prefix)
}

func compareCoreUsageOccurrence(left, right UsageOccurrence) int {
	if value := strings.Compare(left.Path, right.Path); value != 0 {
		return value
	}
	if left.Line != right.Line {
		return left.Line - right.Line
	}
	if left.Column != right.Column {
		return left.Column - right.Column
	}
	if value := strings.Compare(string(left.Key), string(right.Key)); value != 0 {
		return value
	}
	if value := strings.Compare(left.Domain, right.Domain); value != 0 {
		return value
	}
	return strings.Compare(left.Prefix, right.Prefix)
}

func compareUsageFile(left, right UsageFile) int {
	if value := strings.Compare(left.Root, right.Root); value != 0 {
		return value
	}
	return strings.Compare(left.Path, right.Path)
}

func compareUsageMetadata(left, right UsageMetadata) int {
	if value := strings.Compare(left.Kind, right.Kind); value != 0 {
		return value
	}
	return strings.Compare(left.Path, right.Path)
}

func compareUsageSetting(left, right UsageSetting) int {
	return strings.Compare(left.Name, right.Name)
}

func validUsageSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}

func hasUsageControl(value string) bool {
	if !utf8.ValidString(value) || hasUnsafeAuthoredBidiControls(value) {
		return true
	}
	for _, character := range value {
		if character < 0x20 && character != '\t' || character == 0x7f {
			return true
		}
	}
	return false
}

func validGoUsagePlatform(goos, goarch string) bool {
	validOS := map[string]bool{"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "illumos": true, "ios": true, "js": true, "linux": true, "netbsd": true, "openbsd": true, "plan9": true, "solaris": true, "wasip1": true, "windows": true}
	validArch := map[string]bool{"386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true, "mips": true, "mips64": true, "mips64le": true, "mipsle": true, "ppc64": true, "ppc64le": true, "riscv64": true, "s390x": true, "wasm": true}
	return validOS[goos] && validArch[goarch]
}

func validUsageScopeAtom(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character <= 0x20 || character == 0x7f || character == '/' || character == '\\' {
			return false
		}
	}
	return true
}

func validDynamicPrefix(value string, maximum int) bool {
	if len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	if value == "" {
		return true
	}
	trimmed := strings.TrimSuffix(value, ".")
	return trimmed != "" && validMessageID(trimmed, maximum)
}

func validUsageSourcePath(value string, maximum int) bool {
	return strings.HasSuffix(value, ".go") && validUsageLogicalPath(value, maximum)
}

func validUsageRootPath(value string, maximum int) bool {
	return validUsageLogicalPath(value, maximum)
}

func validUsageLogicalPath(value string, maximum int) bool {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || pathpkg.IsAbs(value) || strings.ContainsAny(value, "\\:") || hasUnsafeAuthoredBidiControls(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	cleaned := pathpkg.Clean(value)
	return cleaned == value && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func buildCoverage(spec CatalogSpec, messages []checkedMessage, policy CheckPolicy, invalidPaths checkInvalidPathIndex, collector *checkCollector) []LocaleCoverage {
	locales := make(map[string]bool)
	required := canonicalCheckLocaleSet(spec.Required, spec.Limits.Locale.MaxTagBytes)
	for _, locale := range spec.Supported {
		if collector.pollCancellation() {
			return nil
		}
		if canonical := canonicalCheckLocale(locale, spec.Limits.Locale.MaxTagBytes); canonical != "" {
			locales[canonical] = true
		}
	}
	if canonical := canonicalCheckLocale(spec.SourceLocale, spec.Limits.Locale.MaxTagBytes); canonical != "" {
		locales[canonical] = true
	}
	for locale := range required {
		locales[locale] = true
	}
	orderedLocales := make([]string, 0, len(locales))
	for locale := range locales {
		if collector.pollCancellation() {
			return nil
		}
		orderedLocales = append(orderedLocales, locale)
	}
	slices.Sort(orderedLocales)
	coverage := make([]LocaleCoverage, len(orderedLocales))
	coverageIndex := make(map[string]int, len(orderedLocales))
	for index, locale := range orderedLocales {
		if collector.pollCancellation() {
			return nil
		}
		coverage[index] = LocaleCoverage{
			Locale: locale, Required: required[locale], Total: len(messages), Missing: len(messages),
		}
		coverageIndex[locale] = index
	}

	candidateCapacity := len(messages) + len(spec.Overrides)
	for _, message := range messages {
		if collector.pollCancellation() {
			return nil
		}
		candidateCapacity += len(message.message.Translations)
	}
	candidates := make(map[checkMessageLocale]CheckStatus, candidateCapacity)
	sourceLocale := canonicalCheckLocale(spec.SourceLocale, spec.Limits.Locale.MaxTagBytes)
	for messageIndex, message := range messages {
		for translationIndex, translation := range message.message.Translations {
			if collector.pollCancellation() {
				return nil
			}
			locale := canonicalCheckLocale(translation.Locale, spec.Limits.Locale.MaxTagBytes)
			if _, exists := coverageIndex[locale]; !exists {
				continue
			}
			status, _ := checkReviewStatus(
				translation.Review,
				translation.ContractRevision,
				message.message.Revision,
				translation.SourceDigest,
				message.expected,
				translation.Locale,
				translation.Text,
				translation.ReviewDigest,
			)
			invalid := message.invalid
			if !invalid && len(invalidPaths) != 0 {
				path := fmt.Sprintf("modules[%d].messages[%d].translations[%d]", message.moduleIndex, message.messageIndex, translationIndex)
				invalid = invalidPaths[path]
			}
			if invalid {
				status = CheckInvalid
			}
			candidates[checkMessageLocale{message: messageIndex, locale: locale}] = status
		}
		if _, exists := coverageIndex[sourceLocale]; exists {
			status := CheckStatus("")
			invalid := message.invalid
			if !invalid && len(invalidPaths) != 0 {
				path := fmt.Sprintf("modules[%d].messages[%d].source", message.moduleIndex, message.messageIndex)
				invalid = invalidPaths[path]
			}
			if invalid {
				status = CheckInvalid
			}
			candidates[checkMessageLocale{message: messageIndex, locale: sourceLocale}] = status
		}
	}

	if len(spec.Overrides) != 0 {
		messagesByKey := make(map[Key][]int, len(messages))
		for index, message := range messages {
			if collector.pollCancellation() {
				return nil
			}
			messagesByKey[message.key] = append(messagesByKey[message.key], index)
		}
		for identity, indexed := range indexEffectiveCheckOverrides(spec, collector) {
			if collector.pollCancellation() {
				return nil
			}
			if _, exists := coverageIndex[identity.locale]; !exists {
				continue
			}
			for _, messageIndex := range messagesByKey[identity.key] {
				if collector.pollCancellation() {
					return nil
				}
				message := messages[messageIndex]
				override := indexed.override
				status, _ := checkReviewStatus(
					override.Review,
					override.ContractRevision,
					message.message.Revision,
					override.SourceDigest,
					message.expected,
					override.Locale,
					override.Text,
					override.ReviewDigest,
				)
				invalid := message.invalid || message.message.Override&OverrideApplication == 0
				if !invalid && len(invalidPaths) != 0 {
					path := fmt.Sprintf("overlay.overrides[%d]", indexed.index)
					invalid = invalidPaths[path]
				}
				if invalid {
					status = CheckInvalid
				}
				candidates[checkMessageLocale{message: messageIndex, locale: identity.locale}] = status
			}
		}
	}

	for identity, status := range candidates {
		if collector.pollCancellation() {
			return nil
		}
		entry := &coverage[coverageIndex[identity.locale]]
		entry.Missing--
		switch status {
		case "":
			entry.Approved++
		case CheckStale:
			entry.Stale++
		case CheckReviewRequired:
			entry.ReviewRequired++
		case CheckRejected:
			entry.Rejected++
		case CheckInvalid:
			entry.Invalid++
		}
	}

	for index := range coverage {
		if collector.pollCancellation() {
			return nil
		}
		entry := &coverage[index]
		remaining := entry.Missing
		severity := checkLocaleSeverity(entry.Required, policy.StrictOptional)
		for messageIndex, message := range messages {
			if collector.pollCancellation() {
				return nil
			}
			if remaining == 0 || !collector.retains(severity) {
				break
			}
			if _, exists := candidates[checkMessageLocale{message: messageIndex, locale: entry.Locale}]; exists {
				continue
			}
			collector.add(Finding{
				Status: CheckMissing, Severity: severity,
				Path: "messages." + string(message.key) + ".translations." + entry.Locale,
				Key:  message.key, Locale: entry.Locale, Detail: "locale has no translation",
			})
			remaining--
		}
		collector.addCount(severity, remaining)
	}
	return coverage
}

func indexEffectiveCheckOverrides(spec CatalogSpec, collector *checkCollector) map[checkOverrideLocale]indexedCheckOverride {
	effective := make(map[checkOverrideLocale]indexedCheckOverride, len(spec.Overrides))
	for index, override := range spec.Overrides {
		if collector.pollCancellation() {
			return effective
		}
		locale := canonicalCheckLocale(override.Locale, spec.Limits.Locale.MaxTagBytes)
		if locale == "" {
			continue
		}
		effective[checkOverrideLocale{key: override.Key, locale: locale}] = indexedCheckOverride{
			index: index, override: override,
		}
	}
	return effective
}

func checkLocaleSeverity(required, strictOptional bool) CheckSeverity {
	if required || strictOptional {
		return SeverityError
	}
	return SeverityWarning
}

func canonicalCheckLocaleSet(values []string, maximum int) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if canonical := canonicalCheckLocale(value, maximum); canonical != "" {
			out[canonical] = true
		}
	}
	return out
}

func canonicalCheckLocale(value string, maximum int) string {
	_, canonical, err := canonicalLocale(value, maximum)
	if err != nil {
		return ""
	}
	return canonical
}

func checkSourceDigest(spec CatalogSpec, limits Limits) (string, error) {
	return checkSourceDigestContext(context.Background(), spec, limits)
}

func checkSourceDigestContext(ctx context.Context, spec CatalogSpec, limits Limits) (string, error) {
	raw, err := (SourceCodec{
		Limits:        ArtifactLimits{MaxBytes: maximumArtifactBytes, MaxDepth: maximumArtifactDepth, MaxMembers: maximumArtifactMembers},
		CatalogLimits: limits,
	}).EncodeContext(ctx, spec)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for len(raw) != 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		chunk := min(len(raw), 32<<10)
		_, _ = hash.Write(raw[:chunk])
		raw = raw[chunk:]
	}
	return hex.EncodeToString(hash.Sum(nil)), ctx.Err()
}

func (c LocaleCoverage) Complete() bool {
	return c.Total > 0 && c.Approved == c.Total
}

func (r Report) Summary() string {
	return strconv.Itoa(r.Errors) + " errors, " + strconv.Itoa(r.Warnings) + " warnings"
}
