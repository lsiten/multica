package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Only claim identity and the explicitly selected GUI model cross the boundary.
// Task, AgentData, provider environments and account credentials never do.
type mirrorTaskClaim struct {
	TaskID         string                               `json:"task_id"`
	WorkspaceID    string                               `json:"workspace_id"`
	RuntimeID      string                               `json:"runtime_id"`
	AgentID        string                               `json:"agent_id"`
	DispatchedAt   string                               `json:"dispatched_at"`
	PriorSessionID string                               `json:"prior_session_id,omitempty"`
	Source         *protocol.MirrorSourceBinding        `json:"source,omitempty"`
	Continuation   *protocol.VscreenContinuationContext `json:"continuation,omitempty"`
	Model          *mirrorGUIModel                      `json:"model,omitempty"`
}
type mirrorGUIModel struct {
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	Style    string `json:"style"`
}
type mirrorControlBinding struct {
	Generation       uint64                 `json:"generation"`
	ServerGeneration string                 `json:"server_generation"`
	Resources        []protocol.ResourceKey `json:"resources"`
}
type mirrorProcessRequest struct {
	Generation  uint64                 `json:"generation"`
	Binding     *mirrorControlBinding  `json:"binding,omitempty"`
	Operation   string                 `json:"operation"`
	Payload     json.RawMessage        `json:"payload,omitempty"`
	Claim       *mirrorTaskClaim       `json:"claim,omitempty"`
	ExecutionID string                 `json:"execution_id,omitempty"`
	WorkspaceID string                 `json:"workspace_id,omitempty"`
	RuntimeID   string                 `json:"runtime_id,omitempty"`
	Provider    string                 `json:"provider,omitempty"`
	Local       *mirrorLocalAction     `json:"local,omitempty"`
	Approval    *mirrorApprovalRequest `json:"approval,omitempty"`
	JobID       string                 `json:"job_id,omitempty"`
}
type mirrorLocalAction struct {
	Action         string   `json:"action"`
	WorkspaceID    string   `json:"workspace_id"`
	RuntimeID      string   `json:"runtime_id"`
	InterventionID string   `json:"intervention_id,omitempty"`
	Destination    string   `json:"destination,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	WindowHandle   string   `json:"window_handle,omitempty"`
	Excluded       []uint32 `json:"excluded,omitempty"`
}
type mirrorLocalResult struct {
	InterventionID    string                       `json:"intervention_id,omitempty"`
	SelectionRequired bool                         `json:"selection_required"`
	Candidates        *appcontrol.WindowCandidates `json:"candidates,omitempty"`
}
type mirrorApprovalRequest struct {
	WorkspaceID string                      `json:"workspace_id"`
	RuntimeID   string                      `json:"runtime_id"`
	UserID      string                      `json:"user_id"`
	Operation   protocol.MirrorCLIOperation `json:"operation"`
}
type mirrorExecutionGrant struct {
	InstanceID  string          `json:"instance_id"`
	Claim       mirrorTaskClaim `json:"claim"`
	ExecutionID string          `json:"execution_id"`
	MCP         json.RawMessage `json:"mcp,omitempty"`
	Active      bool            `json:"active"`
}
type mirrorReleaseResult struct {
	InterventionPending bool `json:"intervention_pending"`
}
type mirrorJobResult struct {
	JobID  string                          `json:"job_id"`
	Done   bool                            `json:"done"`
	Result json.RawMessage                 `json:"result,omitempty"`
	Error  string                          `json:"error,omitempty"`
	Reason protocol.VscreenRejectionReason `json:"reason,omitempty"`
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
type mirrorReportRequest struct {
	Report         *protocol.VscreenIntervention        `json:"report,omitempty"`
	WorkspaceID    string                               `json:"workspace_id,omitempty"`
	RuntimeID      string                               `json:"runtime_id,omitempty"`
	InterventionID string                               `json:"intervention_id,omitempty"`
	State          protocol.VscreenInterventionState    `json:"state,omitempty"`
	Epoch          string                               `json:"epoch,omitempty"`
	Proof          *protocol.VscreenContinuationContext `json:"proof,omitempty"`
}
type mirrorReportPort interface {
	Queue(context.Context, protocol.VscreenIntervention) error
	Acknowledged(string, protocol.VscreenInterventionState) bool
	Consume(string, string, protocol.VscreenContinuationContext) error
	CancelScope(string, string) error
	InvalidateEpoch(string, string, string) error
}

func (d *Daemon) mirrorReportsLocked() mirrorReportPort {
	if d.mirrorReportBridge != nil {
		return d.mirrorReportBridge
	}
	if d.vscreenReporter != nil {
		return d.vscreenReporter
	}
	return nil
}

func mirrorClaimsEqual(a, b mirrorTaskClaim) bool {
	a.Model = nil
	b.Model = nil
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

// mirrorReportedClaimKey binds provider-stop notification to the exact claim whose terminal report succeeded.
type mirrorReportedClaimKey struct{}
