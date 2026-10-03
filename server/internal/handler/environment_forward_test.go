package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestEnvironmentForwardRequiresHumanRuntimeOwnerAndWorkspace(t *testing.T) {
	member := dbfx.User(t, "Environment Reader", "environment-reader@example.test")
	dbfx.Member(t, testWorkspaceID, member, "member")
	runtime := dbfx.Runtime(t, "environment-runtime", testutil.Cols{"runtime_mode": "local", "visibility": "public"})
	for _, tc := range []struct {
		name, user, actor, body string
		status                  int
	}{
		{"anonymous", "", "", `{"action":"inventory"}`, http.StatusUnauthorized},
		{"member", member, "", `{"action":"inventory"}`, http.StatusForbidden},
		{"task credential", testUserID, "task_token", `{"action":"inventory"}`, http.StatusForbidden},
		{"arbitrary path", testUserID, "", `{"action":"inventory","path":"/"}`, http.StatusBadRequest},
		{"ambiguous body", testUserID, "", `{"action":"inventory"}{"action":"operation_cancel"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := newRequestAsUser(tc.user, http.MethodPost, "/environments/execute", nil)
			request.Body = io.NopCloser(strings.NewReader(tc.body))
			request.Header.Set("X-Actor-Source", tc.actor)
			request = testutil.WithURLParams(request, "runtimeId", runtime)
			var handler http.Handler = http.HandlerFunc(testHandler.ForwardRuntimeEnvironment)
			if tc.user != "" {
				handler = middleware.RequireWorkspaceMember(testHandler.Queries)(handler)
			}
			testutil.Call(t, handler.ServeHTTP, request).Want(tc.status)
			if len(testHandler.localReviewRelay.pending) != 0 {
				t.Fatal("rejected request reached daemon")
			}
		})
	}
}

func TestEnvironmentForwardUsesOwningRuntimeRelayWithoutCloudPaths(t *testing.T) {
	runtime := dbfx.Runtime(t, "environment-owned", testutil.Cols{"runtime_mode": "local"})
	request := newRequestAsUser(testUserID, http.MethodPost, "/environments/execute", protocol.EnvironmentCommand{Action: "inventory"})
	request = testutil.WithURLParams(request, "runtimeId", runtime)
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	request = request.WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		middleware.RequireWorkspaceMember(testHandler.Queries)(http.HandlerFunc(testHandler.ForwardRuntimeEnvironment)).ServeHTTP(response, request)
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	var command *protocol.LocalReviewCommand
	for command == nil {
		command = testHandler.localReviewRelay.claim(testWorkspaceID, runtime)
		if command != nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("relay command unavailable")
		case <-ticker.C:
		}
	}
	if command.ActorID != testUserID || command.Environment == nil || command.Environment.Action != "inventory" || command.Path != "" || command.TaskID != "" {
		t.Fatalf("invalid forwarding envelope: %+v", command)
	}
	if !testHandler.localReviewRelay.complete(testWorkspaceID, runtime, command.ID, protocol.LocalReviewResult{ClaimToken: command.ClaimToken, Page: json.RawMessage(`[]`)}) {
		t.Fatal("owner result rejected")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("forwarding did not finish")
	}
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatalf("response: %d %s", response.Code, response.Body.String())
	}
}
