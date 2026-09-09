package otelnative

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/propagation"
)

var ErrInvalidSQLConnector = errors.New("otelnative: SQL connector is required")

func SQLOptions(providers Providers) ([]otelsql.Option, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	return []otelsql.Option{
		otelsql.WithTracerProvider(providers.Tracer),
		otelsql.WithMeterProvider(providers.Meter),
		otelsql.WithTextMapPropagator(propagation.TraceContext{}),
		otelsql.WithSQLCommenter(false),
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			DisableQuery: true,
			RecordError: func(err error) bool {
				return !errors.Is(err, sql.ErrNoRows)
			},
		}),
		otelsql.WithSpanNameFormatter(func(_ context.Context, method otelsql.Method, _ string) string {
			return normalizeSQLSpanName(method)
		}),
	}, nil
}

func OpenSQL(providers Providers, driverName, dataSourceName string) (*sql.DB, error) {
	options, err := SQLOptions(providers)
	if err != nil {
		return nil, err
	}
	return otelsql.Open(driverName, dataSourceName, options...)
}

func OpenSQLDB(providers Providers, connector driver.Connector) (*sql.DB, error) {
	if nilInterface(connector) {
		return nil, ErrInvalidSQLConnector
	}
	options, err := SQLOptions(providers)
	if err != nil {
		return nil, err
	}
	return otelsql.OpenDB(connector, options...), nil
}
