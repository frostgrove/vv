package cachegen

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	frameworkcache "github.com/frostgrove/vv/cache"
)

func TestGenerationRequiresScopeConfirmationThenBuildsDeterministicWiring(t *testing.T) {
	directory := fixturePackage(t, "basic/cache.go.txt")
	options := Options{Dir: directory}
	err := Run(&options)
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) || len(confirmation.Caches) != 2 {
		t.Fatalf("initial generation error = %v, want two unconfirmed scopes", err)
	}

	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	generatedPath := filepath.Join(directory, "vv_cache_gen.go")
	document := readTestManifest(t, manifestPath)
	canonical, marshalErr := marshalManifest(document)
	if marshalErr != nil || !bytes.Equal(canonical, readFile(t, manifestPath)) || canonical[0] != '{' {
		t.Fatalf("manifest is not canonical JSON-valid YAML: %v\n%s", marshalErr, readFile(t, manifestPath))
	}
	if len(document.Caches) != 2 || document.Caches[0].Variable != "Globals" || document.Caches[1].Variable != "Projections" {
		t.Fatalf("manifest caches = %#v, want stable logical-name order", document.Caches)
	}
	if document.CompatibilityProof == "" || document.Caches[0].Key.Codec != generatedKeyCodec ||
		document.Caches[0].Key.Algorithm != generatedKeyAlgorithm || document.Caches[0].Key.AlgorithmRevision != generatedKeyAlgorithmRevision {
		t.Fatalf("manifest lacks canonical codec identity or compatibility proof: %#v", document)
	}
	if document.BuildTarget.GOOS != runtime.GOOS || document.BuildTarget.GOARCH != runtime.GOARCH || document.BuildTarget.GoVersion == "" {
		t.Fatalf("manifest build target = %#v", document.BuildTarget)
	}
	if document.Caches[0].Scope.InferredMode != "global" || document.Caches[1].Scope.InferredMode != "partitioned" || document.Caches[1].Scope.InferredPartitionField != "TenantID" {
		t.Fatalf("scope inference = %#v", document.Caches)
	}
	if document.Caches[1].Profile.Expression != "cache.Hot.With(cache.MaxValueBytes(2097152), cache.NegativeFor(60000000000))" ||
		document.Caches[1].Profile.Policy.MaxValueBytes != 2<<20 || document.Caches[1].Profile.Policy.Negative.Duration != 60_000_000_000 {
		t.Fatalf("materialized profile = %#v", document.Caches[1].Profile)
	}
	guard := readFile(t, generatedPath)
	if !bytes.Contains(guard, []byte("confirm every cache scope")) || bytes.Contains(guard, []byte("MustDefine")) {
		t.Fatalf("unconfirmed generation activated inferred scopes:\n%s", guard)
	}
	goTestFails(t, directory, "confirm every cache scope")
	manifestUnconfirmed := readFile(t, manifestPath)
	if err := Run(&options); !errors.As(err, &confirmation) {
		t.Fatalf("repeat unconfirmed generation: %v", err)
	}
	if !bytes.Equal(manifestUnconfirmed, readFile(t, manifestPath)) || !bytes.Equal(guard, readFile(t, generatedPath)) {
		t.Fatal("repeat unconfirmed generation drifted")
	}

	for index := range document.Caches {
		document.Caches[index].Scope.Confirmed = true
	}
	writeTestManifest(t, manifestPath, document)
	if err := Run(&options); err != nil {
		t.Fatalf("generate confirmed caches: %v", err)
	}
	generated := readFile(t, generatedPath)
	for _, expected := range []string{
		"var VVCacheSet = _vvcache.MustSet(",
		"_vvcache.MustDefine(Globals",
		"_vvcache.MustDefine(Projections",
		"_vvcache.GlobalPlan[GlobalKey]()",
		"_vvcache.PartitionedPlan[ProjectionKey]",
		"_vvcache.MustKeyFunc[ProjectionKey]",
		"_vvcache.String(_vvcache.ValueSchema(1))",
		"_vvcache.JSON[Payload]",
	} {
		if !bytes.Contains(generated, []byte(expected)) {
			t.Fatalf("generated wiring lacks %q:\n%s", expected, generated)
		}
	}
	commentOpen := string([]byte{'/', '/'})
	blockOpen := string([]byte{'/', '*'})
	for _, forbidden := range []string{"reflect.", "func init(", "ExplicitFactory", commentOpen, blockOpen} {
		if bytes.Contains(generated, []byte(forbidden)) {
			t.Fatalf("generated wiring contains %q:\n%s", forbidden, generated)
		}
	}
	if err := Run(&Options{Dir: directory, Check: true}); err != nil {
		t.Fatalf("check fresh artifacts: %v", err)
	}
	manifestBefore := readFile(t, manifestPath)
	generatedBefore := append([]byte(nil), generated...)
	if err := Run(&options); err != nil {
		t.Fatalf("regenerate stable artifacts: %v", err)
	}
	if !bytes.Equal(manifestBefore, readFile(t, manifestPath)) || !bytes.Equal(generatedBefore, readFile(t, generatedPath)) {
		t.Fatal("unchanged inputs produced different artifacts")
	}
	writeGeneratedBehaviorTest(t, directory)
	goTestJSONActivation(t, directory)
}

func TestCheckReportsDriftWithoutWriting(t *testing.T) {
	directory := confirmedFixture(t)
	generatedPath := filepath.Join(directory, "vv_cache_gen.go")
	stale := append(readFile(t, generatedPath), '\n')
	if err := os.WriteFile(generatedPath, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory, Check: true})
	var drift *DriftError
	if !errors.As(err, &drift) || len(drift.Paths) != 1 || drift.Paths[0] != generatedPath {
		t.Fatalf("check error = %v, want generated Go drift", err)
	}
	if !bytes.Equal(stale, readFile(t, generatedPath)) {
		t.Fatal("check mode changed a stale artifact")
	}
}

