package jobs

import "context"

func (workers *Workers) Check(ctx context.Context) error {
	if workers == nil || workers.runtime == nil || nilInterface(ctx) {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if record, failed := workers.fatal.load(); failed {
		return record.err
	}
	return workers.runtime.readiness()
}

func (runtime *workersRuntime) readiness() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	switch runtime.state {
	case workersRuntimeRunning:
		return nil
	case workersRuntimeFresh, workersRuntimeDraining:
		return ErrNotActivated
	default:
		if runtime.err != nil {
			return runtime.err
		}
		return ErrNotActivated
	}
}
