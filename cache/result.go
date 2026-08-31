package cache

import (
	"context"
	"time"
)

type State uint8

const (
	Hit State = iota + 1
	Miss
	Negative
	Stale
	Loaded
)

type Presence uint8

const (
	Found Presence = iota + 1
	CleanAbsent
)

type Result[V any] struct {
	Value      V
	State      State
	validUntil time.Time
}

type LoadResult[V any] struct {
	Value    V
	Presence Presence
}

type Loader[K, V any] func(context.Context, K) (LoadResult[V], error)

func Present[V any](value V) LoadResult[V] {
	return LoadResult[V]{Value: value, Presence: Found}
}

func Absent[V any]() LoadResult[V] {
	return LoadResult[V]{Presence: CleanAbsent}
}

type LocalStats struct {
	CoordinationEntries  int
	ActiveFlights        int
	FlightWaiters        int
	CoordinationWaiters  int
	ActiveWrites         int
	TransientBytes       int64
	TransientWaiters     int
	TimedContextWatchers int
}
