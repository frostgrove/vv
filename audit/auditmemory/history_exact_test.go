package auditmemory_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/audit/auditmemory"
	"github.com/frostgrove/vv/audit/audittest"
)

func TestExactHistoryConformance(t *testing.T) {
	audittest.ExactHistory(t, func(ctx context.Context, request audittest.HistoryStoreRequest) (audittest.HistoryStore, error) {
		log, err := auditmemory.NewLog(auditmemory.LogSpec{})
		if err != nil {
			return audittest.HistoryStore{}, err
		}
		deployment, err := auditmemory.NewDeployment(log)
		if err != nil {
			return audittest.HistoryStore{}, err
		}
		if err := deployment.InstallAndActivate(ctx, request.Catalogs, request.Change); err != nil {
			return audittest.HistoryStore{}, err
		}
		store, err := auditmemory.New(auditmemory.Spec{Log: log, Clock: request.Clock})
		if err != nil {
			return audittest.HistoryStore{}, err
		}
		return audittest.HistoryStore{Writer: store, Log: store, Exact: store, Close: store.Close}, nil
	})
}
