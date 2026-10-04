package daemon

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimeRegistrationReportsPublicHostDetails(t *testing.T) {
	for _, builtinOnly := range []bool{false, true} {
		name := "workspace"
		if builtinOnly {
			name = "builtin_refresh"
		}
		t.Run(name, func(t *testing.T) {
			reports := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				response := map[string]any{}
				if r.URL.Path == "/api/daemon/register" {
					var report map[string]any
					if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					reports <- report
					response["runtimes"] = []map[string]string{{"id": "runtime", "provider": "codex"}}
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Errorf("encode registration response: %v", err)
				}
			}))
			defer server.Close()
			d := &Daemon{
				cfg:    Config{ServerBaseURL: "https://user:private@api.example.test/base/?token=secret", CLIVersion: "1.0.24", LaunchedBy: "desktop"},
				client: NewClient(server.URL), logger: slog.New(slog.DiscardHandler),
			}
			builtins := []map[string]string{{"name": "codex", "type": "codex", "version": "0.118.0"}}
			var err error
			if builtinOnly {
				_, err = d.registerBuiltinRuntimesForWorkspaceLocked(t.Context(), "workspace", builtins)
			} else {
				_, _, err = d.registerRuntimesForWorkspaceBatchLocked(t.Context(), "workspace", builtins)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case report := <-reports:
				if report["server_url"] != "https://api.example.test/base" || report["cli_version"] != "1.0.24" || report["launched_by"] != "desktop" {
					t.Fatalf("host registration details = %v", report)
				}
			default:
				t.Fatal("registration did not reach the server")
			}
		})
	}
}
