package auditmemory_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/audit/auditmemory"
	"github.com/frostgrove/vv/audit/audittest"
)

func TestBasicHistoryConformance(t *testing.T) {
	audittest.BasicHistory(t, func(ctx context.Context, request audittest.HistoryStoreRequest) (audittest.HistoryStore, error) {
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
		return audittest.HistoryStore{
			Writer: store, Log: store, Close: store.Close,
			AmbientTransaction: func(ctx context.Context) (context.Context, func() error, error) {
				tx, err := store.Begin(ctx)
				if err != nil {
					return nil, nil, err
				}
				return auditmemory.WithTransaction(ctx, tx), func() error { return tx.Rollback(context.Background()) }, nil
			},
		}, nil
	})
}
