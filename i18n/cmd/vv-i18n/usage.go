package main

import (
	"bytes"
	"context"
	"encoding/hex"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	pathpkg "path"
	"slices"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/frostgrove/vv/i18n"
)

const (
	usageSchemaV1 = "frostgrove.i18n.usage/v1"
	usageSchemaV2 = "frostgrove.i18n.usage/v2"
	usageSchemaV3 = "frostgrove.i18n.usage/v3"
	usageSchema   = "frostgrove.i18n.usage/v4"

	maximumUsageJSONDepth    = 16
	maximumUsageJSONMembers  = 1 << 22
	maximumUsageJSONString   = 4 << 20
	maximumUsageJSONMaterial = 64 << 20
	maximumUsageCoordinate   = 1 << 30
)

var errCommandJSONTooLarge = errors.New("command JSON exceeds the output limit")

type usageDocument struct {
	Schema         string                 `json:"schema"`
	Keys           []i18n.Key             `json:"keys"`
	Dynamic        []i18n.DynamicUsage    `json:"dynamic"`
	Occurrences    []i18n.UsageOccurrence `json:"occurrences"`
	GoScope        *i18n.GoUsageScope     `json:"go_scope,omitempty"`
	ManifestDigest string                 `json:"manifest_digest"`
	Complete       bool                   `json:"complete"`
}

type usageDocumentV3 struct {
	Schema      string                 `json:"schema"`
	Keys        []i18n.Key             `json:"keys"`
	Dynamic     []i18n.DynamicUsage    `json:"dynamic"`
	Occurrences []i18n.UsageOccurrence `json:"occurrences"`
	GoScope     *i18n.GoUsageScope     `json:"go_scope,omitempty"`
	Complete    bool                   `json:"complete"`
}

type usageDocumentV2 struct {
	Schema      string                 `json:"schema"`
	Keys        []i18n.Key             `json:"keys"`
	Dynamic     []i18n.DynamicUsage    `json:"dynamic"`
	Occurrences []i18n.UsageOccurrence `json:"occurrences"`
	Complete    bool                   `json:"complete"`
}

type usageDocumentV1 struct {
	Schema   string              `json:"schema"`
	Keys     []i18n.Key          `json:"keys"`
	Dynamic  []i18n.DynamicUsage `json:"dynamic"`
	Complete bool                `json:"complete"`
}

func encodeUsage(usage i18n.UsageManifest) ([]byte, error) {
	return encodeUsageBoundedContext(context.Background(), usage, maximumCommandInput)
}

func encodeUsageBounded(usage i18n.UsageManifest, maximum int) ([]byte, error) {
	return encodeUsageBoundedContext(context.Background(), usage, maximum)
}

func encodeUsageBoundedContext(ctx context.Context, usage i18n.UsageManifest, maximum int) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("encode usage manifest: context is nil")
	}
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return nil, fmt.Errorf("encode usage manifest: output limit %d is outside supported bounds", maximum)
	}
	if err := preflightUsageOutputContext(ctx, usage, maximum); err != nil {
		return nil, err
	}
	limits := i18n.DefaultUsageLimits()
	if err := preflightUsageManifestCardinality(usage, limits); err != nil {
		return nil, err
	}
	keys := slices.Clone(usage.Keys)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.Sort(keys)
	dynamic := slices.Clone(usage.Dynamic)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(dynamic, func(left, right i18n.DynamicUsage) int {
		if value := strings.Compare(left.Domain, right.Domain); value != 0 {
			return value
		}
		return strings.Compare(left.Prefix, right.Prefix)
	})
	occurrences := slices.Clone(usage.Occurrences)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(occurrences, compareUsageOccurrence)
	scope := cloneGoUsageScope(usage.GoScope)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scope != nil {
		slices.Sort(scope.BuildTags)
		slices.Sort(scope.ToolTags)
		slices.Sort(scope.ReleaseTags)
		slices.SortFunc(scope.Environment, func(left, right i18n.UsageSetting) int { return strings.Compare(left.Name, right.Name) })
		slices.SortFunc(scope.Roots, func(left, right i18n.UsageRoot) int {
			if value := strings.Compare(left.Path, right.Path); value != 0 {
				return value
			}
			return strings.Compare(left.Kind.String(), right.Kind.String())
		})
		slices.SortFunc(scope.Files, compareUsageFileDocument)
		slices.SortFunc(scope.Metadata, compareUsageMetadataDocument)
	}
	canonical := i18n.UsageManifest{Keys: keys, Dynamic: dynamic, Occurrences: occurrences, GoScope: scope, Complete: usage.Complete}
	if canonical.GoScope != nil && canonical.GoScope.Analyzer == i18n.GoUsageAnalyzerV2 {
		canonical.GoScope.SourceDigest = i18n.ExpectedUsageSourceDigest(*canonical.GoScope)
	}
	if err := validateUsageDocumentScope(canonical.GoScope, usage.Complete, limits); err != nil {
		return nil, err
	}
	canonical.ManifestDigest = i18n.ExpectedUsageManifestDigest(canonical)
	if err := validateUsageDocument(canonical, limits); err != nil {
		return nil, err
	}
	document := usageDocument{
		Schema: usageSchema, Keys: keys, Dynamic: dynamic, Occurrences: occurrences,
		GoScope: canonical.GoScope, ManifestDigest: canonical.ManifestDigest, Complete: usage.Complete,
	}
	return encodeCommandJSONBoundedContext(ctx, "usage manifest", document, maximum)
}