func TestTransientPolicyIsMaterializedAndCheckedForDrift(t *testing.T) {
	directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

var Values = cachealias.Auto[string, string](
	cachealias.Hot.With(
		cachealias.MaxTransientBytes(536870912),
		cachealias.MaxTransientWaiters(7),
		cachealias.TransientSaturation(cachealias.RejectTransient()),
	),
)
`)
	err := Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("initial transient policy generation = %v", err)
	}
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	policy := document.Caches[0].Profile.Policy
	if policy.MaxTransientBytes != 536870912 || policy.MaxTransientWaiters != 7 || policy.TransientSaturation != frameworkcache.RejectTransientMode || policy.TransientWait != 0 {
		t.Fatalf("materialized transient policy = %#v", policy)
	}
	if document.Caches[0].Profile.Expression != "cache.Hot.With(cache.MaxTransientBytes(536870912), cache.MaxTransientWaiters(7), cache.TransientSaturation(cache.RejectTransient()))" {
		t.Fatalf("transient policy expression = %q", document.Caches[0].Profile.Expression)
	}
	document.Caches[0].Scope.Confirmed = true
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("confirmed transient policy generation = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "transient_descriptor_test.go"), []byte(`package sample

import "testing"

func TestGeneratedTransientDescriptor(t *testing.T) {
	descriptors := VVCacheSet.Describe()
	if len(descriptors) != 1 || descriptors[0].Policy.MaxTransientWaiters != 7 || descriptors[0].Policy.MaxTransientBytes != 536870912 {
		t.Fatalf("generated transient descriptor = %#v", descriptors)
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	goTest(t, directory)
	sourcePath := filepath.Join(directory, "cache.go")
	source := strings.Replace(string(readFile(t, sourcePath)), "536870912", "536870913", 1)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Run(&Options{Dir: directory, Check: true})
	var drift *DriftError
	if !errors.As(err, &drift) || len(drift.Paths) != 1 || drift.Paths[0] != manifestPath {
		t.Fatalf("transient policy check drift = %v", err)
	}
}

func TestKeyShapeNeedsVersionBumpAndFreshScopeConfirmation(t *testing.T) {
	directory := confirmedFixture(t)
	sourcePath := filepath.Join(directory, "cache.go")
	source := string(readFile(t, sourcePath))
	source = strings.Replace(source, "Revision uint64", "Revision uint64\n\tRegion string", 1)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "key shape or codec changed without a KeyVersion bump") {
		t.Fatalf("key drift error = %v", err)
	}

	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	for index := range document.Caches {
		if document.Caches[index].Variable == "Projections" {
			document.Caches[index].Key.Version++
		}
	}
	writeTestManifest(t, manifestPath, document)
	err = Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) || len(confirmation.Caches) != 1 || !strings.HasSuffix(confirmation.Caches[0], ".Projections") {
		t.Fatalf("versioned key change error = %v, want renewed scope confirmation", err)
	}
	document = readTestManifest(t, manifestPath)
	for index := range document.Caches {
		if document.Caches[index].Variable == "Projections" {
			if document.Caches[index].Key.FingerprintVersion != document.Caches[index].Key.Version || document.Caches[index].Scope.Confirmed {
				t.Fatalf("updated key metadata = %#v", document.Caches[index])
			}
			document.Caches[index].Scope.Confirmed = true
		}
	}
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("generate after renewed confirmation: %v", err)
	}
	goTest(t, directory)
}

func TestUnexportedKeyFieldRequiresManualCodec(t *testing.T) {
	directory := fixturePackage(t, "basic/cache.go.txt")
	sourcePath := filepath.Join(directory, "cache.go")
	source := string(readFile(t, sourcePath))
	source = strings.Replace(source, "TenantID string", "tenantID string", 1)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "field tenantID is unexported") || !strings.Contains(err.Error(), "declare cache.KeyFunc manually") {
		t.Fatalf("private key error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "cache.manifest.yml")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid key wrote a manifest: %v", statErr)
	}
}

func TestProactiveVersionBumpConsumesTheCompatibilityProof(t *testing.T) {
	directory := confirmedFixture(t)
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	for index := range document.Caches {
		if document.Caches[index].Variable == "Projections" {
			document.Caches[index].Key.Version++
		}
	}
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("proactive version bump: %v", err)
	}
	document = readTestManifest(t, manifestPath)
	for _, entry := range document.Caches {
		if entry.Variable == "Projections" && entry.Key.FingerprintVersion != entry.Key.Version {
			t.Fatalf("fingerprint remains anchored to an older version: %#v", entry.Key)
		}
	}
	sourcePath := filepath.Join(directory, "cache.go")
	source := strings.Replace(string(readFile(t, sourcePath)), "Revision uint64", "Revision uint64\n\tRegion string", 1)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "key shape or codec changed without a KeyVersion bump") {
		t.Fatalf("already-consumed version accepted a later key change: %v", err)
	}
}

func TestNamespaceGenerationCannotRollBackAfterAcceptance(t *testing.T) {
	directory := confirmedFixture(t)
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	for index := range document.Caches {
		if document.Caches[index].Variable == "Projections" {
			document.Caches[index].Namespace.Generation = 2
		}
	}
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("accept namespace generation bump: %v", err)
	}
	document = readTestManifest(t, manifestPath)
	for index := range document.Caches {
		if document.Caches[index].Variable == "Projections" {
			if document.Caches[index].Namespace.GenerationAnchor != 2 {
				t.Fatalf("generation anchor was not advanced: %#v", document.Caches[index].Namespace)
			}
			document.Caches[index].Namespace.Generation = 1
		}
	}
	writeTestManifest(t, manifestPath, document)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "namespace generation cannot move below accepted generation 2") {
		t.Fatalf("namespace rollback error = %v", err)
	}
}

