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

func TestRuntimeVoiceHandlerRequiresCapability(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	h := *testHandler
	h.DaemonHub = daemonws.NewHub()
	runtimeID := dbfx.Runtime(t, "meeting voice runtime", testutil.Cols{"daemon_id": "meeting-daemon", "visibility": "public", "metadata": testutil.Raw(`'{}'::jsonb`)})
	req := withURLParam(newRequest(http.MethodPost, "/voice/transcribe", map[string]string{"mime_type": "audio/webm", "audio_base64": "YQ=="}), "runtimeId", runtimeID)
	testutil.Call(t, h.TranscribeRuntimeVoice, req).Want(http.StatusNotImplemented)
}

func TestVoiceLocaleRequiresRuntimeSupportWithoutBreakingLegacyAudio(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	h := *testHandler
	h.DaemonHub = daemonws.NewHub()
	for _, tc := range []struct {
		name, metadata, language, reason string
		status                           int
	}{
		{"legacy audio", `'{"capabilities":["voice-transcription-v1"]}'::jsonb`, "", "daemon_unavailable", http.StatusServiceUnavailable},
		{"locale on old runtime", `'{"capabilities":["voice-transcription-v1"]}'::jsonb`, "en", "voice_upgrade_required", http.StatusNotImplemented},
		{"locale on updated runtime", `'{"capabilities":["voice-transcription-v1","voice-locale-v1"]}'::jsonb`, "en", "daemon_unavailable", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimeID := dbfx.Runtime(t, "locale voice runtime", testutil.Cols{"daemon_id": "locale-daemon", "visibility": "public", "metadata": testutil.Raw(tc.metadata)})
			agentID := dbfx.Agent(t, "locale voice agent", runtimeID)
			body := map[string]string{"mime_type": "audio/webm", "audio_base64": "YQ=="}
			if tc.language != "" {
				body["language"] = tc.language
			}
			for _, target := range []struct {
				name, param, id string
				handle          http.HandlerFunc
			}{{"chat", "id", agentID, h.TranscribeChatVoice}, {"meeting", "runtimeId", runtimeID, h.TranscribeRuntimeVoice}} {
				t.Run(target.name, func(t *testing.T) {
					req := withURLParam(newRequest(http.MethodPost, "/voice/transcribe", body), target.param, target.id)
					var result struct {
						Error string `json:"error"`
					}
					testutil.Call(t, target.handle, req).Want(tc.status).JSON(&result)
					if result.Error != tc.reason {
						t.Fatalf("reason=%q want=%q", result.Error, tc.reason)
					}
				})
			}
		})
	}
}
