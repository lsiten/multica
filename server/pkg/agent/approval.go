package agent

import "encoding/json"

// ApprovalRequest retains the provider's exact operation for a local reviewer.
// Params must never enter task messages, logs, or server telemetry.
type ApprovalRequest struct {
	Method      string
	Params      json.RawMessage
	FileChanges []ApprovalFileChange
}

// ApprovalFileChange is provider-reported scope, retained only for local review.
type ApprovalFileChange struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	MovePath string `json:"move_path,omitempty"`
}