func TestDisabledProfileCannotRetainAProviderSelection(t *testing.T) {
	directory := confirmedFixture(t)
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	for index := range document.Caches {
		if document.Caches[index].Variable == "Projections" {
			document.Caches[index].Profile.ProviderID = "primary"
		}
	}
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("generate explicit provider selection: %v", err)
	}
	sourcePath := filepath.Join(directory, "cache.go")
	source := strings.Replace(string(readFile(t, sourcePath)), "cachealias.Hot.With(", "cachealias.Disabled.With(", 1)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestBefore := readFile(t, manifestPath)
	generatedPath := filepath.Join(directory, "vv_cache_gen.go")
	generatedBefore := readFile(t, generatedPath)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "disabled cache cannot select a provider") {
		t.Fatalf("disabled provider error = %v", err)
	}
	if !bytes.Equal(manifestBefore, readFile(t, manifestPath)) || !bytes.Equal(generatedBefore, readFile(t, generatedPath)) {
		t.Fatal("invalid disabled provider selection changed generated artifacts")
	}
}

func TestValueShapeNeedsSchemaBump(t *testing.T) {
	directory := confirmedFixture(t)
	sourcePath := filepath.Join(directory, "cache.go")
	source := strings.Replace(string(readFile(t, sourcePath)), "Name string\n}", "Name string\n\tRevision uint64\n}", 1)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "value shape or codec changed without a ValueSchema bump") {
		t.Fatalf("value drift error = %v", err)
	}
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	for index := range document.Caches {
		if document.Caches[index].Variable == "Globals" {
			document.Caches[index].Value.Schema++
		}
	}
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("generate versioned value change: %v", err)
	}
	document = readTestManifest(t, manifestPath)
	for _, entry := range document.Caches {
		if entry.Variable == "Globals" && entry.Value.FingerprintSchema != entry.Value.Schema {
			t.Fatalf("value fingerprint remains on the old schema: %#v", entry.Value)
		}
	}
	if !bytes.Contains(readFile(t, filepath.Join(directory, "vv_cache_gen.go")), []byte("_vvcache.ValueSchema(2)")) {
		t.Fatal("generated value codec did not use the bumped schema")
	}
}

func TestCompatibilityAnchorsCannotBeEditedAndRehashedInTheManifest(t *testing.T) {
	directory := confirmedFixture(t)
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	for index := range document.Caches {
		if document.Caches[index].Variable == "Projections" {
			document.Caches[index].Key.Fingerprint = "sha256:forged"
		}
		if document.Caches[index].Variable == "Globals" {
			document.Caches[index].Value.FingerprintSchema = 0
		}
	}
	document.CompatibilityProof = manifestCompatibilityProof(document)
	writeTestManifest(t, manifestPath, document)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "does not match the generated Go anchor") {
		t.Fatalf("edited compatibility anchors error = %v", err)
	}
}

func TestKeyCodecIdentityChangesRequireAKeyVersionBump(t *testing.T) {
	base := manifestCache{
		Name:      "example.test/cachefixture.Cache",
		Variable:  "Cache",
		Namespace: manifestNamespace{Purpose: "example.test/cachefixture.Cache", Generation: 1, GenerationAnchor: 1},
		Scope:     manifestScope{Mode: "global", DecisionFingerprint: scopeFingerprint("global", ""), Confirmed: true},
		Key: manifestType{
			Codec:              generatedKeyCodec,
			Algorithm:          generatedKeyAlgorithm,
			AlgorithmRevision:  generatedKeyAlgorithmRevision,
			Fingerprint:        "sha256:key",
			Version:            1,
			FingerprintVersion: 1,
		},
		Value: manifestValue{
			Codec:             "json",
			Algorithm:         jsonValueAlgorithm,
			AlgorithmRevision: valueAlgorithmRevision,
			Fingerprint:       "sha256:value",
			Schema:            1,
			FingerprintSchema: 1,
		},
	}
	tests := map[string]func(*manifestCache){
		"codec":     func(entry *manifestCache) { entry.Key.Codec = "old-codec" },
		"algorithm": func(entry *manifestCache) { entry.Key.Algorithm = "old-algorithm" },
		"revision":  func(entry *manifestCache) { entry.Key.AlgorithmRevision++ },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			prior := base
			change(&prior)
			if _, err := mergeManifestEntry(declaration{}, base, prior); err == nil || !strings.Contains(err.Error(), "KeyVersion bump") {
				t.Fatalf("codec change without bump error = %v", err)
			}
			prior.Key.Version = 2
			merged, err := mergeManifestEntry(declaration{}, base, prior)
			if err != nil || merged.Key.FingerprintVersion != 2 {
				t.Fatalf("codec change with bump = %#v, %v", merged.Key, err)
			}
		})
	}
}

func TestGeneratedOwnershipUsesCodeInsteadOfComments(t *testing.T) {
	directory := confirmedFixture(t)
	generatedPath := filepath.Join(directory, "vv_cache_gen.go")
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	generated := readFile(t, generatedPath)
	commentOpen := []byte{'/', '/'}
	blockOpen := []byte{'/', '*'}
	if bytes.Contains(generated, commentOpen) || bytes.Contains(generated, blockOpen) || !bytes.Contains(generated, []byte("const "+generatedMarkerName)) {
		t.Fatalf("generated ownership marker is not code-native:\n%s", generated)
	}
	lines := strings.Split(string(generated), "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, "const "+generatedMarkerName+" = ") {
			filtered = append(filtered, line)
		}
	}
	if err := os.WriteFile(generatedPath, []byte(strings.Join(filtered, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestBefore := readFile(t, manifestPath)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "without the generated Go anchor") {
		t.Fatalf("missing code-native marker error = %v", err)
	}
	if !bytes.Equal(manifestBefore, readFile(t, manifestPath)) {
		t.Fatal("missing ownership marker allowed a manifest write")
	}
}

func TestGeneratedGoArtifactNameCannotBeIgnoredOrPlatformSpecific(t *testing.T) {
	if err := validateGoArtifactName("vv_cache_gen.go"); err != nil {
		t.Fatalf("ordinary generated name rejected: %v", err)
	}
	for _, name := range []string{"_vv.go", ".vv.go", "vv_test.go", "vv_linux.go", "vv_amd64.go"} {
		if err := validateGoArtifactName(name); err == nil {
			t.Fatalf("unsafe generated file name %q accepted", name)
		}
	}
}

func TestGeneratedArtifactRefusesADifferentBuildTarget(t *testing.T) {
	directory := confirmedFixture(t)
	goos, goarch := alternateBuildTarget(t)
	command := exec.Command("go", "test", "-c", "-o", filepath.Join(t.TempDir(), "cross.test"), ".")
	command.Dir = directory
	command.Env = environmentValue(buildEnvironment(goos, goarch), "CGO_ENABLED", "0")
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("duplicate case")) {
		t.Fatalf("generated artifact accepted target %s/%s: %v\n%s", goos, goarch, err, output)
	}
}

