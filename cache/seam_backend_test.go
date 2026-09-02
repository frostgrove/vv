package cache

import (
	"bytes"
	"context"
	"sync"
	"time"
)

type seamBackend struct {
	description BackendDescription
	mu          sync.Mutex
	values      map[Address][]byte
	gets        int
	puts        int
	deletes     int
	getFailure  error
	putFailure  error
}

func newSeamBackend(policy Policy) *seamBackend {
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		panic(err)
	}
	return &seamBackend{
		description: BackendDescription{
			Name:              "seam",
			Topology:          ProcessBackend,
			ExpiryClock:       ProcessExpiryClock,
			MaxItemBytes:      maximum,
			RelativeExpiry:    true,
			MaxRelativeExpiry: 24 * time.Hour,
			CapacityBounded:   true,
		},
		values: make(map[Address][]byte),
	}
}

func (this *seamBackend) DescribeBackend() BackendDescription { return this.description }

func (this *seamBackend) Get(_ context.Context, address Address, _ ReadLimit) ([]byte, bool, error) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.gets++
	if this.getFailure != nil {
		return nil, false, this.getFailure
	}
	value, ok := this.values[address]
	return bytes.Clone(value), ok, nil
}

func (this *seamBackend) Put(_ context.Context, address Address, value []byte, _ Expiry) error {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.puts++
	if this.putFailure != nil {
		return this.putFailure
	}
	this.values[address] = bytes.Clone(value)
	return nil
}

func (this *seamBackend) Delete(_ context.Context, address Address) error {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.deletes++
	delete(this.values, address)
	return nil
}

func (this *seamBackend) reads() int {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.gets
}

func (this *seamBackend) writes() int {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.puts
}

func (this *seamBackend) stored() int {
	this.mu.Lock()
	defer this.mu.Unlock()
	return len(this.values)
}
