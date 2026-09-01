package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
)

const workerAdmissionGroupPrefix = "sha256:"

type WorkerAdmissionGroup struct{ value string }

func (group WorkerAdmissionGroup) Value() string  { return group.value }
func (group WorkerAdmissionGroup) String() string { return group.value }
func (group WorkerAdmissionGroup) IsZero() bool   { return group.value == "" }
func (group WorkerAdmissionGroup) valid() bool {
	if len(group.value) != len(workerAdmissionGroupPrefix)+sha256.Size*2 || group.value[:len(workerAdmissionGroupPrefix)] != workerAdmissionGroupPrefix {
		return false
	}
	raw := group.value[len(workerAdmissionGroupPrefix):]
	decoded, err := hex.DecodeString(raw)
	return err == nil && hex.EncodeToString(decoded) == raw
}

type WorkerOption interface{ applyWorker(*workerOptions) error }

type workerOption func(*workerOptions) error

func (option workerOption) applyWorker(options *workerOptions) error { return option(options) }

type workerOptions struct {
	binding        BindingName
	concurrency    int
	classifier     ErrorClassifier
	admission      AdmissionReader
	bindingSet     bool
	concurrencySet bool
	classifierSet  bool
	admissionSet   bool
}

func Binding(raw string) WorkerOption {
	value, valueErr := ParseBindingName(raw)
	return workerOption(func(options *workerOptions) error {
		if options.bindingSet {
			return invalid("duplicate worker binding option")
		}
		if valueErr != nil {
			return valueErr
		}
		options.binding = value
		options.bindingSet = true
		return nil
	})
}

func Concurrency(value int) WorkerOption {
	return workerOption(func(options *workerOptions) error {
		if options.concurrencySet || value < 1 || value > MaxBindingConcurrency {
			return invalid("worker concurrency")
		}
		options.concurrency = value
		options.concurrencySet = true
		return nil
	})
}

func WithAdmission(reader AdmissionReader) WorkerOption {
	return workerOption(func(options *workerOptions) error {
		if options.admissionSet || !validConfiguredAdmissionReader(reader) {
			return invalid("worker admission")
		}
		options.admission = reader
		options.admissionSet = true
		return nil
	})
}

type WorkerBindingDescription struct {
	Definition       Name
	Binding          BindingName
	AdmissionGroup   WorkerAdmissionGroup
	Concurrency      int
	Adapter          bool
	CustomClassifier bool
	DynamicAdmission bool
}

func (WorkerBindingDescription) String() string { return "[job worker binding description]" }
func (description WorkerBindingDescription) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, description.String())
}
func (description WorkerBindingDescription) LogValue() slog.Value {
	return slog.StringValue(description.String())
}

type WorkerPlanDescription struct {
	Bindings           []WorkerBindingDescription
	TotalConcurrency   int
	CatalogFingerprint string
}

func (description WorkerPlanDescription) String() string {
	return fmt.Sprintf("[job worker plan description bindings=%d concurrency=%d]", len(description.Bindings), description.TotalConcurrency)
}
func (description WorkerPlanDescription) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, description.String())
}
func (description WorkerPlanDescription) LogValue() slog.Value {
	return slog.StringValue(description.String())
}

type WorkerPlan struct {
	bindings           []consumerBinding
	descriptions       []WorkerBindingDescription
	totalConcurrency   int
	catalogFingerprint string
}

func NewWorkerPlan(catalog Catalog, consumers ...Consumer) (WorkerPlan, error) {
	if catalog.Len() == 0 || catalog.Fingerprint() == "" {
		return WorkerPlan{}, invalid("worker catalog")
	}
	if len(consumers) == 0 {
		return WorkerPlan{}, invalid("worker bindings")
	}
	if len(consumers) > MaxDefinitions {
		return WorkerPlan{}, tooLarge("worker bindings")
	}
	bindings := make([]consumerBinding, 0, len(consumers))
	definitions := make(map[Declaration]struct{}, len(consumers))
	names := make(map[BindingName]struct{}, len(consumers))
	total := 0
	for index, consumer := range consumers {
		if nilInterface(consumer) {
			return WorkerPlan{}, fmt.Errorf("%w: worker binding %d is nil", ErrInvalid, index)
		}
		binding := consumer.consumerBinding()
		if binding.err != nil {
			return WorkerPlan{}, fmt.Errorf("worker binding %d: %w", index, binding.err)
		}
		if !binding.valid || nilInterface(binding.declaration) || binding.decode == nil || binding.decodeOwned == nil || !binding.binding.valid() || binding.concurrency < 1 || binding.concurrency > MaxBindingConcurrency || !validOptionalAdmissionReader(binding.admission) || !binding.mode.valid() || binding.mode == consumerHandlerStandard && (binding.handle == nil || binding.handleAdapter != nil) || binding.mode == consumerHandlerAdapter && (binding.handle != nil || binding.handleAdapter == nil) {
			return WorkerPlan{}, fmt.Errorf("%w: worker binding %d is invalid or unresolved", ErrInvalid, index)
		}
		registered, ok := catalog.Lookup(binding.declaration.declarationName())
		if !ok || registered != binding.declaration {
			return WorkerPlan{}, fmt.Errorf("%w: worker binding %d definition is not an exact catalog member", ErrInvalid, index)
		}
		if _, exists := definitions[binding.declaration]; exists {
			return WorkerPlan{}, fmt.Errorf("%w: duplicate worker definition %q", ErrConflict, binding.declaration.declarationName())
		}
		if _, exists := names[binding.binding]; exists {
			return WorkerPlan{}, fmt.Errorf("%w: duplicate worker binding %q", ErrConflict, binding.binding)
		}
		if binding.concurrency > MaxWorkerConcurrency-total {
			return WorkerPlan{}, tooLarge("worker concurrency")
		}
		total += binding.concurrency
		definitions[binding.declaration] = struct{}{}
		names[binding.binding] = struct{}{}
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(left, right int) bool {
		return bindings[left].declaration.declarationName().String() < bindings[right].declaration.declarationName().String()
	})
	descriptions := make([]WorkerBindingDescription, len(bindings))
	groupMembers := make(map[*admissionCell][]int)
	for index := range bindings {
		if bindings[index].admission.initialized {
			groupMembers[bindings[index].admission.cell] = append(groupMembers[bindings[index].admission.cell], index)
		}
	}
	for _, members := range groupMembers {
		group := newWorkerAdmissionGroup(bindings, members)
		for _, index := range members {
			bindings[index].admissionGroup = group
		}
	}
	for index, binding := range bindings {
		descriptions[index] = WorkerBindingDescription{
			Definition:       binding.declaration.declarationName(),
			Binding:          binding.binding,
			AdmissionGroup:   binding.admissionGroup,
			Concurrency:      binding.concurrency,
			Adapter:          binding.mode == consumerHandlerAdapter,
			CustomClassifier: binding.classifier != nil,
			DynamicAdmission: binding.admission.initialized,
		}
	}
	return WorkerPlan{bindings: bindings, descriptions: descriptions, totalConcurrency: total, catalogFingerprint: catalog.Fingerprint()}, nil
}

