package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestVoiceHandlerAuthorizationAndValidation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	h := *testHandler
	h.DaemonHub = daemonws.NewHub()
	runtimeID := dbfx.Runtime(t, "voice runtime", testutil.Cols{"daemon_id": "voice-daemon", "visibility": "public", "metadata": testutil.Raw(`'{"capabilities":["voice-transcription-v1"]}'::jsonb`)})
	agentID := dbfx.Agent(t, "voice agent", runtimeID)
	call := func(id string, body any, status int) {
		t.Helper()
		req := withURLParam(newRequest(http.MethodPost, "/voice/transcribe", body), "id", id)
		response := testutil.Call(t, h.TranscribeChatVoice, req).Want(status)
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store header")
		}
	}
	call(agentID, map[string]string{"mime_type": "text/html", "audio_base64": "YQ=="}, http.StatusBadRequest)
	call(agentID, map[string]string{"mime_type": "audio/webm", "audio_base64": "YQ=="}, http.StatusServiceUnavailable)
	missing := dbfx.Agent(t, "unbound voice agent", "")
	call(missing, nil, http.StatusConflict)
	archived := dbfx.Agent(t, "archived voice agent", runtimeID, testutil.Cols{"archived_at": testutil.Raw("now()")})
	call(archived, nil, http.StatusForbidden)
	oldRuntime := dbfx.Runtime(t, "old voice runtime")
	oldAgent := dbfx.Agent(t, "old voice agent", oldRuntime)
	call(oldAgent, nil, http.StatusNotImplemented)
	other := dbfx.User(t, "voice stranger", "voice-stranger@example.test")
	dbfx.Member(t, testWorkspaceID, other, "member")
	req := withURLParam(newRequestAsUser(other, http.MethodPost, "/voice/transcribe", nil), "id", agentID)
	testutil.Call(t, h.TranscribeChatVoice, req).Want(http.StatusForbidden)
}
