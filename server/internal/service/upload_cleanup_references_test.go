package service

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/storage"
)

func TestUploadCleanupReferencesParseStoredURLForms(t *testing.T) {
	for _, document := range []string{
		`![file](https://old.example.test/uploads/workspaces/ws/channel/hash.png)`,
		`<img src="/uploads/workspaces/ws/channel/hash.png">`,
		`{"content":"![file](https:\/\/old.example.test\/uploads\/workspaces\/ws\/channel\/hash.png)"}`,
		`/uploads/workspaces/ws/channel/hash.png?download=1&amp;token=example`,
		`/uploads/workspaces/ws/channel/%68ash.png`,
		"/uploads/workspaces/ws/channel/hash.png\nnext line",
	} {
		t.Run(document, func(t *testing.T) {
			// Given
			t.Setenv("LOCAL_UPLOAD_DIR", t.TempDir())
			t.Setenv("LOCAL_UPLOAD_BASE_URL", "https://current.example.test")
			store := storage.NewLocalStorageFromEnv()
			refs := newUploadReferences()

			// When
			refs.add(store, document)

			// Then
			if !refs.keys["workspaces/ws/channel/hash.png"] {
				t.Fatalf("missing storage key in references: %+v", refs.keys)
			}
		})
	}
}
