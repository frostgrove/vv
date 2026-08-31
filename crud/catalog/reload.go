package catalog

import (
	"context"
	"time"
)

type Reloader interface {
	Reload(ctx context.Context, table, name string) error
}

const (
	minBackoff   = time.Second
	maxBackoff   = 5 * time.Minute
	reloadFloor  = time.Second
	backoffScale = 2
)

type negative struct {
	until time.Time
	wait  time.Duration
}

func (this *loaded) Reload(ctx context.Context, table, name string) error {
	k := consKey{table: table, name: name}
	now := this.clock()

	this.mu.Lock()
	if n, ok := this.misses[k]; ok && now.Before(n.until) {
		this.mu.Unlock()
		return nil
	}
	if now.Before(this.floor) {
		this.mu.Unlock()
		return nil
	}
	this.floor = now.Add(reloadFloor)
	this.mu.Unlock()

	read, err := this.backend.read(ctx, this.source)
	if err != nil {
		return err
	}
	s := newSnapshot(read)
	this.snap.Store(s)

	this.mu.Lock()
	defer this.mu.Unlock()
	if _, found := s.byCons[k]; found {
		delete(this.misses, k)
		return nil
	}
	if this.misses == nil {
		this.misses = map[consKey]negative{}
	}
	n := this.misses[k]
	switch {
	case n.wait == 0:
		n.wait = minBackoff
	case n.wait < maxBackoff:
		n.wait *= backoffScale
		n.wait = min(n.wait, maxBackoff)
	}
	n.until = now.Add(n.wait)
	this.misses[k] = n
	return nil
}

func (this *loaded) clock() time.Time {
	if this.now == nil {
		return time.Now()
	}
	return this.now()
}
