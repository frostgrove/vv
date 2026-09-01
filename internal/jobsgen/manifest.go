package jobsgen

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

	"github.com/frostgrove/vv/jobs"
)

const manifestFormat = 1

type manifestDocument struct {
	Format      int           `json:"format"`
	GeneratedBy string        `json:"generated_by"`
	Package     string        `json:"package"`
	Catalog     string        `json:"catalog"`
	Jobs        []manifestJob `json:"jobs"`
}

type manifestJob struct {
	Variable  string          `json:"variable"`
	Name      string          `json:"name"`
	Kind      string          `json:"declaration"`
	Payload   manifestPayload `json:"payload"`
	Codec     manifestCodec   `json:"codec"`
	Partition string          `json:"partition"`
}

type manifestPayload struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

type manifestCodec struct {
	Kind    string             `json:"kind"`
	Version jobs.SchemaVersion `json:"version"`
}

func readManifest(path, importPath string) (*manifestDocument, error) {
	source, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("jobsgen: read manifest: %w", err)
	}
	var document manifestDocument
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("jobsgen: parse manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, fmt.Errorf("jobsgen: parse manifest: multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("jobsgen: parse manifest: %w", err)
	}
	if document.Format != manifestFormat || document.GeneratedBy != "vv generate jobs" || document.Catalog != "VVJobsCatalog" {
		return nil, fmt.Errorf("jobsgen: unsupported or unrelated jobs manifest")
	}
	if document.Package != importPath {
		return nil, fmt.Errorf("jobsgen: manifest package %q does not match %q", document.Package, importPath)
	}
	seenVariables := map[string]struct{}{}
	seenNames := map[string]struct{}{}
	for _, entry := range document.Jobs {
		if err := validateManifestEntry(entry); err != nil {
			return nil, err
		}
		if _, exists := seenVariables[entry.Variable]; exists {
			return nil, fmt.Errorf("jobsgen: manifest contains duplicate variable %q", entry.Variable)
		}
		if _, exists := seenNames[entry.Name]; exists {
			return nil, fmt.Errorf("jobsgen: manifest contains duplicate name %q", entry.Name)
		}
		seenVariables[entry.Variable] = struct{}{}
		seenNames[entry.Name] = struct{}{}
	}
	return &document, nil
}

func buildManifest(loaded *loadedPackage, declarations []declaration, previous *manifestDocument) (manifestDocument, error) {
	document := manifestDocument{Format: manifestFormat, GeneratedBy: "vv generate jobs", Package: loaded.importPath, Catalog: "VVJobsCatalog", Jobs: make([]manifestJob, 0, len(declarations))}
	prior := map[string]manifestJob{}
	if previous != nil {
		for _, entry := range previous.Jobs {
			prior[entry.Variable] = entry
		}
	}
	seenNames := map[string]string{}
	for _, declaration := range declarations {
		entry := newManifestEntry(loaded, declaration)
		if existing, ok := prior[declaration.variable]; ok {
			entry.Name = existing.Name
			entry.Codec = existing.Codec
			entry.Partition = existing.Partition
		}
		if err := validateManifestEntry(entry); err != nil {
			return manifestDocument{}, err
		}
		if other := seenNames[entry.Name]; other != "" {
			return manifestDocument{}, fmt.Errorf("jobsgen: declarations %s and %s use duplicate name %q", other, entry.Variable, entry.Name)
		}
		seenNames[entry.Name] = entry.Variable
		document.Jobs = append(document.Jobs, entry)
	}
	return document, nil
}

func newManifestEntry(loaded *loadedPackage, declaration declaration) manifestJob {
	shape := types.TypeString(declaration.payload, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	})
	digest := sha256.Sum256([]byte(shape))
	return manifestJob{
		Variable: declaration.variable,
		Name:     defaultName(loaded.name, declaration.variable),
		Kind:     declaration.kind,
		Payload: manifestPayload{
			Type:        types.TypeString(declaration.payload, packageNameQualifier(loaded.types)),
			Fingerprint: "sha256:" + hex.EncodeToString(digest[:]),
		},
		Codec:     manifestCodec{Kind: "safe-json", Version: 1},
		Partition: "global",
	}
}

func validateManifestEntry(entry manifestJob) error {
	if entry.Variable == "" || entry.Kind != "Auto" && entry.Kind != "Declare" || entry.Payload.Type == "" || !strings.HasPrefix(entry.Payload.Fingerprint, "sha256:") {
		return fmt.Errorf("jobsgen: manifest entry %q is incomplete", entry.Variable)
	}
	if _, err := jobs.ParseName(entry.Name); err != nil {
		return fmt.Errorf("jobsgen: manifest name %q is invalid: %w", entry.Name, err)
	}
	if entry.Codec.Kind != "safe-json" || entry.Codec.Version == 0 {
		return fmt.Errorf("jobsgen: %s codec must be safe-json with a positive version", entry.Variable)
	}
	if entry.Partition != "global" && entry.Partition != "tenant_required" {
		return fmt.Errorf("jobsgen: %s partition must be global or tenant_required", entry.Variable)
	}
	return nil
}

func packageNameQualifier(current *types.Package) types.Qualifier {
	return func(pkg *types.Package) string {
		if pkg == nil || pkg.Path() == current.Path() {
			return ""
		}
		return pkg.Name()
	}
}

func defaultName(packageName, variable string) string {
	return wirePart(packageName) + "." + wirePart(variable)
}

func wirePart(value string) string {
	var result strings.Builder
	separator := false
	for _, character := range strings.ToLower(value) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			result.WriteRune(character)
			separator = false
			continue
		}
		if result.Len() != 0 && !separator {
			result.WriteByte('-')
			separator = true
		}
	}
	return strings.Trim(result.String(), "-")
}

func marshalManifest(document manifestDocument) ([]byte, error) {
	sort.Slice(document.Jobs, func(left, right int) bool {
		return document.Jobs[left].Variable < document.Jobs[right].Variable
	})
	source, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("jobsgen: marshal manifest: %w", err)
	}
	return append(source, '\n'), nil
}
