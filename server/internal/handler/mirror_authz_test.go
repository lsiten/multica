package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMirrorSessionEndpointsRejectPrivateRuntimeForNonOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	const daemonID = "mirror-authz-daemon"
	const viewerID = "mirror-authz-viewer"
	runtimeID := dbfx.Runtime(t, "Mirror authz runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"daemon_id":    daemonID,
		"provider":     "mirror_authz",
		"device_info":  "Mirror authz runtime",
		"metadata":     testutil.Raw(`'{"capabilities":["screen-mirror-v1"]}'::jsonb`),
		"visibility":   "private",
		"status":       "online",
	})

	h := *testHandler
	h.DaemonHub = daemonws.NewHub()
	h.MirrorSessions = mirror.NewSessionStore(mirror.SessionStoreOptions{})
	daemonIdentity := daemonws.ClientIdentity{
		DaemonID:     daemonID,
		WorkspaceID:  testWorkspaceID,
		WorkspaceIDs: []string{testWorkspaceID},
		RuntimeIDs:   []string{runtimeID},
	}
	connectMirrorViewerDaemon(t, &h, daemonIdentity)

	body := mirrorSessionRequestBody(viewerID)
	basePath := "/api/runtimes/" + runtimeID + "/mirror/sessions"
	createPath := basePath + "?viewer_id=" + viewerID
	sessionID := createMirrorSessionForTest(t, &h, runtimeID, createPath, body)
	otherUserID := createSecondWorkspaceMember(t)
	sessionPath := mirrorSessionPath(basePath, sessionID, viewerID)

	testutil.Call(t, h.CreateMirrorSession, withURLParam(
		newRequestAs(otherUserID, http.MethodPost, createPath, body), "runtimeId", runtimeID,
	)).Want(http.StatusNotFound)
	testutil.Call(t, h.GetMirrorSession, withURLParams(
		newRequestAs(otherUserID, http.MethodGet, sessionPath, nil),
		"runtimeId", runtimeID, "sessionId", sessionID,
	)).Want(http.StatusNotFound)
	testutil.Call(t, h.CloseMirrorSession, withURLParams(
		newRequestAs(otherUserID, http.MethodDelete, sessionPath, nil),
		"runtimeId", runtimeID, "sessionId", sessionID,
	)).Want(http.StatusNotFound)
	testutil.Call(t, h.GetMirrorSession, withURLParams(
		newRequest(http.MethodGet, sessionPath, nil),
		"runtimeId", runtimeID, "sessionId", sessionID,
	)).Want(http.StatusOK)
	testutil.Call(t, h.CloseMirrorSession, withURLParams(
		newRequest(http.MethodDelete, sessionPath, nil),
		"runtimeId", runtimeID, "sessionId", sessionID,
	)).Want(http.StatusNoContent)
}

func TestMirrorSessionsOnPublicRuntimeAreUserScoped(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	const daemonID = "mirror-public-authz-daemon"
	const viewerID = "mirror-public-viewer"
	runtimeID := dbfx.Runtime(t, "Mirror public authz runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"daemon_id":    daemonID,
		"provider":     "mirror_public_authz",
		"device_info":  "Mirror public authz runtime",
		"metadata":     testutil.Raw(`'{"capabilities":["screen-mirror-v1"]}'::jsonb`),
		"visibility":   "public",
		"status":       "online",
	})

	h := *testHandler
	h.DaemonHub = daemonws.NewHub()
	h.MirrorSessions = mirror.NewSessionStore(mirror.SessionStoreOptions{})
	daemonIdentity := daemonws.ClientIdentity{
		DaemonID:     daemonID,
		WorkspaceID:  testWorkspaceID,
		WorkspaceIDs: []string{testWorkspaceID},
		RuntimeIDs:   []string{runtimeID},
	}
	connectMirrorViewerDaemon(t, &h, daemonIdentity)

	basePath := "/api/runtimes/" + runtimeID + "/mirror/sessions"
	createPath := basePath + "?viewer_id=" + viewerID
	sessionID := createMirrorSessionForTest(t, &h, runtimeID, createPath, mirrorSessionRequestBody(viewerID))
	if err := h.HandleDaemonMirrorAnswer(context.Background(), daemonIdentity, protocol.MirrorAnswerPayload{
		SessionID:   sessionID,
		WorkspaceID: testWorkspaceID,
		RuntimeID:   runtimeID,
		UserID:      testUserID,
		DaemonID:    daemonID,
		ViewerID:    viewerID,
		Answer:      protocol.MirrorSessionDescription{Type: "answer", SDP: "answer-sdp"},
	}); err != nil {
		t.Fatalf("answer owner mirror session: %v", err)
	}

	otherUserID := createSecondWorkspaceMember(t)
	sessionPath := mirrorSessionPath(basePath, sessionID, viewerID)
	testutil.Call(t, h.GetMirrorSession, withURLParams(
		newRequestAs(otherUserID, http.MethodGet, sessionPath, nil),
		"runtimeId", runtimeID, "sessionId", sessionID,
	)).Want(http.StatusNotFound)
	testutil.Call(t, h.CloseMirrorSession, withURLParams(
		newRequestAs(otherUserID, http.MethodDelete, sessionPath, nil),
		"runtimeId", runtimeID, "sessionId", sessionID,
	)).Want(http.StatusNotFound)

	ownerResp := testutil.Call(t, h.GetMirrorSession, withURLParams(
		newRequest(http.MethodGet, sessionPath, nil),
		"runtimeId", runtimeID, "sessionId", sessionID,
	)).Want(http.StatusOK)
	var ownerSession struct {
		Answer *struct {
			SDP string `json:"sdp"`
		} `json:"answer"`
	}
	ownerResp.JSON(&ownerSession)
	if ownerSession.Answer == nil || ownerSession.Answer.SDP != "answer-sdp" {
		t.Fatalf("owner session answer = %+v, want retained answer SDP", ownerSession.Answer)
	}
}

