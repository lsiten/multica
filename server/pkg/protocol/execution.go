package protocol

import (
	"github.com/google/uuid"
	"strings"
	"time"
)

// ExecutionCapabilityV1 advertises claim-bound callbacks and complete snapshot reconciliation.
const ExecutionCapabilityV1 = "execution-reconciliation-v1"

// ExecutionIdentity names a server-authorized worker for one exact dispatch claim.
type ExecutionIdentity struct {
	TaskID       string    `json:"task_id"`
	RuntimeID    string    `json:"runtime_id"`
	DispatchedAt time.Time `json:"dispatched_at"`
	ExecutionID  string    `json:"execution_id"`
	WorkerID     string    `json:"worker_id"`
}

// SupervisorRequest atomically replaces the expected control incarnation.
type SupervisorRequest struct {
	InstanceID    string `json:"instance_id"`
	ExpectedEpoch int64  `json:"expected_epoch"`
}

// SupervisorResponse keeps control ownership separate from task execution identity.
type SupervisorResponse struct {
	InstanceID   string   `json:"instance_id"`
	Epoch        int64    `json:"epoch"`
	Capabilities []string `json:"capabilities"`
}

// BindExecutionRequest reserves a single worker before any provider may start.
type BindExecutionRequest struct {
	WorkerID        string    `json:"worker_id"`
	DispatchedAt    time.Time `json:"dispatched_at"`
	SupervisorEpoch int64     `json:"supervisor_epoch"`
}

// ExecutionGrantRequest requests only callback operations for an existing execution.
type ExecutionGrantRequest struct {
	ExecutionID     string   `json:"execution_id"`
	SupervisorEpoch int64    `json:"supervisor_epoch"`
	Operations      []string `json:"operations"`
	RevokeTokenHash string   `json:"revoke_token_hash,omitempty"`
}

// ExecutionGrantResponse returns the secret once; storage retains only its hash.
type ExecutionGrantResponse struct {
	Token     string            `json:"token"`
	ExpiresAt time.Time         `json:"expires_at"`
	Identity  ExecutionIdentity `json:"identity"`
}

// ExecutionObservation is an explicit live, unknown, or confirmed_lost worker observation.
// confirmed_lost asserts the worker and its provider process tree have both exited.
type ExecutionObservation struct {
	ExecutionIdentity
	State string `json:"state"`
}

// ReconcileExecutionsRequest is one bounded page of an immutable inventory snapshot.
// Missing workers are never inferred lost, even from a complete inventory.
type ReconcileExecutionsRequest struct {
	SnapshotID      string                 `json:"snapshot_id"`
	SupervisorEpoch int64                  `json:"supervisor_epoch"`
	Page            int32                  `json:"page"`
	Complete        bool                   `json:"complete"`
	Unknown         bool                   `json:"unknown"`
	Entries         []ExecutionObservation `json:"entries"`
}

// ExecutionReconcileResult states the authority of one observed worker.
type ExecutionReconcileResult struct {
	TaskID      string `json:"task_id"`
	ExecutionID string `json:"execution_id"`
	Outcome     string `json:"outcome"`
}

// ReconcileExecutionsResponse never permits replacement while inventory is incomplete.
type ReconcileExecutionsResponse struct {
	SnapshotID string                     `json:"snapshot_id"`
	Complete   bool                       `json:"complete"`
	Results    []ExecutionReconcileResult `json:"results"`
}

// executionOperation is the single closed registry shared by grant issuance,
// literal-path authentication, and the scoped client. A comment is a resource
// segment, never part of the operation granted to a worker.
func executionOperation(operation string) (method string, comment bool, ok bool) {
	switch operation {
	case "status":
		return "GET", false, true
	case "start", "prepare-lease", "cancel-ack", "wait-local-directory", "progress", "session", "complete", "fail", "usage", "messages", "jev-decision-logs", "supplements/claim", "worktree-delivery", "project-graph/events":
		return "POST", false, true
	case "supplements/ack":
		return "POST", true, true
	default:
		return "", false, false
	}
}

// ExecutionOperationAllowed validates the method against the closed grant registry.
func ExecutionOperationAllowed(method, operation string) bool {
	expected, _, ok := executionOperation(operation)
	return ok && method == expected
}

// ExecutionCallbackPath builds only a registered route with canonical UUIDs.
func ExecutionCallbackPath(method, operation, taskID, commentID string) (string, bool) {
	expected, comment, ok := executionOperation(operation)
	if !ok || method != expected || !canonicalExecutionUUID(taskID) {
		return "", false
	}
	suffix := operation
	if comment {
		if !canonicalExecutionUUID(commentID) {
			return "", false
		}
		suffix = "supplements/" + commentID + "/ack"
	} else if commentID != "" {
		return "", false
	}
	return "/api/daemon/tasks/" + taskID + "/" + suffix, true
}

// ExecutionCallbackOperation rejects encoded, duplicate, and additional path
// segments rather than allowing a grant through suffix or substring matching.
func ExecutionCallbackOperation(method, path string) (taskID, operation, commentID string, ok bool) {
	if strings.ContainsAny(path, "%?\\") {
		return "", "", "", false
	}
	parts := strings.Split(path, "/")
	if len(parts) < 6 || parts[0] != "" || parts[1] != "api" || parts[2] != "daemon" || parts[3] != "tasks" {
		return "", "", "", false
	}
	taskID = parts[4]
	operation = strings.Join(parts[5:], "/")
	if len(parts) == 8 && parts[5] == "supplements" && parts[7] == "ack" {
		operation = "supplements/ack"
		commentID = parts[6]
	}
	expected, valid := ExecutionCallbackPath(method, operation, taskID, commentID)
	return taskID, operation, commentID, valid && expected == path
}
func canonicalExecutionUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}

// ExecutionInventoryEntry exposes server authority, not a claim of process liveness.
type ExecutionInventoryEntry struct {
	ExecutionIdentity
	Status  string `json:"status"`
	Revoked bool   `json:"revoked"`
}

// ExecutionInventoryResponse is a bounded keyset page of current server identities.
type ExecutionInventoryResponse struct {
	Entries    []ExecutionInventoryEntry `json:"entries"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}
