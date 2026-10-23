package execenv

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const RetainedFamilyWorkspaceFile = ".family-workspace-retained.json"

// RetainedFamilyWorkspace prevents automatic reclamation of an old issue
// checkout after its runs move to the canonical family code directory.
type RetainedFamilyWorkspace struct {
	WorkspaceID          string `json:"workspace_id"`
	IssueID              string `json:"issue_id"`
	CodeWorkspaceIssueID string `json:"code_workspace_issue_id"`
	SelectedWorkDir      string `json:"selected_work_dir"`
}

func WriteRetainedFamilyWorkspace(root string, marker RetainedFamilyWorkspace) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(root, RetainedFamilyWorkspaceFile), data, 0600)
}

func ReadRetainedFamilyWorkspace(root string) (*RetainedFamilyWorkspace, error) {
	file := filepath.Join(root, RetainedFamilyWorkspaceFile)
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, errors.New("invalid retained family workspace marker")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var marker RetainedFamilyWorkspace
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, err
	}
	return &marker, nil
}
