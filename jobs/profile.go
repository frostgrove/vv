package jobs

import (
	"fmt"
	"time"
)

type Profile struct {
	name              string
	policy            Policy
	workerConcurrency int
	err               error
}

var (
	Interactive = newProfile("Interactive", "interactive", 50, 4, 2*time.Minute, 2*time.Hour, 3, 64, 64, time.Second, time.Minute, 24*time.Hour, 7*24*time.Hour)
	Default     = newProfile("Default", "default", 100, 2, DefaultAttemptTimeout, DefaultMaxElapsed, DefaultRetries, DefaultHandlerDeferrals, DefaultDeliveryDeferrals, DefaultRetryDelay, DefaultMaxRetryDelay, DefaultTerminalRetention, DefaultIntentRetention)
	Heavy       = newProfile("Heavy", "heavy", 100, 1, 30*time.Minute, 72*time.Hour, 5, 256, 256, 15*time.Second, 15*time.Minute, 14*24*time.Hour, 30*24*time.Hour)
	Batch       = newProfile("Batch", "batch", 200, 1, 2*time.Hour, 7*24*time.Hour, 5, 512, 512, 30*time.Second, 30*time.Minute, 30*24*time.Hour, 90*24*time.Hour)
)

func newProfile(name, queue string, priority, concurrency int, attemptTimeout, maxElapsed time.Duration, retries, handlerDeferrals, deliveryDeferrals int, initialBackoff, maximumBackoff, retention, intentRetention time.Duration) Profile {
	queueName, err := ParseQueueName(queue)
	policy := Policy{
		Queue:                queueName,
		Priority:             priority,
		AttemptTimeout:       attemptTimeout,
		MaxElapsed:           maxElapsed,
		MaxRetries:           retries,
		MaxHandlerDeferrals:  handlerDeferrals,
		MaxDeliveryDeferrals: deliveryDeferrals,
		Backoff:              Exponential(initialBackoff, maximumBackoff, FullJitter),
		Retention:            retention,
		IntentRetention:      intentRetention,
		Payload:              DefaultPayloadLimit(),
		profile:              name,
	}
	if err == nil && (concurrency < 1 || concurrency > MaxBindingConcurrency) {
		err = fmt.Errorf("%w: worker concurrency is outside supported bounds", ErrInvalid)
	}
	if err == nil {
		err = validatePolicy(policy)
	}
	baseline := snapshotPolicy(policy)
	policy.baseline = &baseline
	return Profile{name: name, policy: policy, workerConcurrency: concurrency, err: err}
}

func (this Profile) Name() string { return this.name }

func (this Profile) Build() (Policy, error) {
	if this.err != nil {
		return Policy{}, this.err
	}
	if this.name == "" || this.policy.profile != this.name {
		return Policy{}, fmt.Errorf("%w: profile is not initialized", ErrInvalid)
	}
	if this.workerConcurrency < 1 || this.workerConcurrency > MaxBindingConcurrency {
		return Policy{}, fmt.Errorf("%w: worker concurrency is outside supported bounds", ErrInvalid)
	}
	if err := validatePolicy(this.policy); err != nil {
		return Policy{}, err
	}
	return this.policy, nil
}

func (this Profile) With(options ...Option) Profile {
	if this.err != nil {
		return this
	}
	policy := this.policy
	for index, option := range options {
		if nilInterface(option) {
			this.err = fmt.Errorf("%w: profile option %d is nil", ErrInvalid, index)
			return this
		}
		if err := option.applyPolicy(&policy); err != nil {
			this.err = fmt.Errorf("%w: profile option %d: %v", ErrInvalid, index, err)
			return this
		}
	}
	policy.profile = this.name
	if err := validatePolicy(policy); err != nil {
		this.err = err
		return this
	}
	this.policy = policy
	return this
}

func MaxBytes(value int) Option     { return PayloadBytes(value) }
func DecodedBytes(value int) Option { return MaxDecodedPayloadBytes(value) }
func MaxDepth(value int) Option     { return PayloadDepth(value) }
