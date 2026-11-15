package daemon

import (
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type mirrorConnectionBinding struct {
	Generation       uint64                 `json:"generation"`
	ServerGeneration string                 `json:"server_generation"`
	Resources        []protocol.ResourceKey `json:"resources"`
}
type mirrorProcessRequest struct {
	Generation  uint64                   `json:"generation"`
	Binding     *mirrorConnectionBinding `json:"binding,omitempty"`
	Operation   string                   `json:"operation"`
	Payload     json.RawMessage          `json:"payload,omitempty"`
	WorkspaceID string                   `json:"workspace_id,omitempty"`
	RuntimeID   string                   `json:"runtime_id,omitempty"`
	JobID       string                   `json:"job_id,omitempty"`
}
type mirrorJobResult struct {
	JobID  string          `json:"job_id"`
	Done   bool            `json:"done"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Reason string          `json:"reason,omitempty"`
}
type mirrorBridgeEvent struct {
	InstanceID string          `json:"instance_id"`
	ID         string          `json:"id"`
	Generation uint64          `json:"generation"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	Deadline   time.Time       `json:"deadline"`
}
type mirrorBridgeReply struct {
	InstanceID string          `json:"instance_id"`
	ID         string          `json:"id"`
	Generation uint64          `json:"generation"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
}
type mirrorBridgePoll struct {
	Renew      bool   `json:"renew"`
	InstanceID string `json:"instance_id"`
	Generation uint64 `json:"generation"`
}
