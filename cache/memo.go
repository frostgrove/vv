package cache

import (
	"bytes"
	"context"
	"fmt"
	"sync"
)

const (
	MaxMemoEntries    = 4096
	MaxMemoTotalBytes = int64(64 << 20)
)

type MemoLimit struct {
	MaxEntries int
	MaxBytes   int64
}

type MemoStats struct {
	Entries int
	Bytes   int64
	Hits    int64
	Stores  int64
	Refused int64
	Closed  bool
}

type memoKey struct {
	address    Address
	codec      string
	keyVersion KeyVersion
	schema     ValueSchema
}

type Memo struct {
	limit   MemoLimit
	mu      sync.Mutex
	entries map[memoKey][]byte
	bytes   int64
	closed  bool
	hits    int64
	stores  int64
	refused int64
}

func NewMemo(limit MemoLimit) (*Memo, error) {
	if limit.MaxEntries <= 0 || limit.MaxEntries > MaxMemoEntries || limit.MaxBytes <= 0 || limit.MaxBytes > MaxMemoTotalBytes {
		return nil, failure("build memo", fmt.Errorf("%w: memo limits are out of range", ErrInvalid))
	}
	return &Memo{limit: limit, entries: make(map[memoKey][]byte)}, nil
}

func (this *Memo) Close() {
	if this == nil {
		return
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	this.closed = true
	this.entries = nil
	this.bytes = 0
}

func (this *Memo) Stats() MemoStats {
	if this == nil {
		return MemoStats{}
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	return MemoStats{
		Entries: len(this.entries),
		Bytes:   this.bytes,
		Hits:    this.hits,
		Stores:  this.stores,
		Refused: this.refused,
		Closed:  this.closed,
	}
}

func (this *Memo) load(key memoKey) ([]byte, bool) {
	if this == nil {
		return nil, false
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.closed {
		return nil, false
	}
	encoded, ok := this.entries[key]
	if !ok {
		return nil, false
	}
	this.hits++
	return bytes.Clone(encoded), true
}

func (this *Memo) store(key memoKey, encoded []byte) {
	if this == nil || len(encoded) == 0 {
		return
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.closed {
		return
	}
	if _, exists := this.entries[key]; exists {
		return
	}
	size := int64(len(encoded))
	if len(this.entries) >= this.limit.MaxEntries || size > this.limit.MaxBytes-this.bytes {
		this.refused++
		return
	}
	this.entries[key] = bytes.Clone(encoded)
	this.bytes += size
	this.stores++
}

func (this *Memo) forget(key memoKey) {
	if this == nil {
		return
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.closed {
		return
	}
	encoded, ok := this.entries[key]
	if !ok {
		return
	}
	delete(this.entries, key)
	this.bytes -= int64(len(encoded))
}

type memoContextKey struct{}

func WithMemo(ctx context.Context, memo *Memo) context.Context {
	if nilInterface(ctx) || memo == nil {
		return ctx
	}
	return context.WithValue(ctx, memoContextKey{}, memo)
}

func MemoFrom(ctx context.Context) *Memo {
	if nilInterface(ctx) {
		return nil
	}
	memo, _ := ctx.Value(memoContextKey{}).(*Memo)
	return memo
}

func (this *cacheCore[K, V]) memoKeyFor(address Address) memoKey {
	return memoKey{
		address:    address,
		codec:      this.valueDescriptor.id,
		keyVersion: this.keyVersion,
		schema:     this.valueDescriptor.schema,
	}
}

func (this *cacheCore[K, V]) forgetMemoized(ctx context.Context, address Address) {
	MemoFrom(ctx).forget(this.memoKeyFor(address))
}

func (this *cacheCore[K, V]) confirmReadAndMemoize(ctx context.Context, ticket readTicket, address Address, encoded []byte) bool {
	memo := MemoFrom(ctx)
	this.coord.mu.Lock()
	defer this.coord.mu.Unlock()
	if ticket.state.generation != ticket.generation || ticket.state.writeActive || ticket.state.invalidating {
		return false
	}
	memo.store(this.memoKeyFor(address), encoded)
	return true
}

func (this *cacheCore[K, V]) confirmBatchReadAndMemoize(ctx context.Context, states []*addressState, generations []uint64, backendRead []Address, encoded map[Address][]byte) bool {
	memo := MemoFrom(ctx)
	this.coord.mu.Lock()
	defer this.coord.mu.Unlock()
	for index, state := range states {
		if state.writeActive || state.invalidating || state.generation != generations[index] {
			return false
		}
	}
	for _, address := range backendRead {
		memo.store(this.memoKeyFor(address), encoded[address])
	}
	return true
}
