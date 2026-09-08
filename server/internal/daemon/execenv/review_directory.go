package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const reviewDirectoryFile = ".review-directory.json"

// ReviewDirectory binds an external local directory to a daemon-prepared task.
// The marker stays in the owned environment, never inside the user's repository.
type ReviewDirectory struct {
	WorkspaceID string `json:"workspace_id"`
	TaskID      string `json:"task_id"`
	Path        string `json:"path"`
	SourcePath  string `json:"source_path,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Commit      string `json:"commit,omitempty"`
}

func WriteReviewDirectory(root string, binding ReviewDirectory) error {
	canonical, err := filepath.EvalSymlinks(binding.Path)
	if err != nil {
		return err
	}
	binding.Path = canonical
	data, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, reviewDirectoryFile), data, 0o600)
}

func ReadReviewDirectory(root string) (ReviewDirectory, error) {
	var binding ReviewDirectory
	data, err := os.ReadFile(filepath.Join(root, reviewDirectoryFile))
	if err != nil {
		return binding, err
	}
	err = json.Unmarshal(data, &binding)
	return binding, err
}
