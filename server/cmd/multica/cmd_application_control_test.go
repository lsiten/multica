package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const applicationCLIAppID = "11111111-1111-4111-8111-111111111111"
const applicationCLIOperationID = "22222222-2222-4222-8222-222222222222"
const applicationCLIInstanceID = "33333333-3333-4333-8333-333333333333"

func applicationCLIEnvironment(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("MULTICA_AGENT_ID", "")
	t.Setenv("MULTICA_TASK_ID", "")
	t.Setenv("MULTICA_DAEMON_PORT", "")
}

func TestApplicationCLIControlPreservesExecutionIdentity(t *testing.T) {
	applicationCLIEnvironment(t)
	for _, action := range []string{"start", "stop", "restart", "publish", "unpublish"} {
		t.Run(action, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/applications/"+applicationCLIAppID+"/operations" {
					t.Errorf("request=%s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["action"] != action || body["idempotency_key"] != "same-request" || body["runtime_id"] != applicationCLIInstanceID || body["revision"] != float64(7) {
					t.Errorf("execution request changed: %v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"id": applicationCLIOperationID, "state": "completed", "steps": []any{}}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			setCLITestServerEnv(t, server.URL)
			command := applicationCommand()
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetArgs([]string{action, applicationCLIAppID, "--body", `{"revision":7,"runtime_id":"` + applicationCLIInstanceID + `","idempotency_key":"same-request","placements":{}}`, "--wait"})
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), `"state": "completed"`) {
				t.Fatalf("missing confirmed result: %s", output.String())
			}
		})
	}
}

func TestApplicationCLIReadsInstanceLogsAndOperationProgress(t *testing.T) {
	applicationCLIEnvironment(t)
	for _, test := range []struct {
		name  string
		args  []string
		path  string
		query string
		body  string
	}{
		{"logs", []string{"logs", applicationCLIAppID, "--instance", applicationCLIInstanceID, "--cursor", "host:1:32", "--limit", "4096"}, "/api/applications/" + applicationCLIAppID + "/instances/" + applicationCLIInstanceID + "/logs", "cursor=host%3A1%3A32&limit=4096", `{"text":"next page","cursor":"host:1:41","gap":false}`},
		{"operation", []string{"operation", applicationCLIAppID, applicationCLIOperationID}, "/api/applications/" + applicationCLIAppID + "/operations/" + applicationCLIOperationID, "", `{"id":"` + applicationCLIOperationID + `","state":"running"}`},
		{"status", []string{"status"}, "/api/applications/board", "", `{"instances":[],"applications":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != test.path || r.URL.RawQuery != test.query {
					t.Errorf("request=%s %s", r.Method, r.URL.String())
				}
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(test.body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			setCLITestServerEnv(t, server.URL)
			command := applicationCommand()
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetArgs(test.args)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if !json.Valid(output.Bytes()) {
				t.Fatalf("output is not JSON: %s", output.String())
			}
		})
	}
}

func TestApplicationCLIWaitReportsFailureAndHonorsCancellation(t *testing.T) {
	applicationCLIEnvironment(t)
	for _, state := range []string{"partial", "failed", "queued"} {
		t.Run(state, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]string{"id": applicationCLIOperationID, "state": state}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			setCLITestServerEnv(t, server.URL)
			command := applicationCommand()
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&bytes.Buffer{})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			command.SetContext(ctx)
			command.SetArgs([]string{"operation", applicationCLIAppID, applicationCLIOperationID, "--wait"})
			started := time.Now()
			err := command.Execute()
			if err == nil {
				t.Fatal("unsuccessful operation reported success")
			}
			if state == "queued" {
				if time.Since(started) > time.Second || !strings.Contains(err.Error(), applicationCLIOperationID) {
					t.Fatalf("wait did not preserve resumable identity: %v", err)
				}
			} else if !strings.Contains(output.String(), `"state": "`+state+`"`) {
				t.Fatalf("terminal evidence missing: %s", output.String())
			}
		})
	}
}

func TestApplicationCLICancelWaitConfirmsCancellationWithoutReportingFailure(t *testing.T) {
	applicationCLIEnvironment(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/applications/"+applicationCLIAppID+"/operations/"+applicationCLIOperationID+"/cancel" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"id": applicationCLIOperationID, "state": "cancelled"}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	setCLITestServerEnv(t, server.URL)
	command := applicationCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"cancel", applicationCLIAppID, applicationCLIOperationID, "--wait"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"state": "cancelled"`) {
		t.Fatalf("confirmed cancellation missing: %s", output.String())
	}
}