func preflightUsageOutputContext(ctx context.Context, usage i18n.UsageManifest, maximum int) error {
	remaining := maximum - 64
	consume := func(size int) bool {
		if size < 0 || size > remaining {
			return false
		}
		remaining -= size
		return true
	}
	if remaining < 0 {
		return fmt.Errorf("encode usage manifest: %w", errCommandJSONTooLarge)
	}
	for _, key := range usage.Keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !consume(len(key) + 2) {
			return fmt.Errorf("encode usage manifest: %w", errCommandJSONTooLarge)
		}
	}
	for _, dynamic := range usage.Dynamic {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !consume(len(dynamic.Domain) + len(dynamic.Prefix) + 2) {
			return fmt.Errorf("encode usage manifest: %w", errCommandJSONTooLarge)
		}
	}
	for _, occurrence := range usage.Occurrences {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !consume(len(occurrence.Key) + len(occurrence.Domain) + len(occurrence.Prefix) + len(occurrence.Path) + 2) {
			return fmt.Errorf("encode usage manifest: %w", errCommandJSONTooLarge)
		}
	}
	if usage.GoScope == nil {
		return ctx.Err()
	}
	scope := usage.GoScope
	if !consume(len(scope.Analyzer) + len(scope.GOOS) + len(scope.GOARCH) + len(scope.Compiler) + len(scope.GoVersion) + len(scope.Toolchain) + len(scope.GoExperiment) + len(scope.GoFlags) + len(scope.GoWork) + len(scope.GoEnv) + len(scope.SourceDigest)) {
		return fmt.Errorf("encode usage manifest: %w", errCommandJSONTooLarge)
	}
	for _, count := range []int{len(scope.Environment), len(scope.BuildTags), len(scope.ToolTags), len(scope.ReleaseTags), len(scope.Roots), len(scope.Files), len(scope.Metadata)} {
		if !consume(count * 2) {
			return fmt.Errorf("encode usage manifest: %w", errCommandJSONTooLarge)
		}
	}
	return ctx.Err()
}

func decodeUsage(raw []byte) (i18n.UsageManifest, error) {
	return decodeUsageContext(context.Background(), raw)
}

