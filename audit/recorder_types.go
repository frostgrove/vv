package audit

type AppendDisposition uint8

const (
	Inserted AppendDisposition = iota + 1
	Replayed
)

type Settlement uint8

const (
	Committed Settlement = iota + 1
	InCallerTransaction
)

type RetryState uint8

const (
	CertainlyNotWritten RetryState = iota + 1
	PendingUnknown
)

type RecoveryMode uint8

const (
	RetryStandalone RecoveryMode = iota + 1
	ReconcileTransaction
)

type LookupState uint8

const (
	Found LookupState = iota + 1
	AbsentNow
)
