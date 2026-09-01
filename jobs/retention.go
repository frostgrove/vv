package jobs

import "context"

type RetentionSweeper interface {
	SweepTerminalRetention(context.Context, int) (int, error)
}