func TestAuthoredGoTargetPreventsEveryWrite(t *testing.T) {
	directory := fixturePackage(t, "basic/cache.go.txt")
	target := filepath.Join(directory, "vv_cache_gen.go")
	if err := os.WriteFile(target, []byte("package sample\n\nconst Authored = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite authored file") {
		t.Fatalf("authored target error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "cache.manifest.yml")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("manifest was written before target validation: %v", statErr)
	}
}

func TestRemovingTheLastAutomaticCacheClearsGeneratedWiring(t *testing.T) {
	directory := confirmedFixture(t)
	source := `package sample

import cachealias "github.com/frostgrove/vv/cache"

var ExplicitFactory = cachealias.New[string, string]
`
	if err := os.WriteFile(filepath.Join(directory, "cache.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("clear removed automatic caches: %v", err)
	}
	document := readTestManifest(t, filepath.Join(directory, "cache.manifest.yml"))
	if len(document.Caches) != 0 {
		t.Fatalf("removed caches remain in manifest: %#v", document.Caches)
	}
	generated := readFile(t, filepath.Join(directory, "vv_cache_gen.go"))
	if !bytes.Contains(generated, []byte("var VVCacheSet = _vvcache.MustSet()")) || bytes.Contains(generated, []byte("MustDefine")) {
		t.Fatalf("removed caches remain in generated wiring:\n%s", generated)
	}
	goTest(t, directory)
}

func TestConfirmedScopeCanOverrideTheInference(t *testing.T) {
	directory := fixturePackage(t, "basic/cache.go.txt")
	err := Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("initial generation: %v", err)
	}
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	for index := range document.Caches {
		document.Caches[index].Scope.Confirmed = true
		if document.Caches[index].Variable == "Projections" {
			document.Caches[index].Scope.Mode = "global"
			document.Caches[index].Scope.PartitionField = ""
		}
	}
	writeTestManifest(t, manifestPath, document)
	err = Run(&Options{Dir: directory})
	if !errors.As(err, &confirmation) {
		t.Fatalf("changed scope did not require a fresh confirmation: %v", err)
	}
	document = readTestManifest(t, manifestPath)
	for index := range document.Caches {
		document.Caches[index].Scope.Confirmed = true
	}
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("generate explicit global override after confirmation: %v", err)
	}
	generated := readFile(t, filepath.Join(directory, "vv_cache_gen.go"))
	if !bytes.Contains(generated, []byte("_vvcache.GlobalPlan[ProjectionKey]()")) || bytes.Contains(generated, []byte("PartitionedPlan")) {
		t.Fatalf("confirmed scope override was ignored:\n%s", generated)
	}
}

func TestTenantLikeFieldsFailClosed(t *testing.T) {
	directory := fixturePackage(t, "basic/cache.go.txt")
	sourcePath := filepath.Join(directory, "cache.go")
	source := string(readFile(t, sourcePath))
	source = strings.Replace(source, "TenantID string", "TenantID string\n\tTenantRegion string", 1)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "multiple tenant-like fields require one cache:\"tenant\" tag") {
		t.Fatalf("ambiguous tenant inference error = %v", err)
	}
}

func TestCacheTagsAreParsedAsExactGoStructTags(t *testing.T) {
	t.Run("other tag value", func(t *testing.T) {
		directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

type Key struct {
	Value string `+"`validate:\"cache:required\"`"+`
}

var Values = cachealias.Auto[Key, string](cachealias.Disabled)
`)
		err := Run(&Options{Dir: directory})
		var confirmation *ConfirmationError
		if !errors.As(err, &confirmation) {
			t.Fatalf("non-cache struct tag affected inference: %v", err)
		}
		document := readTestManifest(t, filepath.Join(directory, "cache.manifest.yml"))
		if document.Caches[0].Scope.InferredMode != "global" {
			t.Fatalf("non-cache struct tag inferred %#v", document.Caches[0].Scope)
		}
	})

	tests := map[string]struct {
		tag      string
		expected string
	}{
		"duplicate key":     {tag: `cache:"tenant" cache:"tenant"`, expected: "duplicate key"},
		"duplicate option":  {tag: `cache:"tenant,tenant"`, expected: "duplicate cache tag option"},
		"malformed spacing": {tag: `cache :"tenant"`, expected: "malformed struct tag"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source := `package sample

import cachealias "github.com/frostgrove/vv/cache"

type Key struct {
	TenantID string ` + "`" + test.tag + "`" + `
}

var Values = cachealias.Auto[Key, string](cachealias.Disabled)
`
			directory := sourcePackage(t, source)
			err := Run(&Options{Dir: directory})
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("invalid cache tag error = %v", err)
			}
		})
	}
}

func TestMutableTenantPartitionNeedsExplicitWiring(t *testing.T) {
	directory := fixturePackage(t, "basic/cache.go.txt")
	sourcePath := filepath.Join(directory, "cache.go")
	source := strings.Replace(string(readFile(t, sourcePath)), "TenantID string", "TenantID *string", 1)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "tenant partition field TenantID") || !strings.Contains(err.Error(), "immutable scalar, array, or struct") {
		t.Fatalf("mutable tenant inference error = %v", err)
	}
}

