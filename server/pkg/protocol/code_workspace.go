package protocol

// CodeWorkspaceSelection reports the managed checkout chosen for an issue family.
// The server validates the family root and runtime before retaining this marker.
type CodeWorkspaceSelection struct {
	IssueID   string `json:"issue_id"`
	RuntimeID string `json:"runtime_id"`
	WorkDir   string `json:"work_dir"`
}
