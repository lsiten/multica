package runtimeproc

// NeedsReconciliation is shared by launch admission and crash recovery. A
// stopped process may still carry an operation with an unknown outcome; its
// receipts must be preserved before a new owner can start.
func (record Record) NeedsReconciliation() bool {
	if record.State != "stopped" {
		return true
	}
	for _, receipt := range record.Operations {
		if receipt.State != "completed" {
			return true
		}
	}
	return false
}