func TestBlankAutomaticDeclarationIsRejected(t *testing.T) {
	directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

var _ = cachealias.Auto[string, string]()
`)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "must be assigned to a named package-level variable") {
		t.Fatalf("blank cache.Auto declaration error = %v", err)
	}
}

func TestEveryUnsupportedAutomaticReferenceFailsClosed(t *testing.T) {
	tests := map[string]string{
		"dot import": `package sample

import . "github.com/frostgrove/vv/cache"

var Values = Auto[string, string](Disabled)
`,
		"wrapped initializer": `package sample

import cachealias "github.com/frostgrove/vv/cache"

func identity[T any](value T) T { return value }

var Values = identity(cachealias.Auto[string, string](cachealias.Disabled))
`,
		"function body": `package sample

import cachealias "github.com/frostgrove/vv/cache"

func makeCache() *cachealias.Cache[string, string] {
	return cachealias.Auto[string, string](cachealias.Disabled)
}
`,
		"instantiated alias": `package sample

import cachealias "github.com/frostgrove/vv/cache"

var Factory = cachealias.Auto[string, string]
`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			directory := sourcePackage(t, source)
			err := Run(&Options{Dir: directory})
			if err == nil || !strings.Contains(err.Error(), "cache.Auto is only supported as a direct package-level variable initializer") {
				t.Fatalf("unsupported cache.Auto reference error = %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(directory, "cache.manifest.yml")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("unsupported cache.Auto wrote a manifest: %v", statErr)
			}
		})
	}
}

func TestExplicitNewReferenceDoesNotBecomeAnAutomaticDeclaration(t *testing.T) {
	directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

var ExplicitFactory = cachealias.New[string, string]
`)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "no package-level cache.Auto declarations") {
		t.Fatalf("explicit cache.New discovery error = %v", err)
	}
}

func TestSamePackageActivationCanReferenceGeneratedSetOnFirstRun(t *testing.T) {
	directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

var Values = cachealias.Auto[string, string](cachealias.Disabled)
var Activation = VVCacheSet
`)
	err := Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("first generation with same-package activation = %v", err)
	}
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	document.Caches[0].Scope.Confirmed = true
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("confirmed generation with same-package activation = %v", err)
	}
	goTest(t, directory)
}

func TestConditionalSourceFilesAreRejectedForUniversalGeneration(t *testing.T) {
	t.Run("platform suffix", func(t *testing.T) {
		directory := fixturePackage(t, "basic/cache.go.txt")
		conditional := "package sample\n\nconst PlatformSpecific = true\n"
		if err := os.WriteFile(filepath.Join(directory, "conditional_windows.go"), []byte(conditional), 0o644); err != nil {
			t.Fatal(err)
		}
		err := Run(&Options{Dir: directory})
		if err == nil || !strings.Contains(err.Error(), "universal cache generation") {
			t.Fatalf("platform-specific source error = %v", err)
		}
	})

	t.Run("active build constraint", func(t *testing.T) {
		directory := fixturePackage(t, "basic/cache.go.txt")
		conditional := strings.Repeat("/", 2) + "go:build !cachegen_never\n\npackage sample\n\nconst ConditionallyBuilt = true\n"
		if err := os.WriteFile(filepath.Join(directory, "conditional.go"), []byte(conditional), 0o644); err != nil {
			t.Fatal(err)
		}
		err := Run(&Options{Dir: directory})
		if err == nil || !strings.Contains(err.Error(), "conditional Go file") {
			t.Fatalf("build-constrained source error = %v", err)
		}
	})
}

func TestInvalidPackageInventoryPreventsEveryWrite(t *testing.T) {
	directory := fixturePackage(t, "basic/cache.go.txt")
	if err := os.WriteFile(filepath.Join(directory, "conflict.go"), []byte("package conflict\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "found packages") {
		t.Fatalf("mixed package error = %v", err)
	}
	for _, name := range []string{"cache.manifest.yml", "vv_cache_gen.go"} {
		if _, statErr := os.Stat(filepath.Join(directory, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("invalid package wrote %s: %v", name, statErr)
		}
	}
}

func TestQualifierRewriteRespectsIdentifierBoundaries(t *testing.T) {
	got := replaceQualifiers("struct{ A x.Type `json:\"x.Type\"`; B y.Type; C xx.Type }", map[string]string{"x": "y", "y": "_vvcachetype1"})
	want := "struct{ A y.Type `json:\"x.Type\"`; B _vvcachetype1.Type; C xx.Type }"
	if got != want {
		t.Fatalf("qualifier rewrite = %q, want %q", got, want)
	}
}

