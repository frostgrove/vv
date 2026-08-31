package cachememory

import "context"

type Operation string

const (
	GetOperation     Operation = "get"
	GetManyOperation Operation = "get_many"
	PutOperation     Operation = "put"
	DeleteOperation  Operation = "delete"
	EvictOperation   Operation = "evict"
	ResetOperation   Operation = "reset"
	CloseOperation   Operation = "close"
)

type Outcome string

const (
	HitOutcome      Outcome = "hit"
	MissOutcome     Outcome = "miss"
	StoredOutcome   Outcome = "stored"
	ReplacedOutcome Outcome = "replaced"
	DeletedOutcome  Outcome = "deleted"
	EvictedOutcome  Outcome = "evicted"
	RejectedOutcome Outcome = "rejected"
	CompleteOutcome Outcome = "complete"
)

type Reason string

const (
	ExpiredReason         Reason = "expired"
	MaxEntriesReason      Reason = "max_entries"
	MaxBytesReason        Reason = "max_bytes"
	MaxItemBytesReason    Reason = "max_item_bytes"
	ReadLimitReason       Reason = "read_limit"
	BatchItemLimitReason  Reason = "batch_item_limit"
	BatchTotalLimitReason Reason = "batch_total_limit"
	ResetReason           Reason = "reset"
	CloseReason           Reason = "close"
)

type Event struct {
	Operation    Operation
	Outcome      Outcome
	Reason       Reason
	Items        int
	ValueBytes   int64
	ChargedBytes int64
}

type Observer interface {
	Observe(context.Context, Event)
}
