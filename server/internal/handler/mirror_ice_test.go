package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestParseMirrorICEPlan(t *testing.T) {
	raw := `[
		{"urls":"stun:stun.example.com:3478"},
		{"urls":["turn:turn.example.com:3478","turns:turns.example.com:5349"],"username":"viewer","credential":"secret"}
	]`

	plan, err := ParseMirrorICEPlan(raw)
	if err != nil {
		t.Fatalf("parse plan: %v", err)
	}
	if !plan.TURNConfigured {
		t.Fatal("TURNConfigured = false, want true")
	}
	if len(plan.ICEServers) != 2 {
		t.Fatalf("servers = %d, want 2", len(plan.ICEServers))
	}
	if got := plan.ICEServers[1].URLs[0]; got != "turn:turn.example.com:3478" {
		t.Fatalf("first TURN url = %q", got)
	}
}

func TestParseMirrorICEPlanRejectsMalformedURLs(t *testing.T) {
	if _, err := ParseMirrorICEPlan(`[{"urls":42}]`); err == nil {
		t.Fatal("malformed ICE plan unexpectedly accepted")
	}
}

func TestGetMirrorICEConfigReturnsDeploymentPlan(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "Mirror ICE runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"provider":     "mirror_ice",
		"visibility":   "private",
	})
	h := *testHandler
	h.cfg.MirrorICE = mirror.ICEPlan{
		ICEServers: []mirror.ICEServer{{
			URLs:       []string{"turn:turn.example.com:3478"},
			Username:   "viewer",
			Credential: "secret",
		}},
		TURNConfigured: true,
	}

	var got struct {
		TURNConfigured bool `json:"turn_configured"`
		ICEServers     []struct {
			URLs       []string `json:"urls"`
			Username   string   `json:"username"`
			Credential string   `json:"credential"`
		} `json:"ice_servers"`
	}
	testutil.Call(t, h.GetMirrorICEConfig, withURLParam(
		newRequest(http.MethodGet, "/api/runtimes/"+runtimeID+"/mirror/config", nil),
		"runtimeId", runtimeID,
	)).Want(http.StatusOK).JSON(&got)

	if !got.TURNConfigured || len(got.ICEServers) != 1 {
		t.Fatalf("ICE config = %+v, want one configured TURN server", got)
	}
	if server := got.ICEServers[0]; server.Username != "viewer" ||
		server.Credential != "secret" ||
		len(server.URLs) != 1 || server.URLs[0] != "turn:turn.example.com:3478" {
		t.Fatalf("ICE server = %+v, want deployment TURN configuration", server)
	}
}

func TestGetMirrorICEConfigRejectsPrivateRuntimeForNonOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "Mirror ICE private runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"provider":     "mirror_ice_private",
		"visibility":   "private",
	})
	otherUserID := createSecondWorkspaceMember(t)

	req := withURLParam(
		newRequestAs(otherUserID, http.MethodGet, "/api/runtimes/"+runtimeID+"/mirror/config", nil),
		"runtimeId", runtimeID,
	)
	testutil.Call(t, testHandler.GetMirrorICEConfig, req).Want(http.StatusNotFound)
}

func TestGetMirrorICEConfigRejectsMalformedRuntimeID(t *testing.T) {
	h := Handler{}
	req := withURLParam(
		newRequest(http.MethodGet, "/api/runtimes/not-a-uuid/mirror/config", nil),
		"runtimeId", "not-a-uuid",
	)
	testutil.Call(t, h.GetMirrorICEConfig, req).
		Want(http.StatusBadRequest).
		Map()
}
