package handler

import (
	"crypto/sha256"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

type mirrorNetworkWire struct {
	Source         string `json:"source"`
	Locked         bool   `json:"locked"`
	CanManage      bool   `json:"can_manage"`
	TURNConfigured bool   `json:"turn_configured"`
	Mode           string `json:"mode"`
	Builtin        struct {
		Enabled    bool     `json:"enabled"`
		Available  bool     `json:"available"`
		Host       string   `json:"host"`
		Port       int      `json:"port"`
		Transports []string `json:"transports"`
	} `json:"builtin"`
	Custom []struct {
		URLs          []string `json:"urls"`
		Username      string   `json:"username"`
		HasCredential bool     `json:"has_credential"`
	} `json:"custom"`
}

func mirrorNetworkRequest(t *testing.T, method string, body any) *http.Request {
	t.Helper()
	req := newRequest(method, "/api/workspaces/"+testWorkspaceID+"/mirror/network", body)
	return withURLParam(req, "id", testWorkspaceID)
}

func mirrorNetworkTestHandler(t *testing.T) Handler {
	t.Helper()
	h := *testHandler
	sum := sha256.Sum256([]byte("mirror-network:test-secret"))
	box, err := secretbox.New(sum[:])
	if err != nil {
		t.Fatalf("secretbox: %v", err)
	}
	h.cfg.MirrorNetworkSecretBox = box
	return h
}

func TestGetWorkspaceMirrorNetworkDefaults(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	var got mirrorNetworkWire
	testutil.Call(t, testHandler.GetWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodGet, nil),
	).Want(http.StatusOK).JSON(&got)

	if got.Mode != "builtin" {
		t.Fatalf("mode = %q, want builtin", got.Mode)
	}
	if !got.CanManage || got.Locked {
		t.Fatalf("can_manage=%v locked=%v, want true/false", got.CanManage, got.Locked)
	}
}

func TestUpdateWorkspaceMirrorNetworkLifecycle(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	h := mirrorNetworkTestHandler(t)
	t.Cleanup(func() {
		testutil.Call(t, h.UpdateWorkspaceMirrorNetwork,
			mirrorNetworkRequest(t, http.MethodPatch, map[string]any{"mode": "builtin"}),
		).Want(http.StatusOK)
	})

	var disabled mirrorNetworkWire
	testutil.Call(t, h.UpdateWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodPatch, map[string]any{"mode": "disabled"}),
	).Want(http.StatusOK).JSON(&disabled)
	if disabled.Mode != "disabled" || disabled.Source != "disabled" || disabled.TURNConfigured {
		t.Fatalf("disabled response = %+v", disabled)
	}

	// A STUN-only custom set is rejected: the collection must contain TURN.
	testutil.Call(t, h.UpdateWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodPatch, map[string]any{
			"mode": "custom",
			"servers": []map[string]any{
				{"urls": []string{"stun:stun.example.com:3478"}},
			},
		}),
	).Want(http.StatusBadRequest)

	// A mixed STUN + TURN set is accepted, and the credential is stored but
	// never echoed back in plaintext.
	var custom mirrorNetworkWire
	testutil.Call(t, h.UpdateWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodPatch, map[string]any{
			"mode": "custom",
			"servers": []map[string]any{
				{"urls": "stun:stun.example.com:3478"},
				{
					"urls":       []string{"turn:turn.example.com:3478?transport=udp"},
					"username":   "viewer",
					"credential": "topsecret",
				},
			},
		}),
	).Want(http.StatusOK).JSON(&custom)
	if custom.Mode != "custom" || custom.Source != "custom" || !custom.TURNConfigured {
		t.Fatalf("custom response = %+v", custom)
	}
	if len(custom.Custom) != 2 || !custom.Custom[1].HasCredential {
		t.Fatalf("custom servers = %+v, want 2 servers with stored credential", custom.Custom)
	}

	// Omitting credential preserves the stored secret; an empty string clears it.
	testutil.Call(t, h.UpdateWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodPatch, map[string]any{
			"mode": "custom",
			"servers": []map[string]any{
				{"urls": "turn:turn.example.com:3478?transport=udp", "username": "viewer"},
			},
		}),
	).Want(http.StatusOK)
	var kept mirrorNetworkWire
	testutil.Call(t, h.GetWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodGet, nil),
	).Want(http.StatusOK).JSON(&kept)
	if len(kept.Custom) != 1 || !kept.Custom[0].HasCredential {
		t.Fatalf("credential was not preserved: %+v", kept.Custom)
	}

	testutil.Call(t, h.UpdateWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodPatch, map[string]any{
			"mode": "custom",
			"servers": []map[string]any{
				{"urls": "turn:turn.example.com:3478?transport=udp", "username": "viewer", "credential": ""},
			},
		}),
	).Want(http.StatusOK)
	var cleared mirrorNetworkWire
	testutil.Call(t, h.GetWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodGet, nil),
	).Want(http.StatusOK).JSON(&cleared)
	if len(cleared.Custom) != 1 || cleared.Custom[0].HasCredential {
		t.Fatalf("credential was not cleared: %+v", cleared.Custom)
	}
}

func TestUpdateWorkspaceMirrorNetworkRejectsLockedDeployment(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	h := *testHandler
	h.cfg.MirrorICE = mirror.ICEPlan{
		ICEServers: []mirror.ICEServer{{
			URLs: []string{"turn:locked.example.com:3478"},
		}},
		TURNConfigured: true,
	}
	testutil.Call(t, h.UpdateWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodPatch, map[string]any{"mode": "disabled"}),
	).Want(http.StatusConflict)
}
