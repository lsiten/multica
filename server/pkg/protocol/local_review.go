package protocol

import "encoding/json"

type LocalReviewRuntimeBinding struct {
	WorkspaceID string `json:"workspace_id"`
	TaskID      string `json:"task_id"`
	RuntimeID   string `json:"runtime_id"`
	AgentID     string `json:"agent_id"`
}

type LocalReviewCommand struct {
	CancellationSupported bool   `json:"cancellation_supported,omitempty"`
	ID                    string `json:"id"`
	CommandID             string `json:"command_id,omitempty"`
	ClaimToken            string `json:"claim_token"`
	ReviewID              string `json:"review_id"`
	WorkspaceID           string `json:"workspace_id"`
	RuntimeID             string `json:"runtime_id"`
	TaskID                string `json:"task_id"`
	Path                  string `json:"path"`
	Target                string `json:"target"`
	Action                string `json:"action"`
	SnapshotID            string `json:"snapshot_id"`
	VersionID             string `json:"version_id,omitempty"`
	FilePath              string `json:"file_path,omitempty"`
	Side                  string `json:"side,omitempty"`
	Offset                int    `json:"offset,omitempty"`
	Limit                 int    `json:"limit,omitempty"`
	ActorID               string `json:"actor_id,omitempty"`
	Comment               string `json:"comment,omitempty"`
}

type LocalReviewStatusRequest struct {
	ClaimToken string `json:"claim_token"`
}

type LocalReviewStatus struct {
	Active *bool `json:"active"`
}

type LocalReviewClaim struct {
	Command *LocalReviewCommand `json:"command"`
}

type LocalReviewResult struct {
	Page         json.RawMessage `json:"page,omitempty"`
	Branches     []string        `json:"branches"`
	ClaimToken   string          `json:"claim_token"`
	Snapshot     json.RawMessage `json:"snapshot"`
	Review       json.RawMessage `json:"review,omitempty"`
	MergedCommit string          `json:"merged_commit"`
	Error        string          `json:"error"`
}
