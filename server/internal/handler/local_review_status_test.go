package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReviewStatusChecksCredentialRuntimeAndClaim(t *testing.T) {
	runtime := dbfx.Runtime(t, "status-owner", testutil.Cols{"runtime_mode": "local", "visibility": "public", "daemon_id": "status-daemon"})
	otherRuntime := dbfx.Runtime(t, "status-other", testutil.Cols{"runtime_mode": "local"})
	member := dbfx.User(t, "Status Reader", "status-reader@example.test")
	dbfx.Member(t, testWorkspaceID, member, "member")
	exchange, err := testHandler.localReviewRelay.enqueue(protocol.LocalReviewCommand{WorkspaceID: testWorkspaceID, RuntimeID: runtime, Action: "file"})
	if err != nil {
		t.Fatal(err)
	}
	claim := testHandler.localReviewRelay.claim(testWorkspaceID, runtime)
	if claim == nil {
		t.Fatal("missing claim")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := testHandler.localReviewRelay.wait(ctx, exchange); err == nil {
			t.Error("expected cancelled exchange")
		}
	})
	for _, tc := range []struct {
		name, user, daemon, runtime, token string
		status                             int
		active                             bool
	}{
		{"owner", testUserID, "", runtime, claim.ClaimToken, http.StatusOK, true},
		{"daemon", "", "status-daemon", runtime, claim.ClaimToken, http.StatusOK, true},
		{"other member", member, "", runtime, claim.ClaimToken, http.StatusForbidden, false},
		{"other daemon", "", "foreign-daemon", runtime, claim.ClaimToken, http.StatusForbidden, false},
		{"other runtime", testUserID, "", otherRuntime, claim.ClaimToken, http.StatusOK, false},
		{"wrong token", testUserID, "", runtime, "wrong", http.StatusOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := protocol.LocalReviewStatusRequest{ClaimToken: tc.token}
			r := newRequestAsUser(tc.user, http.MethodPost, "/status", body)
			if tc.daemon != "" {
				r = newDaemonTokenRequest(http.MethodPost, "/status", body, testWorkspaceID, tc.daemon)
			}
			r = testutil.WithURLParams(r, "runtimeId", tc.runtime, "commandId", claim.ID)
			response := testutil.Call(t, testHandler.LocalReviewRelayStatus, r).Want(tc.status)
			if tc.status == http.StatusOK {
				var result protocol.LocalReviewStatus
				response.JSON(&result)
				if result.Active == nil || *result.Active != tc.active {
					t.Fatal("wrong active status")
				}
			}
		})
	}
}
