package auditmemory_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/audit/auditmemory"
	"github.com/frostgrove/vv/audit/audittest"
)

func TestRunOnlyAttemptConformance(t *testing.T) {
	audittest.RunOnlyAttempts(t, func(ctx context.Context, request audittest.AttemptStoreRequest) (audittest.AttemptStore, error) {
		log, err := auditmemory.NewLog(auditmemory.LogSpec{})
		if err != nil {
			return audittest.AttemptStore{}, err
		}
		deployment, err := auditmemory.NewDeployment(log)
		if err != nil {
			return audittest.AttemptStore{}, err
		}
		if err := deployment.InstallAndActivate(ctx, request.Catalogs, request.Change); err != nil {
			return audittest.AttemptStore{}, err
		}
		var open func() (audittest.AttemptStore, error)
		open = func() (audittest.AttemptStore, error) {
			store, openErr := auditmemory.New(auditmemory.Spec{Log: log, Clock: request.Clock})
			if openErr != nil {
				return audittest.AttemptStore{}, openErr
			}
			return audittest.AttemptStore{
				Writer: store, Log: store, Exact: store, Attempts: store, Types: store,
				Reopen: open, Close: store.Close,
			}, nil
		}
		return open()
	})
}
