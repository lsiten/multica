package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestApplicationCLILocalRoutingAndUncertainResult(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const runtimeID = "22222222-2222-4222-8222-222222222222"
	const workspaceID = "33333333-3333-4333-8333-333333333333"
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "uncertain"}[fail], func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer mul_local" || r.Header.Get("X-Multica-Profile") != "local" {
					t.Error("local scope headers missing")
				}
				if r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(map[string]any{"applications": []map[string]any{{"application_id": appID, "runtime_id": runtimeID, "workspace_id": workspaceID, "revision": 1}}})
					return
				}
				posts++
				var input map[string]any
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if input["action"] != "restart" {
					t.Error("action changed")
				}
				if fail {
					http.Error(w, "receipt pending", 409)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"command_id": input["command_id"], "sync_pending": true})
			}))
			defer server.Close()
			client := cli.NewAPIClient("https://backend.invalid", workspaceID, "mul_local")
			body := map[string]any{"action": "restart", "runtime_id": runtimeID, "revision": float64(1), "idempotency_key": "caller-stable-key"}
			result, handled, err := localApplicationControl(t.Context(), localApplicationHTTPClient(time.Second), server.URL, "local", client, appID, body)
			if !handled || (err != nil) != fail || posts != 1 {
				t.Fatalf("handled=%v err=%v posts=%d", handled, err, posts)
			}
			if !fail && (result["state"] != "completed" || result["local"] != true) {
				t.Fatalf("bad result=%v", result)
			}
			body["action"] = "publish"
			_, handled, err = localApplicationControl(t.Context(), localApplicationHTTPClient(time.Second), server.URL, "local", client, appID, body)
			if handled || err != nil || posts != 1 {
				t.Fatal("publish used local authority")
			}
		})
	}
}
