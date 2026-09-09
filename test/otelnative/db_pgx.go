package otelnative

import "github.com/exaring/otelpgx"

func NewPGXTracer(providers Providers) (*otelpgx.Tracer, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	tracerProvider := scopedTracerProvider{
		TracerProvider: providers.Tracer,
		scope:          pgxScopeName,
		normalizeName:  normalizePGXSpanName,
	}
	return otelpgx.NewTracer(
		otelpgx.WithTracerProvider(tracerProvider),
		otelpgx.WithMeterProvider(providers.Meter),
		otelpgx.WithDisableSQLStatementInAttributes(),
		otelpgx.WithDisableConnectionDetailsInAttributes(),
		otelpgx.WithTrimSQLInSpanName(),
		otelpgx.WithSpanNameFunc(func(string) string { return "statement" }),
	), nil
}