func TestHandleDaemonMirrorAnswerRejectsForeignDaemonScope(t *testing.T) {
	const (
		workspaceID = "workspace-mirror-authz"
		runtimeID   = "runtime-mirror-authz"
		daemonID    = "daemon-mirror-authz"
		viewerID    = "viewer-mirror-authz"
	)
	sessions := mirror.NewSessionStore(mirror.SessionStoreOptions{})
	sessionIdentity := mirror.SessionIdentity{
		WorkspaceID: workspaceID,
		RuntimeID:   runtimeID,
		UserID:      "viewer-user",
		DaemonID:    daemonID,
		ViewerID:    viewerID,
	}
	session, err := sessions.Create(context.Background(), mirror.CreateSessionInput{
		Identity: sessionIdentity,
		Offer:    protocol.MirrorSessionDescription{Type: "offer", SDP: "offer-sdp"},
	})
	if err != nil {
		t.Fatalf("create mirror session: %v", err)
	}
	if _, err := sessions.ConsumeOffer(context.Background(), session.ID, sessionIdentity); err != nil {
		t.Fatalf("consume mirror offer: %v", err)
	}

	h := &Handler{MirrorSessions: sessions}
	answer := protocol.MirrorSessionDescription{Type: "answer", SDP: "answer-sdp"}
	tests := []struct {
		name     string
		identity daemonws.ClientIdentity
	}{
		{
			name: "different runtime scope",
			identity: daemonws.ClientIdentity{
				DaemonID:     daemonID,
				WorkspaceIDs: []string{workspaceID},
				RuntimeIDs:   []string{"runtime-foreign"},
			},
		},
		{
			name: "different daemon id on same runtime",
			identity: daemonws.ClientIdentity{
				DaemonID:     "foreign-daemon",
				WorkspaceIDs: []string{workspaceID},
				RuntimeIDs:   []string{runtimeID},
			},
		},
		{
			name: "foreign workspace scope",
			identity: daemonws.ClientIdentity{
				DaemonID:     daemonID,
				WorkspaceIDs: []string{"workspace-foreign"},
				RuntimeIDs:   []string{runtimeID},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := mirrorAnswerPayload(session.ID, sessionIdentity, answer)
			err := h.HandleDaemonMirrorAnswer(context.Background(), tt.identity, payload)
			if !errors.Is(err, mirror.ErrSessionIdentityMismatch) {
				t.Fatalf("foreign answer error = %v, want %v", err, mirror.ErrSessionIdentityMismatch)
			}
		})
	}

	authorized := daemonws.ClientIdentity{
		DaemonID:     daemonID,
		WorkspaceIDs: []string{workspaceID},
		RuntimeIDs:   []string{runtimeID},
	}
	if err := h.HandleDaemonMirrorAnswer(
		context.Background(), authorized, mirrorAnswerPayload(session.ID, sessionIdentity, answer),
	); err != nil {
		t.Fatalf("authorized daemon answer after rejected foreign answers: %v", err)
	}
}

func mirrorSessionRequestBody(viewerID string) map[string]any {
	return map[string]any{
		"viewer_id": viewerID,
		"offer": map[string]string{
			"type": "offer",
			"sdp":  "offer-sdp",
		},
	}
}

func createMirrorSessionForTest(t *testing.T, h *Handler, runtimeID, path string, body map[string]any) string {
	t.Helper()
	resp := testutil.Call(t, h.CreateMirrorSession, withURLParam(
		newRequest(http.MethodPost, path, body), "runtimeId", runtimeID,
	)).Want(http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	resp.JSON(&created)
	if created.ID == "" {
		t.Fatal("created mirror session id is empty")
	}
	return created.ID
}

func mirrorSessionPath(basePath, sessionID, viewerID string) string {
	return basePath + "/" + sessionID + "?viewer_id=" + viewerID
}

func mirrorAnswerPayload(sessionID string, identity mirror.SessionIdentity, answer protocol.MirrorSessionDescription) protocol.MirrorAnswerPayload {
	return protocol.MirrorAnswerPayload{
		SessionID:   sessionID,
		WorkspaceID: identity.WorkspaceID,
		RuntimeID:   identity.RuntimeID,
		UserID:      identity.UserID,
		DaemonID:    identity.DaemonID,
		ViewerID:    identity.ViewerID,
		Answer:      answer,
	}
}
