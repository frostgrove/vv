package cachegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/types"
	"io"
	"os"
	"sort"
	"strings"

	frameworkcache "github.com/frostgrove/vv/cache"
)

const (
	generatedKeyCodec             = "generated-key-func"
	generatedKeyAlgorithm         = "vv-key-frame/struct-count-u64be/field-declaration-order/pointer-slice-presence-u8/string-bytes-length-u32be/sequence-length-u64be/integer-twos-complement-u64be/float-ieee754be-signed-zero-normalized/bool-u8"
	generatedKeyAlgorithmRevision = 1
	stringValueAlgorithm          = "raw-utf8"
	bytesValueAlgorithm           = "raw-bytes"
	jsonValueAlgorithm            = "encoding/json"
	rfc3339UTCValueAlgorithm      = "rfc3339-utc-canonical-z"
	valueAlgorithmRevision        = 1
)

type manifestDocument struct {
	Format             int             `json:"format"`
	GeneratedBy        string          `json:"generated_by"`
	Package            string          `json:"package"`
	Activation         string          `json:"activation"`
	BuildTarget        buildTarget     `json:"build_target"`
	CompatibilityProof string          `json:"compatibility_proof"`
	Caches             []manifestCache `json:"caches"`
}

type manifestCache struct {
	Name      string            `json:"name"`
	Variable  string            `json:"variable"`
	Namespace manifestNamespace `json:"namespace"`
	Scope     manifestScope     `json:"scope"`
	Key       manifestType      `json:"key"`
	Value     manifestValue     `json:"value"`
	Profile   manifestProfile   `json:"profile"`
}

type manifestNamespace struct {
	ApplicationSource string                    `json:"application_source"`
	EnvironmentSource string                    `json:"environment_source"`
	Purpose           string                    `json:"purpose"`
	Generation        frameworkcache.Generation `json:"generation"`
	GenerationAnchor  frameworkcache.Generation `json:"generation_anchor"`
}

type manifestScope struct {
	InferredMode           string `json:"inferred_mode"`
	InferredPartitionField string `json:"inferred_partition_field,omitempty"`
	Mode                   string `json:"mode"`
	PartitionField         string `json:"partition_field,omitempty"`
	DecisionFingerprint    string `json:"decision_fingerprint"`
	Confirmed              bool   `json:"confirmed"`
}

type manifestType struct {
	Codec              string                    `json:"codec"`
	Algorithm          string                    `json:"algorithm"`
	AlgorithmRevision  uint32                    `json:"algorithm_revision"`
	Type               string                    `json:"type"`
	Fingerprint        string                    `json:"fingerprint"`
	Version            frameworkcache.KeyVersion `json:"version"`
	FingerprintVersion frameworkcache.KeyVersion `json:"fingerprint_version"`
}

type manifestValue struct {
	Type              string                     `json:"type"`
	Codec             string                     `json:"codec"`
	Algorithm         string                     `json:"algorithm"`
	AlgorithmRevision uint32                     `json:"algorithm_revision"`
	Fingerprint       string                     `json:"fingerprint"`
	Schema            frameworkcache.ValueSchema `json:"schema"`
	FingerprintSchema frameworkcache.ValueSchema `json:"fingerprint_schema"`
}

type manifestProfile struct {
	Expression   string                           `json:"expression"`
	Name         string                           `json:"name"`
	ProviderKind frameworkcache.ProviderKind      `json:"provider_kind"`
	ProviderID   frameworkcache.ProviderID        `json:"provider_id"`
	Policy       frameworkcache.PolicyDescription `json:"policy"`
}

func readManifest(path, importPath string) (*manifestDocument, error) {
	source, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cachegen: read manifest: %w", err)
	}
	var document manifestDocument
	if err := decodeJSON(source, &document); err != nil {
		return nil, fmt.Errorf("cachegen: parse manifest: %w", err)
	}
	if document.Format != manifestFormat || document.GeneratedBy != "vv generate cache" {
		return nil, fmt.Errorf("cachegen: unsupported or unrelated cache manifest")
	}
	if document.Package != importPath {
		return nil, fmt.Errorf("cachegen: manifest package %q does not match %q", document.Package, importPath)
	}
	if document.BuildTarget.GOOS == "" || document.BuildTarget.GOARCH == "" || document.BuildTarget.GoVersion == "" {
		return nil, fmt.Errorf("cachegen: manifest build target is incomplete")
	}
	seen := map[string]struct{}{}
	for _, item := range document.Caches {
		if item.Name == "" || item.Variable == "" {
			return nil, fmt.Errorf("cachegen: manifest contains an unnamed cache")
		}
		if _, exists := seen[item.Name]; exists {
			return nil, fmt.Errorf("cachegen: manifest contains duplicate cache %q", item.Name)
		}
		seen[item.Name] = struct{}{}
	}
	return &document, nil
}

