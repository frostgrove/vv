package jobsredis

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frostgrove/vv/jobs"
	"github.com/redis/go-redis/v9"
)

const DefaultPrefix = "frostgrove:jobs"
const FormatVersion = "1"

var ErrNotReady = errors.New("jobsredis: driver is not ready")
var ErrFormatMismatch = errors.New("jobsredis: format mismatch")

type Spec struct {
	Client           redis.UniversalClient
	Namespace        jobs.Namespace
	Prefix           string
	Backend          jobs.BackendID
	Entropy          io.Reader
	OperationTimeout time.Duration
}

type Driver struct {
	namespace        jobs.Namespace
	description      jobs.BackendDescription
	repo             repository
	entropy          io.Reader
	entropyMu        sync.Mutex
	operationTimeout time.Duration
	ready            atomic.Bool
}

var _ jobs.Sender = (*Driver)(nil)
var _ jobs.DeliveryDriver = (*Driver)(nil)

func Open(ctx context.Context, client redis.UniversalClient, namespace jobs.Namespace) (*Driver, error) {
	driver, err := New(Spec{Client: client, Namespace: namespace})
	if err != nil {
		return nil, err
	}
	if err := driver.Prepare(ctx); err != nil {
		return nil, err
	}
	return driver, nil
}

func New(spec Spec) (*Driver, error) {
	if nilValue(spec.Client) || spec.Namespace.IsZero() {
		return nil, fmt.Errorf("jobsredis: %w: client and namespace are required", jobs.ErrInvalid)
	}
	prefix := spec.Prefix
	if prefix == "" {
		prefix = DefaultPrefix
	}
	if !validPrefix(prefix) {
		return nil, fmt.Errorf("jobsredis: %w: invalid prefix", jobs.ErrInvalid)
	}
	operationTimeout := spec.OperationTimeout
	if operationTimeout == 0 {
		operationTimeout = jobs.DefaultOperationTimeout
	}
	if operationTimeout < jobs.MinimumOperationTimeout || operationTimeout > jobs.MaximumOperationTimeout {
		return nil, fmt.Errorf("jobsredis: %w: operation timeout", jobs.ErrInvalid)
	}
	backend := spec.Backend
	if backend.IsZero() {
		backend = defaultBackend(prefix, spec.Namespace)
	}
	durability, err := jobs.NewDurabilityProfile(jobs.AckBeforePersistence, jobs.AcknowledgedLossPossible, jobs.FailureSet{})
	if err != nil {
		return nil, err
	}
	resources, err := jobs.NewResourceProfile(jobs.ResourceProfileSpec{
		SteadyBase: jobs.ResourcesSpec{MaxConcurrentRemoteOps: 2},
		PerWorker:  jobs.ResourcesSpec{MaxConcurrentRemoteOps: 1},
		Lifecycle:  jobs.ResourcesSpec{MaxConcurrentRemoteOps: 1},
	})
	if err != nil {
		return nil, err
	}
	description, err := jobs.NewBackendDescriptionWithResources(backend, durability, jobs.Capabilities{
		Priority:     true,
		Debounce:     true,
		Unique:       true,
		Scheduled:    true,
		AttemptTrace: true,
	}, resources)
	if err != nil {
		return nil, err
	}
	entropy := spec.Entropy
	if entropy == nil {
		entropy = rand.Reader
	}
	digest := spec.Namespace.Digest()
	base := prefix + ":{" + hex.EncodeToString(digest[:]) + "}"
	return &Driver{
		namespace:        spec.Namespace,
		description:      description,
		repo:             newRepository(spec.Client, base),
		entropy:          entropy,
		operationTimeout: operationTimeout,
	}, nil
}

func (d *Driver) Description() jobs.BackendDescription {
	if d == nil {
		return jobs.BackendDescription{}
	}
	return d.description
}

func (d *Driver) Namespace() jobs.Namespace {
	if d == nil {
		return jobs.Namespace{}
	}
	return d.namespace
}

func (d *Driver) Prepare(ctx context.Context) error {
	return d.Check(ctx)
}

func (d *Driver) Check(ctx context.Context) error {
	if d == nil || nilValue(d.repo.client) {
		return ErrNotReady
	}
	d.ready.Store(false)
	opCtx, cancel, err := d.operationContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	if err := d.repo.prepare(opCtx); err != nil {
		return err
	}
	d.ready.Store(true)
	return nil
}

func (d *Driver) requireReady() error {
	if d == nil || !d.ready.Load() {
		return ErrNotReady
	}
	return nil
}

func (d *Driver) operationContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		return nil, nil, jobs.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= d.operationTimeout {
		return ctx, func() {}, nil
	}
	result, cancel := context.WithTimeout(ctx, d.operationTimeout)
	return result, cancel, nil
}

func (d *Driver) token(size int) ([]byte, error) {
	value := make([]byte, size)
	d.entropyMu.Lock()
	_, err := io.ReadFull(d.entropy, value)
	d.entropyMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("jobsredis: entropy: %w", err)
	}
	return value, nil
}

func defaultBackend(prefix string, namespace jobs.Namespace) jobs.BackendID {
	digest := sha256.New()
	_, _ = digest.Write([]byte("frostgrove.jobs.redis.v1\x00"))
	_, _ = digest.Write([]byte(prefix))
	value := namespace.Digest()
	_, _ = digest.Write(value[:])
	var raw [jobs.BackendIDBytes]byte
	copy(raw[:], digest.Sum(nil))
	backend, _ := jobs.BackendIDFromBytes(raw)
	return backend
}

func validPrefix(value string) bool {
	if len(value) == 0 || len(value) > 128 || value[0] == ':' || value[len(value)-1] == ':' {
		return false
	}
	for index := range len(value) {
		current := value[index]
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' || current >= '0' && current <= '9' || current == ':' || current == '-' || current == '_' {
			continue
		}
		return false
	}
	return true
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
