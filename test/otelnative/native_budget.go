package otelnative

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
)

var (
	ErrInvalidNativeBudget  = errors.New("otelnative: invalid native metric budget")
	ErrNativeBudgetOverflow = errors.New("otelnative: native metric budget overflows uint64")
)

type NativeBudgetManifest struct {
	Version     int                           `json:"version"`
	SemconvMode NativeBudgetSemconvMode       `json:"semconv_mode"`
	Domains     map[string]NativeBudgetDomain `json:"domains"`
	Resources   []NativeBudgetResource        `json:"resources"`
	Scopes      []NativeBudgetScope           `json:"scopes"`
	Instruments []NativeBudgetInstrument      `json:"instruments"`
}

type NativeBudgetSemconvMode struct {
	Environment string `json:"environment"`
	Unset       bool   `json:"unset"`
}

type NativeBudgetDomain struct {
	Type         string                  `json:"type"`
	StringValues []string                `json:"string_values,omitempty"`
	Int64Range   *NativeBudgetInt64Range `json:"int64_range,omitempty"`
}

type NativeBudgetInt64Range struct {
	Minimum int64 `json:"minimum"`
	Maximum int64 `json:"maximum"`
}

type NativeBudgetResource struct {
	ID         string            `json:"id"`
	SchemaURL  string            `json:"schema_url"`
	Attributes map[string]string `json:"attributes"`
}

type NativeBudgetScope struct {
	ID            string            `json:"id"`
	Module        string            `json:"module"`
	ModuleVersion string            `json:"module_version"`
	Name          string            `json:"name"`
	Version       string            `json:"version"`
	SchemaURL     string            `json:"schema_url"`
	Attributes    map[string]string `json:"attributes"`
}

type NativeBudgetInstrument struct {
	Resource      string                  `json:"resource"`
	Scope         string                  `json:"scope"`
	Name          string                  `json:"name"`
	Type          string                  `json:"type"`
	Unit          string                  `json:"unit"`
	Temporality   string                  `json:"temporality,omitempty"`
	Monotonic     *bool                   `json:"monotonic,omitempty"`
	Attributes    []NativeBudgetAttribute `json:"attributes"`
	SeriesCeiling uint64                  `json:"series_ceiling"`
}

type NativeBudgetAttribute struct {
	Key         string `json:"key"`
	Domain      string `json:"domain"`
	AllowAbsent bool   `json:"allow_absent"`
}

