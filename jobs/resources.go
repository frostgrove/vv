package jobs

import (
	"fmt"
	"log/slog"
)

const MaxResourceUnits = 1 << 20

type ResourcesSpec struct {
	PinnedConnections      int
	MaxConcurrentDBOps     int
	MaxConcurrentRemoteOps int
}

type Resources struct {
	pinnedConnections      int
	maxConcurrentDBOps     int
	maxConcurrentRemoteOps int
	complete               bool
}

func NewResources(spec ResourcesSpec) (Resources, error) {
	resources := Resources{
		pinnedConnections:      spec.PinnedConnections,
		maxConcurrentDBOps:     spec.MaxConcurrentDBOps,
		maxConcurrentRemoteOps: spec.MaxConcurrentRemoteOps,
		complete:               true,
	}
	if err := validateResourceValues(resources); err != nil {
		return Resources{}, err
	}
	return resources, nil
}

func (resources Resources) PinnedConnections() int      { return resources.pinnedConnections }
func (resources Resources) MaxConcurrentDBOps() int     { return resources.maxConcurrentDBOps }
func (resources Resources) MaxConcurrentRemoteOps() int { return resources.maxConcurrentRemoteOps }
func (resources Resources) IsComplete() bool            { return resources.complete }
func (resources Resources) IsEmpty() bool {
	return resources.complete && resources.pinnedConnections == 0 && resources.maxConcurrentDBOps == 0 && resources.maxConcurrentRemoteOps == 0
}
func (resources Resources) String() string {
	return fmt.Sprintf("[job resources complete=%t pinned-connections=%d max-concurrent-db-ops=%d max-concurrent-remote-ops=%d]", resources.complete, resources.pinnedConnections, resources.maxConcurrentDBOps, resources.maxConcurrentRemoteOps)
}
func (resources Resources) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, resources.String())
}
func (resources Resources) LogValue() slog.Value { return slog.StringValue(resources.String()) }
func (resources Resources) valid() bool          { return validateResourceValues(resources) == nil }

type ResourceProfileSpec struct {
	SteadyBase ResourcesSpec
	PerWorker  ResourcesSpec
	Lifecycle  ResourcesSpec
}

type ResourceProfile struct {
	steadyBase Resources
	perWorker  Resources
	lifecycle  Resources
	declared   bool
}

func NewResourceProfile(spec ResourceProfileSpec) (ResourceProfile, error) {
	steadyBase, err := NewResources(spec.SteadyBase)
	if err != nil {
		return ResourceProfile{}, fmt.Errorf("steady resource base: %w", err)
	}
	perWorker, err := NewResources(spec.PerWorker)
	if err != nil {
		return ResourceProfile{}, fmt.Errorf("per-worker resources: %w", err)
	}
	lifecycle, err := NewResources(spec.Lifecycle)
	if err != nil {
		return ResourceProfile{}, fmt.Errorf("lifecycle resources: %w", err)
	}
	return ResourceProfile{steadyBase: steadyBase, perWorker: perWorker, lifecycle: lifecycle, declared: true}, nil
}

func (profile ResourceProfile) SteadyBase() Resources { return profile.steadyBase }
func (profile ResourceProfile) PerWorker() Resources  { return profile.perWorker }
func (profile ResourceProfile) Lifecycle() Resources  { return profile.lifecycle }
func (profile ResourceProfile) IsDeclared() bool      { return profile.declared }
func (profile ResourceProfile) Resolve(workerConcurrency int) (Resources, error) {
	if workerConcurrency < 1 {
		return Resources{}, invalid("resource profile worker concurrency")
	}
	if workerConcurrency > MaxWorkerConcurrency {
		return Resources{}, tooLarge("resource profile worker concurrency")
	}
	if !profile.valid() {
		return Resources{}, invalid("resource profile")
	}
	if !profile.declared {
		return Resources{}, nil
	}
	return resolveResources(profile.steadyBase, profile.perWorker, workerConcurrency)
}
func (profile ResourceProfile) String() string {
	return fmt.Sprintf("[job resource profile declared=%t]", profile.declared)
}
func (profile ResourceProfile) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, profile.String())
}
func (profile ResourceProfile) LogValue() slog.Value { return slog.StringValue(profile.String()) }
func (profile ResourceProfile) valid() bool {
	if !profile.declared {
		return profile == ResourceProfile{}
	}
	return profile.steadyBase.complete && profile.perWorker.complete && profile.lifecycle.complete && profile.steadyBase.valid() && profile.perWorker.valid() && profile.lifecycle.valid()
}

func SumResources(values ...Resources) (Resources, error) {
	result := Resources{complete: true}
	for index, value := range values {
		if !value.valid() {
			return Resources{}, fmt.Errorf("resource set %d: %w", index, ErrInvalid)
		}
		var err error
		result.pinnedConnections, err = addResourceValue(result.pinnedConnections, value.pinnedConnections)
		if err != nil {
			return Resources{}, err
		}
		result.maxConcurrentDBOps, err = addResourceValue(result.maxConcurrentDBOps, value.maxConcurrentDBOps)
		if err != nil {
			return Resources{}, err
		}
		result.maxConcurrentRemoteOps, err = addResourceValue(result.maxConcurrentRemoteOps, value.maxConcurrentRemoteOps)
		if err != nil {
			return Resources{}, err
		}
		result.complete = result.complete && value.complete
	}
	return result, nil
}

func resolveResources(base Resources, perWorker Resources, concurrency int) (Resources, error) {
	pinned, err := resolveResourceValue(base.pinnedConnections, perWorker.pinnedConnections, concurrency)
	if err != nil {
		return Resources{}, err
	}
	database, err := resolveResourceValue(base.maxConcurrentDBOps, perWorker.maxConcurrentDBOps, concurrency)
	if err != nil {
		return Resources{}, err
	}
	remote, err := resolveResourceValue(base.maxConcurrentRemoteOps, perWorker.maxConcurrentRemoteOps, concurrency)
	if err != nil {
		return Resources{}, err
	}
	return Resources{pinnedConnections: pinned, maxConcurrentDBOps: database, maxConcurrentRemoteOps: remote, complete: true}, nil
}

func resolveResourceValue(base, perWorker, concurrency int) (int, error) {
	if perWorker > (MaxResourceUnits-base)/concurrency {
		return 0, tooLarge("resolved resources")
	}
	return base + perWorker*concurrency, nil
}

func addResourceValue(left, right int) (int, error) {
	if right > MaxResourceUnits-left {
		return 0, tooLarge("summed resources")
	}
	return left + right, nil
}

func validateResourceValues(resources Resources) error {
	values := [...]int{resources.pinnedConnections, resources.maxConcurrentDBOps, resources.maxConcurrentRemoteOps}
	for _, value := range values {
		if value < 0 {
			return invalid("resources")
		}
		if value > MaxResourceUnits {
			return tooLarge("resources")
		}
	}
	return nil
}
