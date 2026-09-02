package cache

import (
	"context"
	"fmt"
)

func (this *Cache[K, V]) Check(ctx context.Context) error {
	core, err := this.core()
	if err != nil {
		return err
	}
	if nilInterface(ctx) {
		return failure("check cache", fmt.Errorf("%w: context is nil", ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return failure("check cache", err)
	}
	if core.policy.disabled {
		return nil
	}
	checker, ok := HealthCheckerOf(core.backend)
	if !ok {
		return nil
	}
	backendCtx, cancel, contextErr := core.backendContext(ctx)
	if contextErr != nil {
		return failure("check cache", contextErr)
	}
	checkErr := backendCheck(checker, backendCtx)
	cancel()
	if checkErr != nil {
		return failure("check cache", checkErr)
	}
	return nil
}
