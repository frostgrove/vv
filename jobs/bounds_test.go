package jobs

import (
	"testing"
	"time"
)

func TestJobBoundsAreExact(t *testing.T) {
	integers := map[string]struct {
		got  int
		want int
	}{
		"definitions":                {MaxDefinitions, 4096},
		"name bytes":                 {MaxNameBytes, 128},
		"queue name bytes":           {MaxQueueNameBytes, 64},
		"binding name bytes":         {MaxBindingNameBytes, 128},
		"build id bytes":             {MaxBuildIDBytes, 128},
		"codec id bytes":             {MaxCodecIDBytes, 64},
		"intent bytes":               {MaxIntentBytes, 512},
		"intent digest bytes":        {IntentDigestBytes, 32},
		"invocation id bytes":        {InvocationIDBytes, 16},
		"failure code bytes":         {MaxFailureCodeBytes, 64},
		"public failure bytes":       {MaxPublicFailureBytes, 2048},
		"default payload bytes":      {DefaultPayloadBytes, 65536},
		"payload bytes":              {MaxPayloadBytes, 1048576},
		"default decoded bytes":      {DefaultDecodedBytes, 262144},
		"decoded bytes":              {MaxDecodedBytes, 4194304},
		"payload depth":              {MaxPayloadDepth, 64},
		"supported revisions":        {MaxSupportedRevisions, 8},
		"upcast hops":                {MaxUpcastHops, 7},
		"trace carrier bytes":        {MaxTraceCarrierBytes, 1024},
		"trace parent bytes":         {MaxTraceParentBytes, 256},
		"trace state bytes":          {MaxTraceStateBytes, 512},
		"correlation fields":         {MaxCorrelationFields, 8},
		"correlation key bytes":      {MaxCorrelationKeyBytes, 32},
		"correlation value bytes":    {MaxCorrelationValueBytes, 128},
		"actor identity bytes":       {MaxActorIdentityBytes, 512},
		"identity token bytes":       {MaxIdentityTokenBytes, 2048},
		"identity provenance bytes":  {MaxIdentityProvenanceBytes, 64},
		"default attempt trace":      {DefaultAttemptTraceEvents, 32},
		"attempt trace":              {MaxAttemptTraceEvents, 128},
		"trace event name bytes":     {MaxTraceEventNameBytes, 64},
		"default retries":            {DefaultRetries, 5},
		"maximum retries":            {MaximumRetries, 32},
		"default handler deferrals":  {DefaultHandlerDeferrals, 256},
		"maximum handler deferrals":  {MaximumHandlerDeferrals, 4096},
		"default delivery deferrals": {DefaultDeliveryDeferrals, 256},
		"maximum delivery deferrals": {MaximumDeliveryDeferrals, 4096},
		"attempt ordinal":            {MaxAttemptOrdinal, 4129},
		"invocation outcomes":        {MaxInvocationOutcomes, 12356},
		"binding concurrency":        {MaxBindingConcurrency, 256},
		"worker concurrency":         {MaxWorkerConcurrency, 4096},
		"default claim items":        {DefaultClaimItems, 64},
		"claim items":                {MaxClaimItems, 256},
		"default claim bytes":        {DefaultClaimBytes, MaxDeliveryRecordBytes},
		"claim bytes":                {MaxClaimBytes, 67108864},
		"default reclaim batch":      {DefaultReclaimBatch, 100},
		"reclaim batch":              {MaxReclaimBatch, 1000},
		"default transient bytes":    {DefaultTransientBytes, 16777216},
		"default waiters":            {DefaultTransientWaiters, 256},
		"maximum waiters":            {MaxTransientWaiters, 4096},
		"worker in-flight bytes":     {DefaultWorkerInFlightBytes, 67108864},
		"maximum worker bytes":       {MaxWorkerInFlightBytes, 1073741824},
	}
	for name, test := range integers {
		t.Run(name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("got %d, want %d", test.got, test.want)
			}
		})
	}

	durations := map[string]struct {
		got  time.Duration
		want time.Duration
	}{
		"default attempt timeout":  {DefaultAttemptTimeout, 10 * time.Minute},
		"maximum attempt timeout":  {MaximumAttemptTimeout, 24 * time.Hour},
		"default max elapsed":      {DefaultMaxElapsed, 24 * time.Hour},
		"maximum max elapsed":      {MaximumMaxElapsed, 30 * 24 * time.Hour},
		"minimum retry delay":      {MinRetryDelay, 100 * time.Millisecond},
		"default retry delay":      {DefaultRetryDelay, 5 * time.Second},
		"default max retry delay":  {DefaultMaxRetryDelay, 5 * time.Minute},
		"maximum retry delay":      {MaxRetryDelay, time.Hour},
		"terminal retention":       {DefaultTerminalRetention, 7 * 24 * time.Hour},
		"intent retention":         {DefaultIntentRetention, 30 * 24 * time.Hour},
		"maximum retention":        {MaxRetention, 365 * 24 * time.Hour},
		"minimum poll interval":    {MinimumPollInterval, 10 * time.Millisecond},
		"poll interval":            {DefaultPollInterval, time.Second},
		"maximum poll interval":    {MaximumPollInterval, time.Minute},
		"lease ttl":                {DefaultLeaseTTL, time.Minute},
		"heartbeat":                {DefaultHeartbeat, 15 * time.Second},
		"minimum reclaim interval": {MinimumReclaimInterval, 100 * time.Millisecond},
		"reclaim interval":         {DefaultReclaimInterval, 15 * time.Second},
		"maximum reclaim interval": {MaximumReclaimInterval, 24 * time.Hour},
		"shutdown grace":           {DefaultShutdownGrace, 20 * time.Second},
		"maximum shutdown grace":   {MaxShutdownGrace, 10 * time.Minute},
		"transient wait":           {DefaultTransientWait, 250 * time.Millisecond},
	}
	for name, test := range durations {
		t.Run(name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("got %s, want %s", test.got, test.want)
			}
		})
	}
}
