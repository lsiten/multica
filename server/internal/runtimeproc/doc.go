package runtimeproc

// Operation retention is bounded by explicit acknowledgement, not implicit eviction.
// The control owner issues "acknowledge" with the latest Fence after consuming
// results. AcknowledgePayload selects completed receipts; other receipts survive,
// including pending work from older epochs. An absent/null payload retains the
// all-completed contract and refuses retirement while any receipt is pending.
// Success increments ReplayEpoch and Revision once. Only the latest completed ACK
// marker is retained for lost-response retries. Retained old requests remain
// queryable/retryable; retired old IDs cannot be reused even with a new fence.
// Domain admission is bounded at 128 receipts, lifecycle at 144, stop at 145,
// and ACK at 146 (one recovery intent slot). Status reports current receipt usage.
// Read capabilities consume neither revisions nor operation receipts.
// DetachMutations optionally executes admitted domain work under the Serve root
// and original deadline, so disconnects do not cancel durable work or admitted
// ACK housekeeping. ReconcileMutation can explicitly prove terminal physical
// outcomes after a canceled domain handler returns, within a two-second root-bound
// context; false, invalid, canceled, or root-canceled reconciliation stays pending.
// Admission and
// queued callers remain caller-cancellable; root/deadline cancellation retains
// uncertain pending receipts. Execution stays inline and Serve joins all handlers.
// A resource incarnation change is a domain decision, never receipt garbage collection.
//
// A missing record (errors.Is(err, os.ErrNotExist)) is distinct from an uncertain
// owner. Network failures mean suspect; only an acquired kernel lock plus confirmed
// stopped record permits replacement. Crashed records and pending operations need
// explicit domain reconciliation outside this package. No automatic replay occurs.
//
// Root cancellation of Process terminates that exact child. Normal callers should
// Stop and Wait to allow domain cleanup. This initial transport does not implement
// independent descendant survival, Windows Job Object breakaway or control ownership
// transfer. Domain handlers own their descendants and must cooperate with cancellation.
