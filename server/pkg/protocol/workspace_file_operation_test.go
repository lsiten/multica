package protocol

import "testing"

func TestWorkspaceFileOperationSelectionIdentity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation WorkspaceFileOperation
		valid     bool
	}{
		{"selected move", WorkspaceFileOperation{Kind: WorkspaceMove, Path: "from", Destination: "to", ExpectedFileID: "26d9d04d-5c45-40b6-bb58-c60e7d59433c"}, true},
		{"selected trash", WorkspaceFileOperation{Kind: WorkspaceTrash, Path: "from", ExpectedFileID: "26d9d04d-5c45-40b6-bb58-c60e7d59433c"}, true},
		{"malformed identity", WorkspaceFileOperation{Kind: WorkspaceTrash, Path: "from", ExpectedFileID: "malformed"}, false},
		{"creation cannot select existing identity", WorkspaceFileOperation{Kind: WorkspaceCreateFile, Path: "new", ExpectedFileID: "26d9d04d-5c45-40b6-bb58-c60e7d59433c"}, false},
		{"restore uses recovery identity", WorkspaceFileOperation{Kind: WorkspaceRestore, Path: "recovered", RecoveryID: "original-delete", ExpectedFileID: "26d9d04d-5c45-40b6-bb58-c60e7d59433c"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.operation.Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}
