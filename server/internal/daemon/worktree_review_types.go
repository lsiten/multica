package daemon

import (
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type worktreeReviewRequest struct {
	FileOperation     *protocol.WorkspaceFileOperation `json:"file_operation,omitempty"`
	FileOperationID   string                           `json:"file_operation_id,omitempty"`
	FileID            string                           `json:"file_id,omitempty"`
	ProjectResourceID string                           `json:"project_resource_id,omitempty"`
	Scope             string                           `json:"scope,omitempty"`
	IndexID           string                           `json:"index_id,omitempty"`
	Branch            string                           `json:"branch,omitempty"`
	Head              string                           `json:"head,omitempty"`
	Message           string                           `json:"message,omitempty"`
	Paths             []string                         `json:"paths,omitempty"`
	legacyRuntime     *execenv.ReviewRuntime
	TaskID            string `json:"task_id"`
	WorkspaceID       string `json:"workspace_id"`
	RuntimeID         string `json:"runtime_id"`
	Path              string `json:"path"`
	Target            string `json:"target"`
	Action            string `json:"action"`
	SnapshotID        string `json:"snapshot_id"`
	VersionID         string `json:"version_id,omitempty"`
	FilePath          string `json:"file_path,omitempty"`
	RelativePath      string `json:"relative_path,omitempty"`
	Recursive         *bool  `json:"recursive,omitempty"`
	Side              string `json:"side,omitempty"`
	Offset            int    `json:"offset,omitempty"`
	Limit             int    `json:"limit,omitempty"`
	Comment           string `json:"comment"`
	CommandID         string `json:"command_id"`
	ActorID           string `json:"actor_id"`
	EditBase          string `json:"edit_base,omitempty"`
	EditNew           string `json:"edit_new,omitempty"`
	EditBaseSHA       string `json:"edit_base_sha,omitempty"`
}

type worktreeReviewResponse struct {
	localreview.Snapshot
	Review localreview.RecordView `json:"review"`
}
