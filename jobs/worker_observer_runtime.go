package jobs

import (
	"context"
	"time"
)

func (workers *Workers) observeWorker(spec workerEventSpec) {
	if workers == nil || nilInterface(workers.config.observer) {
		return
	}
	event, err := newWorkerEvent(workers.plan, spec)
	if err != nil {
		return
	}
	safeObserve(workers.config.observer, context.Background(), event)
}

func (workers *Workers) observeWorkerStart(operation WorkerOperation, active int) time.Time {
	workers.observeWorker(workerEventSpec{
		Operation: operation,
		Outcome:   WorkerOutcomeStarted,
		Active:    active,
		Limit:     workers.plan.TotalConcurrency(),
	})
	startedAt, err := workers.config.clock.Now()
	if err != nil {
		return time.Time{}
	}
	return startedAt
}

func (workers *Workers) observeWorkerFinish(operation WorkerOperation, outcome WorkerOutcome, failure WorkerFailure, active int, startedAt time.Time) {
	elapsed := time.Duration(0)
	if !startedAt.IsZero() {
		finishedAt, err := workers.config.clock.Now()
		if err == nil && !finishedAt.Before(startedAt) {
			elapsed = finishedAt.Sub(startedAt)
		}
	}
	workers.observeWorker(workerEventSpec{
		Operation: operation,
		Outcome:   outcome,
		Failure:   failure,
		Active:    active,
		Limit:     workers.plan.TotalConcurrency(),
		Elapsed:   elapsed,
	})
}

func (workers *Workers) observeClaim(batch ClaimBatch, call workerDriverCall, active int) {
	spec := workerEventSpec{
		Operation: WorkerOperationClaim,
		Outcome:   call.outcome,
		Failure:   call.failure,
		Active:    active,
		Limit:     workers.plan.TotalConcurrency(),
		Elapsed:   call.elapsed,
	}
	if call.outcome == WorkerOutcomeComplete {
		spec.Items = len(batch.items)
		if spec.Items == 0 {
			spec.Outcome = WorkerOutcomeEmpty
		} else {
			spec.Bytes = claimedDeliveryBytes(batch.items)
		}
	}
	workers.observeWorker(spec)
}

func (workers *Workers) observeRecover(result RecoverResult, call workerDriverCall, active int) {
	spec := workerEventSpec{
		Operation: WorkerOperationRecover,
		Outcome:   call.outcome,
		Failure:   call.failure,
		Active:    active,
		Limit:     workers.plan.TotalConcurrency(),
		Elapsed:   call.elapsed,
	}
	if call.outcome == WorkerOutcomeComplete {
		spec.Items = len(result.items)
		spec.Released = result.released
		spec.More = result.more
		if spec.Items+spec.Released == 0 {
			spec.Outcome = WorkerOutcomeEmpty
		} else {
			spec.Bytes = recoveredDeliveryBytes(result.items)
		}
	}
	workers.observeWorker(spec)
}

func (workers *Workers) observeSaturation(operation WorkerOperation, active, limit int) {
	workers.observeWorker(workerEventSpec{
		Operation: operation,
		Outcome:   WorkerOutcomeSaturated,
		Active:    active,
		Limit:     limit,
	})
}

func (workers *Workers) observeRenew(request RenewRequest, result RenewResult, call workerDriverCall) {
	spec := workerEventSpec{
		Operation: WorkerOperationRenew,
		Outcome:   call.outcome,
		Failure:   call.failure,
		Items:     len(request.leases),
		Limit:     workers.plan.TotalConcurrency(),
		Elapsed:   call.elapsed,
	}
	if call.outcome == WorkerOutcomeComplete {
		spec.Results = renewalResultCounts(result.items)
	}
	workers.observeWorker(spec)
}

func (workers *Workers) observeApply(definition Name, binding BindingName, request ApplyRequest, result ApplyResult, call workerDriverCall) {
	spec := workerEventSpec{
		Operation:   WorkerOperationApply,
		Outcome:     call.outcome,
		Failure:     call.failure,
		Definition:  definition,
		Binding:     binding,
		CommandKind: request.command.kind,
		Items:       1,
		Elapsed:     call.elapsed,
	}
	if call.outcome == WorkerOutcomeComplete {
		spec.Results = []WorkerDeliveryResultCount{{
			mutation: result.result.mutation,
			control:  result.result.control,
			items:    1,
		}}
	}
	workers.observeWorker(spec)
}

func claimedDeliveryBytes(items []ClaimedDelivery) int {
	total := 0
	for index := range items {
		size, err := items[index].recordSize()
		if err != nil {
			return 0
		}
		total += size
	}
	return total
}

func recoveredDeliveryBytes(items []RecoveredDelivery) int {
	total := 0
	for index := range items {
		size, err := items[index].recordSize()
		if err != nil {
			return 0
		}
		total += size
	}
	return total
}

func renewalResultCounts(items []LeaseRenewal) []WorkerDeliveryResultCount {
	type resultKey struct {
		mutation DeliveryMutationStatus
		control  DeliveryControlStatus
	}
	counts := make(map[resultKey]int, maxWorkerDeliveryResultCounts)
	for _, item := range items {
		counts[resultKey{mutation: item.mutation, control: item.control}]++
	}
	results := make([]WorkerDeliveryResultCount, 0, len(counts))
	for key, items := range counts {
		results = append(results, WorkerDeliveryResultCount{mutation: key.mutation, control: key.control, items: items})
	}
	return results
}