func TestQualifierCanonicalizationAcrossFilesIsStableAndCompiles(t *testing.T) {
	directory := sourcePackage(t, `package sample

import (
	z "example.test/cachefixture/alpha"
	a "example.test/cachefixture/beta"

	cachealias "github.com/frostgrove/vv/cache"
)

var Mixed = cachealias.Auto[struct {
	Left z.Key
	Right a.Key
}, string](cachealias.Disabled)
`)
	for _, packageName := range []string{"alpha", "beta"} {
		packageDirectory := filepath.Join(directory, packageName)
		if err := os.Mkdir(packageDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		source := "package " + packageName + "\n\ntype Key struct { ID string }\n"
		if err := os.WriteFile(filepath.Join(packageDirectory, "types.go"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	second := `package sample

import (
	a "example.test/cachefixture/alpha"

	cachealias "github.com/frostgrove/vv/cache"
)

var Alpha = cachealias.Auto[a.Key, string](cachealias.Disabled)
`
	if err := os.WriteFile(filepath.Join(directory, "second.go"), []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) || len(confirmation.Caches) != 2 {
		t.Fatalf("cross-file alias generation = %v", err)
	}
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	for index := range document.Caches {
		document.Caches[index].Scope.Confirmed = true
	}
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatal(err)
	}
	generatedPath := filepath.Join(directory, "vv_cache_gen.go")
	generated := readFile(t, generatedPath)
	for _, expected := range []string{`a "example.test/cachefixture/alpha"`, `_vvcachetype1 "example.test/cachefixture/beta"`, "Left  a.Key", "Right _vvcachetype1.Key"} {
		if !bytes.Contains(generated, []byte(expected)) {
			t.Fatalf("cross-file aliases lack %q:\n%s", expected, generated)
		}
	}
	goTest(t, directory)
	for range 20 {
		if err := Run(&Options{Dir: directory}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(generated, readFile(t, generatedPath)) {
			t.Fatal("cross-file alias output is nondeterministic")
		}
	}
}

func TestGeneratedCodeCoversStructuralKeysAndExternalValueTypes(t *testing.T) {
	directory := sourcePackage(t, `package sample

import (
	"time"

	cachealias "github.com/frostgrove/vv/cache"
)

type TextID string

func (value TextID) MarshalText() ([]byte, error) {
	return []byte(value), nil
}

type ComplexKey struct {
	TenantID TextID `+"`cache:\"tenant\"`"+`
	Enabled bool
	Score float64
	Payload []byte
	Values []uint32
	Fixed [2]byte
	Optional *uint64
}

var Complex = cachealias.Auto[ComplexKey, []byte](cachealias.Disabled)
var Moments = cachealias.Auto[string, time.Time](cachealias.Disabled)
`)
	err := Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) || len(confirmation.Caches) != 2 {
		t.Fatalf("initial complex generation = %v", err)
	}
	path := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, path)
	for index := range document.Caches {
		document.Caches[index].Scope.Confirmed = true
	}
	writeTestManifest(t, path, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("generate structural keys: %v", err)
	}
	generated := readFile(t, filepath.Join(directory, "vv_cache_gen.go"))
	for _, expected := range []string{"_vvmath.Float64bits", "for index", "_vvcache.Bytes", "_vvcache.RFC3339UTC", "time.Time"} {
		if !bytes.Contains(generated, []byte(expected)) {
			t.Fatalf("structural generated code lacks %q:\n%s", expected, generated)
		}
	}
	for _, entry := range document.Caches {
		if entry.Variable == "Moments" && (entry.Value.Codec != "time-rfc3339-utc" || entry.Value.Algorithm != rfc3339UTCValueAlgorithm) {
			t.Fatalf("time value codec = %#v", entry.Value)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "time_wiring_test.go"), []byte(`package sample

import "testing"

func TestGeneratedTimeWiring(t *testing.T) {
	if len(VVCacheSet.Describe()) != 2 {
		t.Fatal("generated cache set is incomplete")
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	goTest(t, directory)
}

func TestAutomaticTimeValuesAreRootOnly(t *testing.T) {
	directory := sourcePackage(t, `package sample

import (
	"time"

	cachealias "github.com/frostgrove/vv/cache"
)

type Payload struct { At time.Time }

var Pointer = cachealias.Auto[string, *time.Time](cachealias.Disabled)
var Nested = cachealias.Auto[string, Payload](cachealias.Disabled)
var Slice = cachealias.Auto[string, []time.Time](cachealias.Disabled)
var Mapped = cachealias.Auto[string, map[string]time.Time](cachealias.Disabled)
`)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "time.Time is only supported as the exact top-level value type") {
		t.Fatalf("nested time error = %v", err)
	}
	for _, name := range []string{"Mapped value", "Nested value", "Pointer value", "Slice value"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("nested time error lacks %q: %v", name, err)
		}
	}
}

func TestExternalDTOsRemainEligibleForAutomaticGeneration(t *testing.T) {
	directory := sourcePackage(t, `package sample

import (
	dto "example.test/cachefixture/dto"

	cachealias "github.com/frostgrove/vv/cache"
)

var Values = cachealias.Auto[dto.Key, dto.Value](cachealias.Disabled)
`)
	dtoDirectory := filepath.Join(directory, "dto")
	if err := os.Mkdir(dtoDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	dtoSource := `package dto

type Key struct {
	TenantID string
	Revision uint64
}

type Value struct {
	Name string
}
`
	if err := os.WriteFile(filepath.Join(dtoDirectory, "dto.go"), []byte(dtoSource), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("initial external DTO generation = %v", err)
	}
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	document.Caches[0].Scope.Confirmed = true
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("confirmed external DTO generation = %v", err)
	}
	generated := readFile(t, filepath.Join(directory, "vv_cache_gen.go"))
	if !bytes.Contains(generated, []byte("dto.Key")) || !bytes.Contains(generated, []byte("dto.Value")) {
		t.Fatalf("external DTO types were not preserved:\n%s", generated)
	}
	goTest(t, directory)
}

func TestGeneratedFloatKeyNormalizesSignedZero(t *testing.T) {
	directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

type FloatKey struct {
	Score float64
}

var Values = cachealias.Auto[FloatKey, string](cachealias.Disabled)
`)
	err := Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("initial float-key generation = %v", err)
	}
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	document.Caches[0].Scope.Confirmed = true
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("confirmed float-key generation = %v", err)
	}
	name := encoderName(declaration{logicalName: "example.test/cachefixture.Values"})
	testSource := fmt.Sprintf(`package sample

import (
	"bytes"
	"math"
	"testing"

	cachealias "github.com/frostgrove/vv/cache"
)

func TestSignedZeroKeysMatch(t *testing.T) {
	positive, err := %s(FloatKey{Score: 0}, cachealias.KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	negative, err := %s(FloatKey{Score: math.Copysign(0, -1)}, cachealias.KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(positive, negative) {
		t.Fatal("equal signed zero keys use different encodings")
	}
}
`, name, name)
	if err := os.WriteFile(filepath.Join(directory, "float_key_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	goTest(t, directory)
}

func TestGeneratedKeyCodecDistinguishesNilAndEmptySlices(t *testing.T) {
	directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

type SliceKey struct {
	Payload []byte
}

var Slices = cachealias.Auto[SliceKey, string](cachealias.Disabled)
`)
	err := Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("initial slice-key generation = %v", err)
	}
	manifestPath := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, manifestPath)
	document.Caches[0].Scope.Confirmed = true
	writeTestManifest(t, manifestPath, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("confirmed slice-key generation = %v", err)
	}
	declaration := declaration{logicalName: "example.test/cachefixture.Slices"}
	testSource := fmt.Sprintf(`package sample

import (
	"bytes"
	"testing"

	cachealias "github.com/frostgrove/vv/cache"
)

func TestNilAndEmptySliceKeysDiffer(t *testing.T) {
	nilKey, err := %s(SliceKey{}, cachealias.KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	emptyKey, err := %s(SliceKey{Payload: []byte{}}, cachealias.KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(nilKey, emptyKey) {
		t.Fatal("nil and empty slices collide")
	}
}
`, encoderName(declaration), encoderName(declaration))
	if err := os.WriteFile(filepath.Join(directory, "slice_key_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	goTest(t, directory)
}

func TestAutomaticValuesRejectApplicationDefinedSerialization(t *testing.T) {
	for name, methods := range map[string]string{
		"json marshal value":     `func (Payload) MarshalJSON() ([]byte, error) { return []byte("null"), nil }`,
		"json marshal pointer":   `func (*Payload) MarshalJSON() ([]byte, error) { return []byte("null"), nil }`,
		"json unmarshal value":   `func (Payload) UnmarshalJSON([]byte) error { return nil }`,
		"json unmarshal pointer": `func (*Payload) UnmarshalJSON([]byte) error { return nil }`,
		"text marshal value":     `func (Payload) MarshalText() ([]byte, error) { return []byte("value"), nil }`,
		"text marshal pointer":   `func (*Payload) MarshalText() ([]byte, error) { return []byte("value"), nil }`,
		"text unmarshal value":   `func (Payload) UnmarshalText([]byte) error { return nil }`,
		"text unmarshal pointer": `func (*Payload) UnmarshalText([]byte) error { return nil }`,
	} {
		t.Run(name, func(t *testing.T) {
			directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

type Payload struct { Value string }

`+methods+`

var Custom = cachealias.Auto[string, Payload](cachealias.Disabled)
`)
			err := Run(&Options{Dir: directory})
			if err == nil || !strings.Contains(err.Error(), "custom JSON or text serialization") || !strings.Contains(err.Error(), "cache.Codec manually") {
				t.Fatalf("custom serializer error = %v", err)
			}
		})
	}
	t.Run("reachable nested hook", func(t *testing.T) {
		directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

type Hook string

func (*Hook) UnmarshalText([]byte) error { return nil }

type Payload struct { Value Hook }

var Custom = cachealias.Auto[string, Payload](cachealias.Disabled)
`)
		err := Run(&Options{Dir: directory})
		if err == nil || !strings.Contains(err.Error(), "custom JSON or text serialization") || !strings.Contains(err.Error(), "field Value") {
			t.Fatalf("nested serializer error = %v", err)
		}
	})
}

func TestAutomaticValuesRejectAmbiguousOrHiddenJSONState(t *testing.T) {
	tests := map[string]struct {
		declarations string
		expected     string
	}{
		"duplicate names": {
			declarations: `type Payload struct {
	A string ` + "`json:\"value\"`" + `
	B string ` + "`json:\"value\"`" + `
}`,
			expected: `JSON field name "value" is ambiguous`,
		},
		"embedding conflict": {
			declarations: `type First struct { Value string }
type Second struct { Value string }
type Payload struct {
	First
	Second
}`,
			expected: `JSON field name "Value" is ambiguous`,
		},
		"hidden state": {
			declarations: `type Payload struct {
	Visible string
	hidden string
}`,
			expected: "hidden JSON state",
		},
		"invalid anonymous tag": {
			declarations: `type Inner struct { Value string }
type Payload struct {
	Inner ` + "`json:\"😀\"`" + `
	Value string
}`,
			expected: `JSON field name "😀" is invalid`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

`+test.declarations+`

var Values = cachealias.Auto[string, Payload](cachealias.Disabled)
`)
			err := Run(&Options{Dir: directory})
			if err == nil || !strings.Contains(err.Error(), test.expected) || !strings.Contains(err.Error(), "cache.Codec manually") {
				t.Fatalf("unsafe JSON shape error = %v", err)
			}
		})
	}
}

func TestAutomaticValuesRejectVisibleUnexportedAnonymousPointers(t *testing.T) {
	directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

type hidden struct { Value string }

type Payload struct {
	*hidden `+"`json:\"hidden\"`"+`
}

var Values = cachealias.Auto[string, Payload](cachealias.Disabled)
`)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "JSON-visible unexported anonymous pointer") || !strings.Contains(err.Error(), "cache.Codec manually") {
		t.Fatalf("unexported anonymous pointer error = %v", err)
	}
	for name, field := range map[string]string{
		"ignored unexported": `type hidden struct { Value string }
type Payload struct {
	Visible string
	*hidden ` + "`json:\"-\"`" + `
}`,
		"tagged exported": `type Inner struct { Value string }
type Payload struct {
	*Inner ` + "`json:\"inner\"`" + `
}`,
	} {
		t.Run(name, func(t *testing.T) {
			directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

`+field+`

var Values = cachealias.Auto[string, Payload](cachealias.Disabled)
`)
			err := Run(&Options{Dir: directory})
			var confirmation *ConfirmationError
			if !errors.As(err, &confirmation) || len(confirmation.Caches) != 1 {
				t.Fatalf("safe anonymous pointer generation = %v", err)
			}
		})
	}
}

func TestAutomaticOmitZeroRejectsApplicationDefinedZeroSemantics(t *testing.T) {
	methods := map[string]string{
		"value receiver":   `func (Field) IsZero() bool { return false }`,
		"pointer receiver": `func (*Field) IsZero() bool { return false }`,
	}
	for name, method := range methods {
		t.Run(name, func(t *testing.T) {
			directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

type Field int

`+method+`

type Payload struct {
	Field Field `+"`json:\",omitzero\"`"+`
}

var Values = cachealias.Auto[string, Payload](cachealias.Disabled)
`)
			err := Run(&Options{Dir: directory})
			if err == nil || !strings.Contains(err.Error(), "application-defined IsZero with json omitzero") || !strings.Contains(err.Error(), "cache.Codec manually") {
				t.Fatalf("custom IsZero error = %v", err)
			}
		})
	}
}

func TestSerializationMethodNamesWithWrongSignaturesUseStructuralJSON(t *testing.T) {
	directory := sourcePackage(t, `package sample

import cachealias "github.com/frostgrove/vv/cache"

type Payload struct { Value string }

func (Payload) MarshalJSON() string { return "not-an-interface-method" }

var Values = cachealias.Auto[string, Payload](cachealias.Disabled)
`)
	err := Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("wrong-signature method affected codec discovery: %v", err)
	}
}

