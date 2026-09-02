package codegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	manifestFormat      = 1
	manifestGeneratedBy = "vv generate resource"
)

type DriftError struct {
	Paths []string
}

func (this *DriftError) Error() string {
	return "codegen: generated artefacts are stale: " + strings.Join(this.Paths, ", ")
}

type ConfirmationError struct {
	Manifest string
	Bodies   []string
}

func (this *ConfirmationError) Error() string {
	return fmt.Sprintf("codegen: a wire body publishes more than the narrowing derives; set confirmed: true in %s for: %s",
		this.Manifest, strings.Join(this.Bodies, ", "))
}

type manifestDocument struct {
	Format      int                `json:"format"`
	GeneratedBy string             `json:"generated_by"`
	Package     string             `json:"package"`
	Resources   []manifestResource `json:"resources"`
}

type manifestResource struct {
	Model    string       `json:"model"`
	Create   manifestBody `json:"create"`
	Patch    manifestBody `json:"patch"`
	Response manifestBody `json:"response"`
}

type manifestBody struct {
	Narrowed    []string `json:"narrowed"`
	Fields      []string `json:"fields"`
	Widened     []string `json:"widened"`
	Fingerprint string   `json:"derivation_fingerprint"`
	Confirmed   bool     `json:"confirmed"`
}

func (this manifestResource) body(name string) manifestBody {
	switch name {
	case "create":
		return this.Create
	case "patch":
		return this.Patch
	default:
		return this.Response
	}
}

func readResourceManifest(path, pkg string) (*manifestDocument, error) {
	source, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("codegen: read %s: %w", path, err)
	}
	var document manifestDocument
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("codegen: refusing to overwrite an unrelated manifest %s: %w", path, err)
	}
	if document.Format != manifestFormat || document.GeneratedBy != manifestGeneratedBy {
		return nil, fmt.Errorf("codegen: refusing to overwrite an unrelated manifest %s", path)
	}
	if document.Package != pkg {
		return nil, fmt.Errorf("codegen: %s belongs to package %q, not %q", path, document.Package, pkg)
	}
	seen := map[string]bool{}
	for _, resource := range document.Resources {
		if resource.Model == "" {
			return nil, fmt.Errorf("codegen: %s carries an unnamed resource", path)
		}
		if seen[resource.Model] {
			return nil, fmt.Errorf("codegen: %s carries %s twice", path, resource.Model)
		}
		seen[resource.Model] = true
	}
	return &document, nil
}

func mergeManifestBody(narrowed, publishable []string, prior *manifestBody) (manifestBody, error) {
	fresh := manifestBody{Narrowed: append([]string(nil), narrowed...), Fields: append([]string(nil), narrowed...)}
	if prior != nil {
		fresh.Fields = append([]string(nil), prior.Fields...)
	}
	sort.Strings(fresh.Fields)

	allowed := setOf(publishable)
	derived := setOf(narrowed)
	seen := map[string]bool{}
	fresh.Widened = []string{}
	for _, name := range fresh.Fields {
		if seen[name] {
			return manifestBody{}, fmt.Errorf("%s is listed twice", name)
		}
		seen[name] = true
		if !allowed[name] {
			return manifestBody{}, fmt.Errorf("%s cannot be published here: the model does not carry it, or the shape this body maps onto does not", name)
		}
		if !derived[name] {
			fresh.Widened = append(fresh.Widened, name)
		}
	}
	fresh.Fingerprint = derivationFingerprint(fresh.Narrowed)
	if prior != nil && prior.Fingerprint == fresh.Fingerprint {
		fresh.Confirmed = prior.Confirmed
	}
	return fresh, nil
}

func derivationFingerprint(narrowed []string) string {
	sum := sha256.New()
	for _, name := range narrowed {
		sum.Write([]byte(name))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func unconfirmedBodies(document manifestDocument) []string {
	out := []string{}
	for _, resource := range document.Resources {
		for _, name := range []string{"create", "patch", "response"} {
			body := resource.body(name)
			if len(body.Widened) > 0 && !body.Confirmed {
				out = append(out, resource.Model+" "+name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func marshalManifest(document manifestDocument) ([]byte, error) {
	source, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("codegen: encode manifest: %w", err)
	}
	return append(source, '\n'), nil
}

func validateManifestTarget(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("codegen: inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("codegen: refusing symlink manifest %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("codegen: manifest %s is not a regular file", path)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("codegen: inspect %s: %w", path, err)
	}
	var header struct {
		GeneratedBy string `json:"generated_by"`
	}
	if json.Unmarshal(current, &header) != nil || header.GeneratedBy != manifestGeneratedBy {
		return fmt.Errorf("codegen: refusing to overwrite an unrelated manifest %s", path)
	}
	return nil
}

func setOf(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}
