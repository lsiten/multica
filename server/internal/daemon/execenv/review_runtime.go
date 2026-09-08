package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const reviewRuntimeFile = ".review-runtime.json"

// ReviewRuntime records the exact runtime that prepared a task environment.
// Readers must match both workspace and task against the authoritative owner.
type ReviewRuntime struct {
	WorkspaceID string `json:"workspace_id"`
	TaskID      string `json:"task_id"`
	RuntimeID   string `json:"runtime_id"`
	AgentID     string `json:"agent_id,omitempty"`
	AgentName   string `json:"agent_name,omitempty"`
}

func WriteReviewRuntime(root string, binding ReviewRuntime) error {
	data, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".review-runtime-tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(root, reviewRuntimeFile))
}

func ReadReviewRuntime(root string) (ReviewRuntime, error) {
	var binding ReviewRuntime
	data, err := os.ReadFile(filepath.Join(root, reviewRuntimeFile))
	if err != nil {
		return binding, err
	}
	err = json.Unmarshal(data, &binding)
	return binding, err
}
