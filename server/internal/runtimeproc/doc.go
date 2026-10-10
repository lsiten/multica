package runtimeproc

// Operation retention is bounded by explicit acknowledgement, not implicit eviction.
// The sole authorized control owner issues "acknowledge" with the latest Fence only
// after consuming every completed receipt through that revision. Pending receipts
// refuse acknowledgement. Success increments ReplayEpoch, preserving resource and
// supervisor epochs, and retains the acknowledgement receipt for lost-ACK retries.
// Request IDs include ReplayEpoch; retired IDs are rejected even if their fence is
// rewritten. QueryOperation returns retired_request rather than not_found for an
// old epoch. Read capabilities consume neither revisions nor operation receipts.
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