func decodeJSON(source []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func buildManifest(loaded *loadedPackage, declarations []declaration, previous *manifestDocument) (manifestDocument, error) {
	document := manifestDocument{
		Format:      manifestFormat,
		GeneratedBy: "vv generate cache",
		Package:     loaded.importPath,
		Activation:  "VVCacheSet",
		BuildTarget: loaded.target,
		Caches:      make([]manifestCache, 0, len(declarations)),
	}
	old := map[string]manifestCache{}
	if previous != nil {
		for _, item := range previous.Caches {
			old[item.Name] = item
		}
	}
	problems := make([]string, 0)
	for _, declaration := range declarations {
		entry := newManifestEntry(declaration)
		if prior, exists := old[entry.Name]; exists {
			var err error
			entry, err = mergeManifestEntry(declaration, entry, prior)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
		}
		if err := validateManifestEntry(declaration, entry); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		document.Caches = append(document.Caches, entry)
	}
	if len(problems) != 0 {
		sort.Strings(problems)
		return manifestDocument{}, fmt.Errorf("cachegen: manifest compatibility failed: %s", strings.Join(problems, "; "))
	}
	document.CompatibilityProof = manifestCompatibilityProof(document)
	return document, nil
}

func newManifestEntry(declaration declaration) manifestCache {
	valueCodec, valueAlgorithm := valueCodecSpec(declaration.valueType)
	return manifestCache{
		Name:     declaration.logicalName,
		Variable: declaration.variable,
		Namespace: manifestNamespace{
			ApplicationSource: "cache.ActivationSpec.Application",
			EnvironmentSource: "cache.ActivationSpec.Environment",
			Purpose:           declaration.logicalName,
			Generation:        1,
			GenerationAnchor:  1,
		},
		Scope: manifestScope{
			InferredMode:           declaration.key.inferredMode,
			InferredPartitionField: declaration.key.partitionName,
			Mode:                   declaration.key.inferredMode,
			PartitionField:         declaration.key.partitionName,
			DecisionFingerprint:    scopeFingerprint(declaration.key.inferredMode, declaration.key.partitionName),
		},
		Key: manifestType{
			Codec:              generatedKeyCodec,
			Algorithm:          generatedKeyAlgorithm,
			AlgorithmRevision:  generatedKeyAlgorithmRevision,
			Type:               declaration.keySyntax,
			Fingerprint:        fingerprint(declaration.keyType),
			Version:            1,
			FingerprintVersion: 1,
		},
		Value: manifestValue{
			Type:              declaration.valueSyntax,
			Codec:             valueCodec,
			Algorithm:         valueAlgorithm,
			AlgorithmRevision: valueAlgorithmRevision,
			Fingerprint:       fingerprint(declaration.valueType),
			Schema:            1,
			FingerprintSchema: 1,
		},
		Profile: manifestProfile{
			Expression:   declaration.profile.expression,
			Name:         declaration.profile.description.Name,
			ProviderKind: declaration.profile.description.Provider,
			Policy:       declaration.profile.description.Policy,
		},
	}
}

func mergeManifestEntry(declaration declaration, fresh, prior manifestCache) (manifestCache, error) {
	if prior.Variable != fresh.Variable || prior.Namespace.Purpose != fresh.Namespace.Purpose {
		return manifestCache{}, fmt.Errorf("%s: source identity changed", fresh.Name)
	}
	if prior.Namespace.Generation == 0 || prior.Namespace.GenerationAnchor == 0 || prior.Key.Version == 0 || prior.Value.Schema == 0 {
		return manifestCache{}, fmt.Errorf("%s: generation, key version, and value schema must be positive", fresh.Name)
	}
	if prior.Namespace.Generation < prior.Namespace.GenerationAnchor {
		return manifestCache{}, fmt.Errorf("%s: namespace generation cannot move below accepted generation %d", fresh.Name, prior.Namespace.GenerationAnchor)
	}
	fresh.Namespace.Generation = prior.Namespace.Generation
	fresh.Namespace.GenerationAnchor = prior.Namespace.Generation
	fresh.Key.Version = prior.Key.Version
	fresh.Value.Schema = prior.Value.Schema
	fresh.Profile.ProviderID = prior.Profile.ProviderID
	if prior.Key.FingerprintVersion == 0 || prior.Key.Version < prior.Key.FingerprintVersion {
		return manifestCache{}, fmt.Errorf("%s: key fingerprint version is invalid", fresh.Name)
	}
	if prior.Value.FingerprintSchema == 0 || prior.Value.Schema < prior.Value.FingerprintSchema {
		return manifestCache{}, fmt.Errorf("%s: value fingerprint schema is invalid", fresh.Name)
	}
	keyChanged := prior.Key.Fingerprint != fresh.Key.Fingerprint || prior.Key.Codec != fresh.Key.Codec ||
		prior.Key.Algorithm != fresh.Key.Algorithm || prior.Key.AlgorithmRevision != fresh.Key.AlgorithmRevision
	valueChanged := prior.Value.Fingerprint != fresh.Value.Fingerprint || prior.Value.Codec != fresh.Value.Codec ||
		prior.Value.Algorithm != fresh.Value.Algorithm || prior.Value.AlgorithmRevision != fresh.Value.AlgorithmRevision
	if keyChanged {
		if prior.Key.Version <= prior.Key.FingerprintVersion {
			return manifestCache{}, fmt.Errorf("%s: key shape or codec changed without a KeyVersion bump", fresh.Name)
		}
		fresh.Key.FingerprintVersion = prior.Key.Version
	} else {
		fresh.Key.FingerprintVersion = prior.Key.Version
		fresh.Scope.Mode = prior.Scope.Mode
		fresh.Scope.PartitionField = prior.Scope.PartitionField
		fresh.Scope.Confirmed = prior.Scope.Confirmed
		fresh.Scope.DecisionFingerprint = scopeFingerprint(fresh.Scope.Mode, fresh.Scope.PartitionField)
		if prior.Scope.DecisionFingerprint != fresh.Scope.DecisionFingerprint {
			fresh.Scope.Confirmed = false
		}
	}
	if valueChanged {
		if prior.Value.Schema <= prior.Value.FingerprintSchema {
			return manifestCache{}, fmt.Errorf("%s: value shape or codec changed without a ValueSchema bump", fresh.Name)
		}
		fresh.Value.FingerprintSchema = prior.Value.Schema
	} else {
		fresh.Value.FingerprintSchema = prior.Value.Schema
	}
	return fresh, nil
}

func validateManifestEntry(declaration declaration, entry manifestCache) error {
	if err := validManifestPart(entry.Name); err != nil {
		return fmt.Errorf("%s: logical name is invalid: %w", entry.Name, err)
	}
	if err := validManifestPart(entry.Namespace.Purpose); err != nil {
		return fmt.Errorf("%s: namespace purpose is invalid: %w", entry.Name, err)
	}
	if entry.Namespace.GenerationAnchor == 0 || entry.Namespace.Generation < entry.Namespace.GenerationAnchor || entry.Key.FingerprintVersion == 0 ||
		entry.Value.FingerprintSchema == 0 || entry.Key.AlgorithmRevision == 0 || entry.Value.AlgorithmRevision == 0 {
		return fmt.Errorf("%s: fingerprint version metadata is missing", entry.Name)
	}
	if entry.Scope.Mode != "global" && entry.Scope.Mode != "partitioned" {
		return fmt.Errorf("%s: scope mode must be global or partitioned", entry.Name)
	}
	if entry.Scope.DecisionFingerprint != scopeFingerprint(entry.Scope.Mode, entry.Scope.PartitionField) {
		return fmt.Errorf("%s: scope decision fingerprint is stale", entry.Name)
	}
	if entry.Scope.Mode == "global" {
		if entry.Scope.PartitionField != "" {
			return fmt.Errorf("%s: global scope cannot name a partition field", entry.Name)
		}
	} else {
		field := keyField(declaration.keyType, entry.Scope.PartitionField)
		if field == nil {
			return fmt.Errorf("%s: partition field %q is not a top-level key field", entry.Name, entry.Scope.PartitionField)
		}
		if err := validateKeyType(field.Type(), map[types.Type]bool{}); err != nil {
			return fmt.Errorf("%s: partition field %q: %w", entry.Name, entry.Scope.PartitionField, err)
		}
		if err := validatePartitionType(field.Type(), map[types.Type]bool{}); err != nil {
			return fmt.Errorf("%s: partition field %q: %w", entry.Name, entry.Scope.PartitionField, err)
		}
	}
	provider := string(entry.Profile.ProviderID)
	if provider != "" && validManifestPart(provider) != nil {
		return fmt.Errorf("%s: provider id is invalid", entry.Name)
	}
	if provider != "" && entry.Profile.ProviderKind == frameworkcache.NoProviderKind {
		return fmt.Errorf("%s: disabled cache cannot select a provider", entry.Name)
	}
	return nil
}

func validManifestPart(value string) error {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return fmt.Errorf("length or surrounding whitespace is invalid")
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("only printable ASCII is allowed")
		}
	}
	return nil
}