func decodeUsageContext(ctx context.Context, raw []byte) (i18n.UsageManifest, error) {
	if ctx == nil {
		return i18n.UsageManifest{}, errors.New("decode usage manifest: context is nil")
	}
	limits := i18n.DefaultUsageLimits()
	if err := preflightUsageJSON(ctx, raw, usageJSONLimitsFrom(limits)); err != nil {
		return i18n.UsageManifest{}, fmt.Errorf("decode usage manifest: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return i18n.UsageManifest{}, err
	}
	var envelope struct {
		Schema string `json:"schema"`
	}
	if err := jsonv2.Unmarshal(raw, &envelope, jsontext.AllowDuplicateNames(false)); err != nil {
		return i18n.UsageManifest{}, fmt.Errorf("decode usage manifest: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return i18n.UsageManifest{}, err
	}
	switch envelope.Schema {
	case usageSchemaV1:
		var document usageDocumentV1
		if err := jsonv2.Unmarshal(raw, &document, jsonv2.RejectUnknownMembers(true), jsontext.AllowDuplicateNames(false)); err != nil {
			return i18n.UsageManifest{}, fmt.Errorf("decode usage manifest: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return i18n.UsageManifest{}, err
		}
		return i18n.UsageManifest{Keys: document.Keys, Dynamic: document.Dynamic}, nil
	case usageSchemaV2:
		var document usageDocumentV2
		if err := jsonv2.Unmarshal(raw, &document, jsonv2.RejectUnknownMembers(true), jsontext.AllowDuplicateNames(false)); err != nil {
			return i18n.UsageManifest{}, fmt.Errorf("decode usage manifest: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return i18n.UsageManifest{}, err
		}
		return i18n.UsageManifest{Keys: document.Keys, Dynamic: document.Dynamic, Occurrences: document.Occurrences}, nil
	case usageSchemaV3:
		var document usageDocumentV3
		if err := jsonv2.Unmarshal(raw, &document, jsonv2.RejectUnknownMembers(true), jsontext.AllowDuplicateNames(false)); err != nil {
			return i18n.UsageManifest{}, fmt.Errorf("decode usage manifest: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return i18n.UsageManifest{}, err
		}
		return i18n.UsageManifest{Keys: document.Keys, Dynamic: document.Dynamic, Occurrences: document.Occurrences}, nil
	case usageSchema:
		var document usageDocument
		if err := jsonv2.Unmarshal(raw, &document, jsonv2.RejectUnknownMembers(true), jsontext.AllowDuplicateNames(false)); err != nil {
			return i18n.UsageManifest{}, fmt.Errorf("decode usage manifest: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return i18n.UsageManifest{}, err
		}
		if err := validateUsageDocumentScope(document.GoScope, document.Complete, limits); err != nil {
			return i18n.UsageManifest{}, err
		}
		manifest := i18n.UsageManifest{Keys: document.Keys, Dynamic: document.Dynamic, Occurrences: document.Occurrences,
			GoScope: document.GoScope, ManifestDigest: document.ManifestDigest, Complete: document.Complete}
		if err := validateUsageDocument(manifest, limits); err != nil {
			return i18n.UsageManifest{}, err
		}
		if manifest.ManifestDigest != i18n.ExpectedUsageManifestDigest(manifest) {
			return i18n.UsageManifest{}, errors.New("usage manifest digest does not match its canonical content")
		}
		return manifest, nil
	default:
		return i18n.UsageManifest{}, fmt.Errorf("unsupported usage schema %q", envelope.Schema)
	}
}

func cloneGoUsageScope(scope *i18n.GoUsageScope) *i18n.GoUsageScope {
	if scope == nil {
		return nil
	}
	cloned := *scope
	cloned.BuildTags = slices.Clone(scope.BuildTags)
	cloned.ToolTags = slices.Clone(scope.ToolTags)
	cloned.ReleaseTags = slices.Clone(scope.ReleaseTags)
	cloned.Environment = slices.Clone(scope.Environment)
	cloned.Roots = slices.Clone(scope.Roots)
	cloned.Files = slices.Clone(scope.Files)
	cloned.Metadata = slices.Clone(scope.Metadata)
	return &cloned
}

func compareUsageFileDocument(left, right i18n.UsageFile) int {
	if value := strings.Compare(left.Root, right.Root); value != 0 {
		return value
	}
	return strings.Compare(left.Path, right.Path)
}

func compareUsageMetadataDocument(left, right i18n.UsageMetadata) int {
	if value := strings.Compare(left.Kind, right.Kind); value != 0 {
		return value
	}
	return strings.Compare(left.Path, right.Path)
}

func preflightUsageManifestCardinality(manifest i18n.UsageManifest, limits i18n.UsageLimits) error {
	if len(manifest.Keys) > limits.MaxKeys || len(manifest.Dynamic) > limits.MaxDynamic || len(manifest.Occurrences) > limits.MaxOccurrences {
		return errors.New("usage manifest content exceeds configured bounds")
	}
	if manifest.GoScope == nil {
		return nil
	}
	scope := manifest.GoScope
	if len(scope.Roots) > limits.MaxRoots || len(scope.Files) > limits.MaxFiles || len(scope.Metadata) > limits.MaxMetadata ||
		len(scope.BuildTags) > limits.MaxTags || len(scope.ToolTags) > limits.MaxTags || len(scope.ReleaseTags) > limits.MaxTags ||
		len(scope.Environment) > limits.MaxEnvironment {
		return errors.New("usage manifest Go scope exceeds configured bounds")
	}
	return nil
}

func canonicalUsageTags(tags []string, maximum int) bool {
	if !slices.IsSorted(tags) {
		return false
	}
	for index, tag := range tags {
		if tag == "" || len(tag) > maximum || strings.ContainsAny(tag, " \t\r\n/\\") || index != 0 && tag == tags[index-1] {
			return false
		}
	}
	return true
}

func validateUsageDocumentScope(scope *i18n.GoUsageScope, complete bool, limits i18n.UsageLimits) error {
	if scope == nil {
		if complete {
			return errors.New("complete usage manifest requires Go scope provenance")
		}
		return nil
	}
	decodedDigest, digestErr := hex.DecodeString(scope.SourceDigest)
	if scope.Analyzer != i18n.GoUsageAnalyzerV2 || scope.GOOS == "" || scope.GOARCH == "" || scope.Compiler == "" || scope.GoVersion == "" || scope.Toolchain == "" ||
		(scope.GoWork != "off" && scope.GoWork != "active" && scope.GoWork != "mixed") || (scope.GoEnv != "off" && scope.GoEnv != "active") ||
		len(scope.Roots) == 0 || (complete && len(scope.Files) == 0) || len(scope.Metadata) == 0 ||
		digestErr != nil || len(decodedDigest) != 32 || strings.ToLower(scope.SourceDigest) != scope.SourceDigest {
		return errors.New("usage manifest has incomplete Go scope provenance")
	}
	if scope.SelectedFiles < 0 || scope.ExcludedFiles < 0 || scope.SelectedFiles > limits.MaxFiles || scope.ExcludedFiles > limits.MaxFiles-scope.SelectedFiles {
		return errors.New("usage manifest has invalid Go source counts")
	}
	if len(scope.Roots) > limits.MaxRoots || len(scope.BuildTags) > limits.MaxTags || len(scope.ToolTags) > limits.MaxTags || len(scope.ReleaseTags) > limits.MaxTags || len(scope.Environment) > limits.MaxEnvironment {
		return errors.New("usage manifest Go scope exceeds configured bounds")
	}
	if len(scope.Files) > limits.MaxFiles || len(scope.Metadata) > limits.MaxMetadata {
		return errors.New("usage manifest ledgers exceed configured bounds")
	}
	if scope.SourceDigest != i18n.ExpectedUsageSourceDigest(*scope) {
		return errors.New("usage manifest source digest does not match its ledger")
	}
	for _, root := range scope.Roots {
		if !root.Kind.Valid() || root.Path == "" {
			return errors.New("usage manifest has invalid Go roots")
		}
		if complete && root.Kind != i18n.UsageRootDirectory {
			return errors.New("complete usage manifest requires directory roots")
		}
	}
	if !slices.IsSortedFunc(scope.Roots, func(left, right i18n.UsageRoot) int {
		if value := strings.Compare(left.Path, right.Path); value != 0 {
			return value
		}
		return strings.Compare(left.Kind.String(), right.Kind.String())
	}) || !slices.IsSortedFunc(scope.Files, compareUsageFileDocument) || !slices.IsSortedFunc(scope.Metadata, compareUsageMetadataDocument) {
		return errors.New("usage manifest provenance ledgers are not sorted")
	}
	for _, tags := range [][]string{scope.BuildTags, scope.ToolTags, scope.ReleaseTags} {
		if !canonicalUsageTags(tags, limits.MaxStringBytes) {
			return errors.New("usage manifest tags are invalid, repeated, or not sorted")
		}
	}
	if !slices.IsSortedFunc(scope.Environment, func(left, right i18n.UsageSetting) int { return strings.Compare(left.Name, right.Name) }) || len(scope.Environment) == 0 {
		return errors.New("usage manifest Go environment is empty or not sorted")
	}
	for index, setting := range scope.Environment {
		if setting.Name == "" || strings.ContainsAny(setting.Name, " \t\r\n/\\") || index != 0 && setting.Name == scope.Environment[index-1].Name {
			return errors.New("usage manifest has invalid or repeated Go environment settings")
		}
	}
	settings := make(map[string]string, len(scope.Environment))
	for _, setting := range scope.Environment {
		settings[setting.Name] = setting.Value
	}
	if settings["GOOS"] != scope.GOOS || settings["GOARCH"] != scope.GOARCH || settings["GOVERSION"] != scope.GoVersion ||
		settings["GOEXPERIMENT"] != scope.GoExperiment || settings["GOFLAGS"] != scope.GoFlags || settings["GOWORK"] != scope.GoWork ||
		settings["GOENV"] != scope.GoEnv || settings["CGO_ENABLED"] != fmt.Sprint(scope.CgoEnabled) {
		return errors.New("usage manifest Go environment contradicts its indexed fields")
	}
	roots := make(map[string]i18n.UsageRootKind, len(scope.Roots))
	for index, root := range scope.Roots {
		if !validUsageDocumentPath(root.Path) || index != 0 && root == scope.Roots[index-1] {
			return errors.New("usage manifest has invalid or repeated Go roots")
		}
		roots[root.Path] = root.Kind
	}
	selected := 0
	excluded := 0
	for index, file := range scope.Files {
		if _, ok := roots[file.Root]; !ok || !validUsageDocumentPath(file.Path) || file.LogicalPath != file.Root+"/"+file.Path || !strings.HasSuffix(file.LogicalPath, ".go") || !validUsageDocumentSHA(file.SHA256) {
			return errors.New("usage manifest has a file outside its declared root")
		}
		if index != 0 && file.Root == scope.Files[index-1].Root && file.Path == scope.Files[index-1].Path {
			return errors.New("usage manifest has a repeated Go file")
		}
		if file.Selected {
			selected++
		} else {
			excluded++
		}
	}
	if selected != scope.SelectedFiles || excluded != scope.ExcludedFiles || complete && selected == 0 {
		return errors.New("usage manifest Go source counts do not match its ledger")
	}
	for index, metadata := range scope.Metadata {
		if metadata.Kind == "" || strings.ContainsAny(metadata.Kind, " \t\r\n/\\") || !validUsageDocumentPath(metadata.Path) || !validUsageDocumentSHA(metadata.SHA256) {
			return errors.New("usage manifest has invalid Go metadata")
		}
		if index != 0 && metadata.Kind == scope.Metadata[index-1].Kind && metadata.Path == scope.Metadata[index-1].Path {
			return errors.New("usage manifest has repeated Go metadata")
		}
	}
	return nil
}

func validateUsageDocument(manifest i18n.UsageManifest, limits i18n.UsageLimits) error {
	if err := validateUsageDocumentScope(manifest.GoScope, manifest.Complete, limits); err != nil {
		return err
	}
	if manifest.GoScope == nil || manifest.GoScope.Analyzer != i18n.GoUsageAnalyzerV2 {
		return nil
	}
	if !slices.IsSorted(manifest.Keys) || !slices.IsSortedFunc(manifest.Dynamic, func(left, right i18n.DynamicUsage) int {
		if value := strings.Compare(left.Domain, right.Domain); value != 0 {
			return value
		}
		return strings.Compare(left.Prefix, right.Prefix)
	}) || !slices.IsSortedFunc(manifest.Occurrences, compareUsageOccurrence) {
		return errors.New("usage manifest content is not canonically sorted")
	}
	keys := make(map[i18n.Key]bool, len(manifest.Keys))
	for _, key := range manifest.Keys {
		if keys[key] {
			return errors.New("usage manifest has repeated keys")
		}
		keys[key] = true
	}
	dynamic := make(map[string]bool, len(manifest.Dynamic))
	for _, value := range manifest.Dynamic {
		identity := value.Domain + "\x00" + value.Prefix
		if dynamic[identity] {
			return errors.New("usage manifest has repeated dynamic usage")
		}
		dynamic[identity] = true
	}
	selected := make(map[string]bool, manifest.GoScope.SelectedFiles)
	for _, file := range manifest.GoScope.Files {
		selected[file.LogicalPath] = file.Selected
	}
	coordinates := make(map[string]bool, len(manifest.Occurrences))
	occurrenceKeys := make(map[i18n.Key]bool, len(manifest.Keys))
	occurrenceDynamic := make(map[string]bool, len(manifest.Dynamic))
	for _, occurrence := range manifest.Occurrences {
		if !selected[occurrence.Path] {
			return errors.New("usage occurrence is not backed by a selected source file")
		}
		exact := occurrence.Key != ""
		bounded := occurrence.Domain != ""
		if exact == bounded || !bounded && occurrence.Prefix != "" || occurrence.Line <= 0 || occurrence.Column <= 0 ||
			occurrence.Line > maximumUsageCoordinate || occurrence.Column > maximumUsageCoordinate {
			return errors.New("usage occurrence identity or coordinate is invalid")
		}
		if exact {
			if !keys[occurrence.Key] {
				return errors.New("usage occurrence key is absent from usage keys")
			}
			occurrenceKeys[occurrence.Key] = true
		} else {
			identity := occurrence.Domain + "\x00" + occurrence.Prefix
			if !dynamic[identity] {
				return errors.New("usage occurrence is absent from dynamic usage")
			}
			occurrenceDynamic[identity] = true
		}
		coordinate := occurrence.Path + "\x00" + fmt.Sprint(occurrence.Line) + "\x00" + fmt.Sprint(occurrence.Column)
		if coordinates[coordinate] {
			return errors.New("usage manifest has repeated source coordinates")
		}
		coordinates[coordinate] = true
	}
	for _, key := range manifest.Keys {
		if !occurrenceKeys[key] {
			return errors.New("usage key has no source occurrence")
		}
	}
	for _, value := range manifest.Dynamic {
		if !occurrenceDynamic[value.Domain+"\x00"+value.Prefix] {
			return errors.New("dynamic usage has no source occurrence")
		}
	}
	if !validUsageDocumentSHA(manifest.ManifestDigest) {
		return errors.New("usage manifest digest is invalid")
	}
	return nil
}

func validUsageDocumentPath(value string) bool {
	return value != "" && len(value) <= maximumUsageJSONString && !pathpkg.IsAbs(value) && !strings.ContainsAny(value, "\\:\r\n") &&
		pathpkg.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func validUsageDocumentSHA(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

type usageJSONScan struct {
	ctx      context.Context
	limits   usageJSONLimits
	members  int
	material int
}

type usageJSONLimits struct {
	depth       int
	members     int
	keys        int
	dynamic     int
	occurrences int
	roots       int
	files       int
	metadata    int
	tags        int
	environment int
	stringBytes int
	material    int
}

func defaultUsageJSONLimits() usageJSONLimits {
	return usageJSONLimitsFrom(i18n.DefaultUsageLimits())
}

func usageJSONLimitsFrom(limits i18n.UsageLimits) usageJSONLimits {
	return usageJSONLimits{
		depth: maximumUsageJSONDepth, members: maximumUsageJSONMembers,
		keys: limits.MaxKeys, dynamic: limits.MaxDynamic, occurrences: limits.MaxOccurrences,
		roots: limits.MaxRoots, files: limits.MaxFiles, metadata: limits.MaxMetadata,
		tags: limits.MaxTags, environment: limits.MaxEnvironment,
		stringBytes: limits.MaxStringBytes, material: limits.MaxMaterialBytes,
	}
}

func preflightUsageJSON(ctx context.Context, raw []byte, limits usageJSONLimits) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	decoder := stdjson.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	state := usageJSONScan{ctx: ctx, limits: limits}
	if err := state.value(decoder, "", 1); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return fmt.Errorf("trailing data: %w", err)
	}
	return nil
}

func (s *usageJSONScan) value(decoder *stdjson.Decoder, path string, depth int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if depth > s.limits.depth {
		return fmt.Errorf("JSON depth exceeds %d", s.limits.depth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(stdjson.Delim)
	if !composite {
		if value, ok := token.(string); ok {
			return s.addString(value)
		}
		if value, ok := token.(stdjson.Number); ok {
			if len(value) > 64 {
				return errors.New("JSON number exceeds 64 bytes")
			}
			return s.addString(string(value))
		}
		return nil
	}
	switch delimiter {
	case '{':
		for decoder.More() {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("object member name is not a string")
			}
			if err := s.addString(name); err != nil {
				return err
			}
			if err := s.addMember(); err != nil {
				return err
			}
			child := name
			if path != "" {
				child = path + "." + name
			}
			if err := s.value(decoder, child, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != stdjson.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		count := 0
		maximum := usageJSONArrayLimit(path, s.limits)
		for decoder.More() {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			count++
			if count > maximum {
				return fmt.Errorf("%s exceeds %d entries", path, maximum)
			}
			if err := s.addMember(); err != nil {
				return err
			}
			if err := s.value(decoder, path+"[]", depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != stdjson.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return fmt.Errorf("unexpected delimiter %q", delimiter)
	}
	return nil
}

func (s *usageJSONScan) addMember() error {
	s.members++
	if s.members > s.limits.members {
		return fmt.Errorf("JSON members exceed %d", s.limits.members)
	}
	return nil
}

func (s *usageJSONScan) addString(value string) error {
	if len(value) > s.limits.stringBytes {
		return fmt.Errorf("JSON string exceeds %d bytes", s.limits.stringBytes)
	}
	if len(value) > s.limits.material-s.material {
		return fmt.Errorf("JSON string material exceeds %d bytes", s.limits.material)
	}
	s.material += len(value)
	return nil
}

func usageJSONArrayLimit(path string, limits usageJSONLimits) int {
	switch path {
	case "keys":
		return limits.keys
	case "dynamic":
		return limits.dynamic
	case "occurrences":
		return limits.occurrences
	case "go_scope.roots":
		return limits.roots
	case "go_scope.files":
		return limits.files
	case "go_scope.metadata":
		return limits.metadata
	case "go_scope.build_tags", "go_scope.tool_tags", "go_scope.release_tags":
		return limits.tags
	case "go_scope.environment":
		return limits.environment
	default:
		return limits.roots
	}
}

func encodeReport(report i18n.Report) ([]byte, error) {
	return encodeReportBoundedContext(context.Background(), report, maximumCommandInput)
}

func encodeReportBounded(report i18n.Report, maximum int) ([]byte, error) {
	return encodeReportBoundedContext(context.Background(), report, maximum)
}

func encodeReportBoundedContext(ctx context.Context, report i18n.Report, maximum int) ([]byte, error) {
	return encodeCommandJSONBoundedContext(ctx, "check report", report, maximum)
}

type commandJSONBuffer struct {
	bytes.Buffer
	maximum int
	ctx     context.Context
}

func (b *commandJSONBuffer) Write(value []byte) (int, error) {
	if b.ctx != nil {
		if err := b.ctx.Err(); err != nil {
			return 0, err
		}
	}
	if len(value) > b.maximum-b.Len() {
		return 0, errCommandJSONTooLarge
	}
	return b.Buffer.Write(value)
}

func encodeCommandJSON(name string, value any) ([]byte, error) {
	return encodeCommandJSONBoundedContext(context.Background(), name, value, maximumCommandInput)
}

func encodeCommandJSONBounded(name string, value any, maximum int) ([]byte, error) {
	return encodeCommandJSONBoundedContext(context.Background(), name, value, maximum)
}

func encodeCommandJSONBoundedContext(ctx context.Context, name string, value any, maximum int) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("encode %s: context is nil", name)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return nil, fmt.Errorf("encode %s: output limit %d is outside supported bounds", name, maximum)
	}
	buffer := commandJSONBuffer{maximum: maximum - 1, ctx: ctx}
	err := jsonv2.MarshalWrite(&buffer, value, jsonv2.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append(buffer.Bytes(), '\n'), nil
}
