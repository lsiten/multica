package agent

import "encoding/json"

// ApprovalRequest retains the provider's exact operation for a local reviewer.
// Params must never enter task messages, logs, or server telemetry.
type ApprovalRequest struct {
	Method string
	Params json.RawMessage
}
