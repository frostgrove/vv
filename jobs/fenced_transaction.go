package jobs

import "context"

type FencedTransactions interface {
	InFencedTx(
		context.Context,
		AttemptController,
		func(context.Context) error,
		func(context.Context) error,
	) error
}
