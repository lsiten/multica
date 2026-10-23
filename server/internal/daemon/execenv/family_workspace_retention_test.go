package execenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFamilyWorkspaceMarkersReplaceSymlinkWithoutWritingTarget(t *testing.T) {
	for _, marker := range []string{RetainedFamilyWorkspaceFile, managedEnvProvenanceFile} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(t.TempDir(), "preserved")
			if err := os.WriteFile(target, []byte("user data"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(root, marker)); err != nil {
				t.Fatal(err)
			}
			var err error
			if marker == RetainedFamilyWorkspaceFile {
				err = WriteRetainedFamilyWorkspace(root, RetainedFamilyWorkspace{WorkspaceID: "workspace", IssueID: "child", CodeWorkspaceIssueID: "root"})
			} else {
				err = WriteManagedEnvProvenance(root, ManagedEnvProvenance{WorkspaceID: "workspace", CodeWorkspaceIssueID: "root"})
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(target); err != nil || string(got) != "user data" {
				t.Fatalf("symlink target was modified: %q %v", got, err)
			}
			info, err := os.Lstat(filepath.Join(root, marker))
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("marker not published atomically: %v %v", info, err)
			}
		})
	}
}
