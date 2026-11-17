package protocol

type WorkspaceTrashEntry struct {
	RecoveryID string `json:"recovery_id"`
	Path       string `json:"path"`
	Directory  bool   `json:"directory"`
	DeletedAt  string `json:"deleted_at,omitempty"`
	ActorID    string `json:"actor_id"`
}

type WorkspaceTrashPage struct {
	Kind       string                `json:"kind"`
	Path       string                `json:"path"`
	Entries    []WorkspaceTrashEntry `json:"items"`
	NextOffset int                   `json:"next_offset"`
	HasMore    bool                  `json:"has_more"`
}