func newWorkerAdmissionGroup(bindings []consumerBinding, members []int) WorkerAdmissionGroup {
	digest := sha256.New()
	writeFingerprintString(digest, "frostgrove.jobs.worker-admission-group.v1")
	writeFingerprintUint(digest, uint64(len(members)))
	for _, index := range members {
		writeFingerprintString(digest, bindings[index].declaration.declarationName().Value())
		writeFingerprintString(digest, bindings[index].binding.Value())
	}
	return WorkerAdmissionGroup{value: workerAdmissionGroupPrefix + hex.EncodeToString(digest.Sum(nil))}
}

func MustWorkerPlan(catalog Catalog, consumers ...Consumer) WorkerPlan {
	plan, err := NewWorkerPlan(catalog, consumers...)
	if err != nil {
		panic(err)
	}
	return plan
}

func (plan WorkerPlan) Len() int { return len(plan.bindings) }

func (plan WorkerPlan) TotalConcurrency() int { return plan.totalConcurrency }

func (plan WorkerPlan) CatalogFingerprint() string { return plan.catalogFingerprint }

func (plan WorkerPlan) Describe() WorkerPlanDescription {
	return WorkerPlanDescription{
		Bindings:           append([]WorkerBindingDescription(nil), plan.descriptions...),
		TotalConcurrency:   plan.totalConcurrency,
		CatalogFingerprint: plan.catalogFingerprint,
	}
}

func (plan WorkerPlan) workerBindings() []consumerBinding {
	return append([]consumerBinding(nil), plan.bindings...)
}

func (plan WorkerPlan) String() string {
	return fmt.Sprintf("[job worker plan bindings=%d concurrency=%d]", plan.Len(), plan.TotalConcurrency())
}
func (plan WorkerPlan) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, plan.String())
}
func (plan WorkerPlan) LogValue() slog.Value { return slog.StringValue(plan.String()) }

type resolvedWorkerOptions struct {
	binding     BindingName
	concurrency int
	classifier  ErrorClassifier
	admission   AdmissionReader
}

func resolveWorkerOptions(declaration Declaration, defaultConcurrency int, values []WorkerOption) (resolvedWorkerOptions, error) {
	if nilInterface(declaration) || !declaration.declarationName().valid() {
		return resolvedWorkerOptions{}, invalid("worker definition")
	}
	var options workerOptions
	for index, value := range values {
		if nilInterface(value) {
			return resolvedWorkerOptions{}, fmt.Errorf("%w: worker option %d is nil", ErrInvalid, index)
		}
		if err := value.applyWorker(&options); err != nil {
			return resolvedWorkerOptions{}, fmt.Errorf("worker option %d: %w", index, err)
		}
	}
	if !options.bindingSet {
		binding, err := ParseBindingName(declaration.declarationName().String())
		if err != nil {
			return resolvedWorkerOptions{}, err
		}
		options.binding = binding
	}
	if !options.concurrencySet {
		if defaultConcurrency < 1 || defaultConcurrency > MaxBindingConcurrency {
			return resolvedWorkerOptions{}, invalid("explicit worker concurrency is required")
		}
		options.concurrency = defaultConcurrency
	}
	return resolvedWorkerOptions{binding: options.binding, concurrency: options.concurrency, classifier: options.classifier, admission: options.admission}, nil
}

func validConfiguredAdmissionReader(reader AdmissionReader) bool {
	return reader.initialized && reader.cell != nil && reader.freshness > 0 && reader.freshness <= MaximumAdmissionFreshness
}

func validOptionalAdmissionReader(reader AdmissionReader) bool {
	return !reader.initialized && reader.cell == nil && reader.freshness == 0 || validConfiguredAdmissionReader(reader)
}
