package health

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultTimeout   = 2 * time.Second
	DefaultFreshness = time.Second
)

type Spec struct {
	Contributions []Contribution

	Timeout time.Duration

	Freshness time.Duration

	Now func() time.Time
}

type Registry struct {
	contributions []Contribution
	freshness     time.Duration
	now           func() time.Time

	mutex   sync.Mutex
	last    *evaluation
	running *evaluation
}

func New(spec Spec) (*Registry, error) {
	timeout := spec.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	freshness := spec.Freshness
	if freshness == 0 {
		freshness = DefaultFreshness
	}
	now := spec.Now
	if now == nil {
		now = time.Now
	}

	contributions, err := accept(spec.Contributions, timeout, freshness)
	if err != nil {
		return nil, err
	}
	return &Registry{contributions: contributions, freshness: freshness, now: now}, nil
}

func Auto(contributions ...Contribution) (*Registry, error) {
	return New(Spec{Contributions: contributions})
}

func accept(contributions []Contribution, timeout, freshness time.Duration) ([]Contribution, error) {
	var problems []string
	if timeout < 0 {
		problems = append(problems, fmt.Sprintf("the check timeout is negative (%s)", timeout))
	}
	if freshness < 0 {
		problems = append(problems, fmt.Sprintf("the freshness window is negative (%s)", freshness))
	}

	accepted := make([]Contribution, 0, len(contributions))
	seen := make(map[string]struct{}, len(contributions))
	codes := make(map[string]string, len(contributions))

	for position, contribution := range contributions {
		where := contribution.Name
		if where == "" {
			where = fmt.Sprintf("contribution %d", position)
		}
		if contribution.Name == "" {
			problems = append(problems, fmt.Sprintf("%s has no name", where))
		} else if _, duplicate := seen[contribution.Name]; duplicate {
			problems = append(problems, fmt.Sprintf("%s is contributed twice", where))
			continue
		} else {
			seen[contribution.Name] = struct{}{}
		}

		if !contribution.Importance.Known() {
			problems = append(problems, fmt.Sprintf(
				"%s has importance %q, which is not one of required, degrading, informational or disabled",
				where, contribution.Importance))
		}
		if contribution.Probe == nil && contribution.Importance != Disabled {
			problems = append(problems, fmt.Sprintf("%s has no probe", where))
		}
		if contribution.Timeout < 0 {
			problems = append(problems, fmt.Sprintf("%s has a negative timeout (%s)", where, contribution.Timeout))
		}
		if contribution.Code != "" {
			if owner, taken := codes[contribution.Code]; taken {
				problems = append(problems, fmt.Sprintf(
					"%s publishes code %q, which %s already publishes", where, contribution.Code, owner))
			}
			codes[contribution.Code] = where
		}

		if contribution.Timeout == 0 {
			contribution.Timeout = timeout
		}
		accepted = append(accepted, contribution)
	}

	if len(problems) > 0 {
		return nil, &RegistrationError{problems: problems}
	}
	slices.SortStableFunc(accepted, func(a, b Contribution) int { return strings.Compare(a.Name, b.Name) })
	return accepted, nil
}

var ErrRegistration = errors.New("health: the registered checks are not usable")

type RegistrationError struct {
	problems []string
}

func (this *RegistrationError) Problems() []string { return slices.Clone(this.problems) }

func (this *RegistrationError) Is(target error) bool { return target == ErrRegistration }

func (this *RegistrationError) Error() string {
	return fmt.Sprintf("%s:\n  - %s", ErrRegistration.Error(), strings.Join(this.problems, "\n  - "))
}

// Live answers whether this process is running, and asks no dependency
// anything. A liveness probe that pings the database restarts every replica the
// moment the database is slow, which is the one situation where restarting them
// helps least.
func (this *Registry) Live() Report { return Report{Status: StatusLive} }

func (this *Registry) Ready(ctx context.Context) Report { return this.evaluate(ctx).report }

func (this *Registry) Inspect(ctx context.Context) Detail { return this.evaluate(ctx).detail }

func (this *Registry) Contributions() []Contribution { return slices.Clone(this.contributions) }

type evaluation struct {
	done       chan struct{}
	observedAt time.Time
	report     Report
	detail     Detail
}

// evaluate serves one shared pass to every concurrent caller: a readiness
// endpoint is scraped by every probe, load balancer and dashboard a deployment
// has, and one pass per scrape turns a health page into load on the dependency
// it is asking about.
//
// A cached pass younger than the freshness window is returned as it is. A pass
// already in flight is joined rather than duplicated, and the waiters do not
// donate their cancellation to it — the same rule as [[D-084]]: a request that
// gives up must not fail a pass every other waiter is still waiting for. The
// flight is bounded instead by the per-check timeouts.
func (this *Registry) evaluate(ctx context.Context) *evaluation {
	this.mutex.Lock()
	if this.last != nil && this.now().Sub(this.last.observedAt) < this.freshness {
		fresh := this.last
		this.mutex.Unlock()
		return fresh
	}
	if this.running != nil {
		running := this.running
		this.mutex.Unlock()
		<-running.done
		return running
	}
	running := &evaluation{done: make(chan struct{})}
	this.running = running
	this.mutex.Unlock()

	running.report, running.detail = this.pass(context.WithoutCancel(ctx))
	running.observedAt = running.detail.ObservedAt
	close(running.done)

	this.mutex.Lock()
	this.last = running
	this.running = nil
	this.mutex.Unlock()
	return running
}

func (this *Registry) pass(ctx context.Context) (Report, Detail) {
	details := make([]CheckDetail, len(this.contributions))
	var group sync.WaitGroup
	for position, contribution := range this.contributions {
		if contribution.Importance == Disabled {
			details[position] = CheckDetail{
				Name:       contribution.Name,
				Code:       contribution.Code,
				Importance: contribution.Importance,
				State:      StateDisabled,
			}
			continue
		}
		group.Add(1)
		go func() {
			defer group.Done()
			details[position] = this.ask(ctx, contribution)
		}()
	}
	group.Wait()

	observedAt := this.now()
	status := StatusReady
	var codes []string
	for _, detail := range details {
		if detail.State != StateFailing {
			continue
		}
		switch detail.Importance {
		case Required:
			status = StatusDown
		case Degrading:
			if status == StatusReady {
				status = StatusDegraded
			}
		default:
			continue
		}
		if detail.Code != "" {
			codes = append(codes, detail.Code)
		}
	}
	sort.Strings(codes)

	return Report{Status: status, Codes: codes},
		Detail{Status: status, ObservedAt: observedAt, Checks: details}
}

func (this *Registry) ask(ctx context.Context, contribution Contribution) CheckDetail {
	detail := CheckDetail{
		Name:       contribution.Name,
		Code:       contribution.Code,
		Importance: contribution.Importance,
		State:      StatePassing,
	}

	asked, cancel := context.WithTimeout(ctx, contribution.Timeout)
	defer cancel()

	started := this.now()
	err := probing(asked, contribution.Probe)
	detail.Took = this.now().Sub(started)
	if err != nil {
		detail.State = StateFailing
		detail.Message = truncate(err.Error())
	}
	return detail
}

func probing(ctx context.Context, probe Probe) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("the probe panicked: %v", recovered)
		}
	}()
	return probe.Check(ctx)
}
