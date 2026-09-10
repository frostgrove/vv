package audit

type AttemptTransitionKind uint8

const (
	AttemptStartedTransition AttemptTransitionKind = iota + 1
	AttemptCheckpointTransition
	AttemptOutcomeUnknownTransition
	AttemptSucceededTransition
	AttemptFailedTransition
	AttemptCancelledTransition
	AttemptAbandonedTransition
)

const (
	AttemptStartedAction        Action = "attempt.started"
	AttemptCheckpointAction     Action = "attempt.checkpoint"
	AttemptOutcomeUnknownAction Action = "attempt.outcome_unknown"
	AttemptSucceededAction      Action = "attempt.succeeded"
	AttemptFailedAction         Action = "attempt.failed"
	AttemptCancelledAction      Action = "attempt.cancelled"
	AttemptAbandonedAction      Action = "attempt.abandoned"
)

type AttemptState uint8

const (
	AttemptOpenState AttemptState = iota + 1
	AttemptUncertainState
	AttemptSucceededState
	AttemptFailedState
	AttemptCancelledState
	AttemptAbandonedState
)
