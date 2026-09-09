package audit

type receipt struct {
	disposition AppendDisposition
	settlement  Settlement
	revision    RevisionID
	operation   OperationID
	reconcile   ReconcileKey
}

type Receipt struct{ value receipt }

func (r Receipt) Disposition() AppendDisposition { return r.value.disposition }
func (r Receipt) Settlement() Settlement         { return r.value.settlement }
func (r Receipt) RevisionID() RevisionID         { return r.value.revision }
func (r Receipt) OperationID() OperationID       { return r.value.operation }

func (r Receipt) ReconcileKey() (ReconcileKey, bool) {
	return r.value.reconcile, r.value.reconcile.valid()
}

func (Receipt) HoldDisposition() (HoldTransitionDisposition, bool) {
	return 0, false
}

func (Receipt) HoldProjection() (HoldProjectionStateView, bool) {
	return HoldProjectionStateView{}, false
}

func (Receipt) AttemptTransition() (AttemptTransitionWireView, bool) {
	return AttemptTransitionWireView{}, false
}

func (Receipt) AttemptProjection() (AttemptProjectionStateView, bool) {
	return AttemptProjectionStateView{}, false
}

func receiptFromAppend(result AppendResult, settlement Settlement, reconcile ReconcileKey) Receipt {
	header := result.Stored().Revision()
	return Receipt{value: receipt{
		disposition: result.Disposition(), settlement: settlement,
		revision: header.RevisionID, operation: header.OperationID, reconcile: reconcile,
	}}
}

func receiptFromStored(stored StoredHeader, settlement Settlement) Receipt {
	header := stored.Revision()
	return Receipt{value: receipt{
		disposition: Replayed, settlement: settlement,
		revision: header.RevisionID, operation: header.OperationID,
	}}
}

type retryToken struct {
	state     RetryState
	mode      RecoveryMode
	reconcile ReconcileKey
	request   AppendRequest
}

type RetryToken struct{ value retryToken }

func (r RetryToken) State() RetryState          { return r.value.state }
func (r RetryToken) Mode() RecoveryMode         { return r.value.mode }
func (r RetryToken) ReconcileKey() ReconcileKey { return r.value.reconcile }

type recordResult struct {
	receipt Receipt
	retry   RetryToken
}

type RecordResult struct{ value recordResult }

func (r RecordResult) Receipt() (Receipt, bool) {
	return r.value.receipt, r.value.receipt.value.revision != (RevisionID{})
}

func (r RecordResult) RetryToken() (RetryToken, bool) {
	return r.value.retry, r.value.retry.value.reconcile.valid()
}

type captureResult struct {
	staged  bool
	receipt Receipt
	retry   RetryToken
}

type CaptureResult struct{ value captureResult }

func (r CaptureResult) Staged() bool { return r.value.staged }
func (r CaptureResult) Receipt() (Receipt, bool) {
	return r.value.receipt, r.value.receipt.value.revision != (RevisionID{})
}
func (r CaptureResult) RetryToken() (RetryToken, bool) {
	return r.value.retry, r.value.retry.value.reconcile.valid()
}

type groupResult struct {
	joined    bool
	receipt   Receipt
	retry     RetryToken
	reconcile ReconcileKey
}

type GroupResult struct{ value groupResult }

func (r GroupResult) Joined() bool { return r.value.joined }
func (r GroupResult) Receipt() (Receipt, bool) {
	return r.value.receipt, r.value.receipt.value.revision != (RevisionID{})
}
func (r GroupResult) RetryToken() (RetryToken, bool) {
	return r.value.retry, r.value.retry.value.reconcile.valid()
}
func (r GroupResult) ReconcileKey() (ReconcileKey, bool) {
	return r.value.reconcile, r.value.reconcile.valid()
}

type retryCarrier struct {
	token RetryToken
	err   error
}

func (e *retryCarrier) Error() string { return "audit: write requires recovery" }

func RetryTokenOf(err error) (RetryToken, bool) {
	carrier, ok := findErrorAs[*retryCarrier](err)
	if !ok || carrier == nil || !carrier.token.value.reconcile.valid() {
		return RetryToken{}, false
	}
	return carrier.token, true
}

func ReconcileKeyOf(err error) (ReconcileKey, bool) {
	token, ok := RetryTokenOf(err)
	if !ok {
		return ReconcileKey{}, false
	}
	return token.ReconcileKey(), true
}