func TestAuthoredImportCannotShadowGeneratedDeclarations(t *testing.T) {
	directory := sourcePackage(t, `package sample

import (
	VVCacheSet "time"

	cachealias "github.com/frostgrove/vv/cache"
)

var Durations = cachealias.Auto[VVCacheSet.Duration, string](cachealias.Disabled)
`)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "generated declarations collide with authored code: VVCacheSet") {
		t.Fatalf("authored import collision error = %v", err)
	}
}

func TestAutomaticValuesMustRoundTripEvenWhenDisabled(t *testing.T) {
	directory := sourcePackage(t, `package sample

import (
	"encoding/binary"

	cachealias "github.com/frostgrove/vv/cache"
)

var InterfaceValue = cachealias.Auto[string, binary.ByteOrder](cachealias.Disabled)
var FunctionValue = cachealias.Auto[string, func()](cachealias.Disabled)
`)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "InterfaceValue value") || !strings.Contains(err.Error(), "FunctionValue value") ||
		!strings.Contains(err.Error(), "declare a cache.Codec manually") {
		t.Fatalf("unsupported automatic values error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "cache.manifest.yml")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid values wrote a manifest: %v", statErr)
	}
}

func confirmedFixture(t *testing.T) string {
	t.Helper()
	directory := fixturePackage(t, "basic/cache.go.txt")
	err := Run(&Options{Dir: directory})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("initial generation: %v", err)
	}
	path := filepath.Join(directory, "cache.manifest.yml")
	document := readTestManifest(t, path)
	for index := range document.Caches {
		document.Caches[index].Scope.Confirmed = true
	}
	writeTestManifest(t, path, document)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatalf("confirmed generation: %v", err)
	}
	return directory
}