func LoadNativeBudgetManifest(path string) (NativeBudgetManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return NativeBudgetManifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var manifest NativeBudgetManifest
	if err = decoder.Decode(&manifest); err != nil {
		return NativeBudgetManifest{}, fmt.Errorf("%w: %v", ErrInvalidNativeBudget, err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return NativeBudgetManifest{}, fmt.Errorf("%w: trailing JSON", ErrInvalidNativeBudget)
	}
	if err = manifest.Validate(); err != nil {
		return NativeBudgetManifest{}, err
	}
	return manifest, nil
}

func (m NativeBudgetManifest) Validate() error {
	if m.Version != 1 {
		return nativeBudgetError("version must be 1")
	}
	if m.SemconvMode.Environment != "OTEL_SEMCONV_STABILITY_OPT_IN" || !m.SemconvMode.Unset {
		return nativeBudgetError("default semconv mode must be an unset OTEL_SEMCONV_STABILITY_OPT_IN")
	}
	if len(m.Domains) == 0 || len(m.Resources) == 0 || len(m.Scopes) == 0 || len(m.Instruments) == 0 {
		return nativeBudgetError("domains, resources, scopes and instruments are required")
	}
	for name, domain := range m.Domains {
		if name == "" {
			return nativeBudgetError("domain name is empty")
		}
		if _, err := nativeDomainCardinality(domain); err != nil {
			return nativeBudgetError("domain %q: %v", name, err)
		}
	}
	resources := make(map[string]NativeBudgetResource, len(m.Resources))
	resourceTuples := make(map[string]string, len(m.Resources))
	for _, resource := range m.Resources {
		if resource.ID == "" || len(resource.Attributes) == 0 {
			return nativeBudgetError("resource ID and attributes are required")
		}
		if _, exists := resources[resource.ID]; exists {
			return nativeBudgetError("duplicate resource ID %q", resource.ID)
		}
		tuple := nativeStringTuple(resource.SchemaURL, resource.Attributes)
		if prior, exists := resourceTuples[tuple]; exists {
			return nativeBudgetError("resources %q and %q have the same tuple", prior, resource.ID)
		}
		resources[resource.ID] = resource
		resourceTuples[tuple] = resource.ID
	}
	scopes := make(map[string]NativeBudgetScope, len(m.Scopes))
	scopeTuples := make(map[string]string, len(m.Scopes))
	for _, scope := range m.Scopes {
		if scope.ID == "" || scope.Module == "" || scope.ModuleVersion == "" || scope.Name == "" || scope.Version == "" {
			return nativeBudgetError("scope ID, module pin, name and version are required")
		}
		if _, exists := scopes[scope.ID]; exists {
			return nativeBudgetError("duplicate scope ID %q", scope.ID)
		}
		tuple := nativeStringTuple(scope.Name+"\x00"+scope.Version+"\x00"+scope.SchemaURL, scope.Attributes)
		if prior, exists := scopeTuples[tuple]; exists {
			return nativeBudgetError("scopes %q and %q have the same tuple", prior, scope.ID)
		}
		scopes[scope.ID] = scope
		scopeTuples[tuple] = scope.ID
	}
	instruments := make(map[string]struct{}, len(m.Instruments))
	for _, instrument := range m.Instruments {
		if _, exists := resources[instrument.Resource]; !exists {
			return nativeBudgetError("instrument %q has unknown resource %q", instrument.Name, instrument.Resource)
		}
		if _, exists := scopes[instrument.Scope]; !exists {
			return nativeBudgetError("instrument %q has unknown scope %q", instrument.Name, instrument.Scope)
		}
		if instrument.Name == "" || instrument.SeriesCeiling == 0 {
			return nativeBudgetError("instrument name and positive ceiling are required")
		}
		if err := validateNativeMetricShape(instrument); err != nil {
			return nativeBudgetError("instrument %q: %v", instrument.Name, err)
		}
		attributeKeys := make(map[string]struct{}, len(instrument.Attributes))
		for _, item := range instrument.Attributes {
			if item.Key == "" || item.Domain == "" {
				return nativeBudgetError("instrument %q has an incomplete attribute", instrument.Name)
			}
			if _, exists := attributeKeys[item.Key]; exists {
				return nativeBudgetError("instrument %q duplicates attribute %q", instrument.Name, item.Key)
			}
			if _, exists := m.Domains[item.Domain]; !exists {
				return nativeBudgetError("instrument %q references unknown domain %q", instrument.Name, item.Domain)
			}
			attributeKeys[item.Key] = struct{}{}
		}
		key := nativeInstrumentKey(instrument.Resource, instrument.Scope, instrument.Name)
		if _, exists := instruments[key]; exists {
			return nativeBudgetError("duplicate instrument tuple %q", key)
		}
		calculated, err := m.CalculateSeriesCeiling(instrument)
		if err != nil {
			return nativeBudgetError("instrument %q: %v", instrument.Name, err)
		}
		if calculated > instrument.SeriesCeiling {
			return nativeBudgetError("instrument %q domain product %d exceeds ceiling %d", instrument.Name, calculated, instrument.SeriesCeiling)
		}
		instruments[key] = struct{}{}
	}
	return nil
}

func (m NativeBudgetManifest) ValidateEnvironment() error {
	if err := m.Validate(); err != nil {
		return err
	}
	if _, present := os.LookupEnv(m.SemconvMode.Environment); present {
		return nativeBudgetError("%s must be unset", m.SemconvMode.Environment)
	}
	return nil
}

func (m NativeBudgetManifest) CalculateSeriesCeiling(instrument NativeBudgetInstrument) (uint64, error) {
	product := uint64(1)
	for _, item := range instrument.Attributes {
		domain, exists := m.Domains[item.Domain]
		if !exists {
			return 0, fmt.Errorf("unknown domain %q", item.Domain)
		}
		cardinality, err := nativeDomainCardinality(domain)
		if err != nil {
			return 0, err
		}
		if item.AllowAbsent {
			if cardinality == math.MaxUint64 {
				return 0, ErrNativeBudgetOverflow
			}
			cardinality++
		}
		if cardinality == 0 || product > math.MaxUint64/cardinality {
			return 0, ErrNativeBudgetOverflow
		}
		product *= cardinality
	}
	return product, nil
}

func nativeDomainCardinality(domain NativeBudgetDomain) (uint64, error) {
	switch domain.Type {
	case "string":
		if len(domain.StringValues) == 0 || domain.Int64Range != nil {
			return 0, errors.New("string domain needs only string_values")
		}
		seen := make(map[string]struct{}, len(domain.StringValues))
		for _, value := range domain.StringValues {
			if value == "" {
				return 0, errors.New("string domain contains an empty value")
			}
			if _, exists := seen[value]; exists {
				return 0, fmt.Errorf("duplicate value %q", value)
			}
			seen[value] = struct{}{}
		}
		return uint64(len(domain.StringValues)), nil
	case "int64":
		if len(domain.StringValues) != 0 || domain.Int64Range == nil || domain.Int64Range.Minimum < 0 || domain.Int64Range.Maximum < domain.Int64Range.Minimum {
			return 0, errors.New("int64 domain needs one non-negative ordered range")
		}
		return uint64(domain.Int64Range.Maximum-domain.Int64Range.Minimum) + 1, nil
	default:
		return 0, fmt.Errorf("unsupported domain type %q", domain.Type)
	}
}

func validateNativeMetricShape(instrument NativeBudgetInstrument) error {
	switch instrument.Type {
	case "gauge_int64", "gauge_float64":
		if instrument.Temporality != "" || instrument.Monotonic != nil {
			return errors.New("gauge cannot declare temporality or monotonicity")
		}
	case "sum_int64", "sum_float64":
		if instrument.Temporality != "cumulative" || instrument.Monotonic == nil {
			return errors.New("sum needs cumulative temporality and monotonicity")
		}
	case "histogram_int64", "histogram_float64":
		if instrument.Temporality != "cumulative" || instrument.Monotonic != nil {
			return errors.New("histogram needs cumulative temporality and no monotonicity")
		}
	default:
		return fmt.Errorf("unsupported metric type %q", instrument.Type)
	}
	return nil
}

func nativeBudgetError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidNativeBudget, fmt.Sprintf(format, args...))
}

func nativeStringTuple(prefix string, values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(prefix)
	for _, key := range keys {
		builder.WriteByte(0)
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(values[key])
	}
	return builder.String()
}

func nativeInstrumentKey(resource, scope, name string) string {
	return resource + "\x00" + scope + "\x00" + name
}
