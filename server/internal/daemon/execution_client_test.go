package daemon

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestExecutionCapabilityOldAndSupportedServers(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		supported  bool
	}{
		{"old missing endpoint", `{}`, 404, false},
		{"old ignores capability", `{"instance_id":"control","epoch":1}`, 200, false},
		{"supported", `{"instance_id":"control","epoch":1,"capabilities":["execution-reconciliation-v1"]}`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); w.Write([]byte(tc.body)) }))
			defer server.Close()
			response, err := NewClient(server.URL).AcquireExecutionSupervisor(context.Background(), "runtime", protocol.SupervisorRequest{InstanceID: "control"})
			if tc.supported {
				if err != nil || response.Epoch != 1 {
					t.Fatalf("capability: response=%v err=%v", response, err)
				}
			} else if !errors.Is(err, ErrExecutionUnsupported) {
				t.Fatalf("old server must stay drain-only: %v", err)
			}
		})
	}
}

func TestExecutionClientCannotUseAccountOrControlAuthority(t *testing.T) {
	identity := protocol.ExecutionIdentity{TaskID: uuid.NewString(), RuntimeID: uuid.NewString(), WorkerID: uuid.NewString(), ExecutionID: uuid.NewString(), DispatchedAt: time.Now()}
	if _, err := NewExecutionClient("http://127.0.0.1", protocol.ExecutionGrantResponse{Token: "mul_account", Identity: identity, ExpiresAt: time.Now().Add(time.Minute)}); err == nil {
		t.Fatal("accepted account credential")
	}
	token := "mwt_" + strings.Repeat("a", 64)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/daemon/tasks/"+identity.TaskID+"/usage" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("wrong transport scope: %s", r.URL.Path)
		}
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client, err := NewExecutionClient(server.URL, protocol.ExecutionGrantResponse{Token: token, Identity: identity, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Callback(context.Background(), "reconcile", nil, nil); err == nil || calls != 0 {
		t.Fatal("control operation reached server")
	}
	if err = client.Callback(context.Background(), "usage", map[string]any{"usage": []any{}}, nil); err != nil || calls != 1 {
		t.Fatalf("scoped usage: %v calls=%d", err, calls)
	}
}

func TestExecutionClientRejectsMalformedAuthority(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer server.Close()
	client := NewClient(server.URL)
	if _, err := client.BindTaskExecution(context.Background(), uuid.NewString(), uuid.NewString(), protocol.BindExecutionRequest{WorkerID: uuid.NewString(), DispatchedAt: time.Now()}); !errors.Is(err, errInvalidResponseBody) {
		t.Fatalf("malformed bind: %v", err)
	}
	if _, err := client.IssueExecutionGrant(context.Background(), uuid.NewString(), uuid.NewString(), protocol.ExecutionGrantRequest{ExecutionID: uuid.NewString()}); !errors.Is(err, errInvalidResponseBody) {
		t.Fatalf("malformed grant: %v", err)
	}
}