func keyField(value types.Type, name string) *types.Var {
	if name == "" {
		return nil
	}
	structure, ok := types.Unalias(value).Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if field.Name() == name {
			return field
		}
	}
	return nil
}

func fingerprint(value types.Type) string {
	hash := sha256.Sum256([]byte(typeShape(value)))
	return "sha256:" + hex.EncodeToString(hash[:])
}

func scopeFingerprint(mode, partitionField string) string {
	hash := sha256.Sum256([]byte(mode + "\x00" + partitionField))
	return "sha256:" + hex.EncodeToString(hash[:])
}

func valueCodecSpec(value types.Type) (string, string) {
	value = types.Unalias(value)
	if isExactTimeValue(value) {
		return "time-rfc3339-utc", rfc3339UTCValueAlgorithm
	}
	if types.Identical(value, types.Typ[types.String]) {
		return "string", stringValueAlgorithm
	}
	slice, ok := value.(*types.Slice)
	if ok && types.Identical(slice.Elem(), types.Typ[types.Byte]) {
		return "bytes", bytesValueAlgorithm
	}
	return "json", jsonValueAlgorithm
}

func manifestCompatibilityProof(document manifestDocument) string {
	type record struct {
		Name                   string
		GenerationAnchor       frameworkcache.Generation
		KeyCodec               string
		KeyAlgorithm           string
		KeyAlgorithmRevision   uint32
		KeyFingerprint         string
		KeyFingerprintVersion  frameworkcache.KeyVersion
		ValueCodec             string
		ValueAlgorithm         string
		ValueAlgorithmRevision uint32
		ValueFingerprint       string
		ValueFingerprintSchema frameworkcache.ValueSchema
	}
	caches := append([]manifestCache(nil), document.Caches...)
	sort.Slice(caches, func(left, right int) bool { return caches[left].Name < caches[right].Name })
	records := make([]record, 0, len(caches))
	for _, cache := range caches {
		records = append(records, record{
			Name:                   cache.Name,
			GenerationAnchor:       cache.Namespace.GenerationAnchor,
			KeyCodec:               cache.Key.Codec,
			KeyAlgorithm:           cache.Key.Algorithm,
			KeyAlgorithmRevision:   cache.Key.AlgorithmRevision,
			KeyFingerprint:         cache.Key.Fingerprint,
			KeyFingerprintVersion:  cache.Key.FingerprintVersion,
			ValueCodec:             cache.Value.Codec,
			ValueAlgorithm:         cache.Value.Algorithm,
			ValueAlgorithmRevision: cache.Value.AlgorithmRevision,
			ValueFingerprint:       cache.Value.Fingerprint,
			ValueFingerprintSchema: cache.Value.FingerprintSchema,
		})
	}
	source, _ := json.Marshal(struct {
		Format      int
		Package     string
		BuildTarget buildTarget
		Caches      []record
	}{Format: document.Format, Package: document.Package, BuildTarget: document.BuildTarget, Caches: records})
	hash := sha256.Sum256(source)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func marshalManifest(document manifestDocument) ([]byte, error) {
	source, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("cachegen: encode manifest: %w", err)
	}
	return append(source, '\n'), nil
}

func unconfirmedCaches(document manifestDocument) []string {
	result := make([]string, 0)
	for _, entry := range document.Caches {
		if !entry.Scope.Confirmed {
			result = append(result, entry.Name)
		}
	}
	sort.Strings(result)
	return result
}