func fixturePackage(t *testing.T, fixture string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	return sourcePackage(t, string(source))
}

func sourcePackage(t *testing.T, source string) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "cache.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	framework, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module example.test/cachefixture\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => %s\n", framework)
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	return directory
}

func readTestManifest(t *testing.T, path string) manifestDocument {
	t.Helper()
	document, err := readManifest(path, "example.test/cachefixture")
	if err != nil {
		t.Fatal(err)
	}
	if document == nil {
		t.Fatal("manifest is missing")
	}
	return *document
}

func writeTestManifest(t *testing.T, path string, document manifestDocument) {
	t.Helper()
	source, err := marshalManifest(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func goTest(t *testing.T, directory string) {
	t.Helper()
	command := exec.Command("go", "test", "./...")
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated fixture does not compile: %v\n%s", err, output)
	}
}

func goTestJSONActivation(t *testing.T, directory string) {
	t.Helper()
	for _, experiment := range strings.Split(os.Getenv("GOEXPERIMENT"), ",") {
		if experiment == "jsonv2" {
			goTestFails(t, directory, "safe JSON is unavailable with jsonv2")
			return
		}
	}
	goTest(t, directory)
}

func goTestFails(t *testing.T, directory, expected string) {
	t.Helper()
	command := exec.Command("go", "test", "./...")
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte(expected)) {
		t.Fatalf("package with unconfirmed scope did not fail closed: %v\n%s", err, output)
	}
}

func alternateBuildTarget(t *testing.T) (string, string) {
	t.Helper()
	command := exec.Command("go", "tool", "dist", "list")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range strings.Fields(string(output)) {
		parts := strings.Split(target, "/")
		if len(parts) == 2 && parts[0] == runtime.GOOS && parts[1] != runtime.GOARCH {
			return parts[0], parts[1]
		}
	}
	for _, target := range strings.Fields(string(output)) {
		parts := strings.Split(target, "/")
		if len(parts) == 2 && parts[0] != runtime.GOOS && parts[1] == runtime.GOARCH {
			return parts[0], parts[1]
		}
	}
	t.Fatal("no alternate Go build target")
	return "", ""
}

func environmentValue(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func writeGeneratedBehaviorTest(t *testing.T, directory string) {
	t.Helper()
	declaration := declaration{logicalName: "example.test/cachefixture.Projections"}
	source := fmt.Sprintf(`package sample

import (
	"bytes"
	"errors"
	"testing"

	cachealias "github.com/frostgrove/vv/cache"
)

func TestGeneratedKeySemantics(t *testing.T) {
	first, err := %s(ProjectionKey{TenantID: "tenant-a", Revision: 1}, cachealias.KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	second, err := %s(ProjectionKey{TenantID: "tenant-a", Revision: 2}, cachealias.KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("different typed keys have the same encoding")
	}
	if _, err := %s(ProjectionKey{TenantID: "tenant-a", Revision: 1}, cachealias.KeyLimit{MaxBytes: 1}); !errors.Is(err, cachealias.ErrTooLarge) {
		t.Fatalf("bounded key error = %%v", err)
	}
	partitionA, err := %s(ProjectionKey{TenantID: "tenant-a", Revision: 1}, cachealias.KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	partitionSame, err := %s(ProjectionKey{TenantID: "tenant-a", Revision: 999}, cachealias.KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	partitionB, err := %s(ProjectionKey{TenantID: "tenant-b", Revision: 1}, cachealias.KeyLimit{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(partitionA, partitionSame) || bytes.Equal(partitionA, partitionB) {
		t.Fatal("generated partitioner does not isolate the confirmed tenant field")
	}
	if _, err := %s(ProjectionKey{Revision: 1}, cachealias.KeyLimit{MaxBytes: 1024}); !errors.Is(err, cachealias.ErrInvalid) {
		t.Fatalf("zero tenant partition error = %%v", err)
	}
}
`, encoderName(declaration), encoderName(declaration), encoderName(declaration), partitionerName(declaration), partitionerName(declaration), partitionerName(declaration), partitionerName(declaration))
	if err := os.WriteFile(filepath.Join(directory, "generated_behavior_test.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}
