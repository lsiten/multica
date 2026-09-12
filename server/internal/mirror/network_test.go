package mirror

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func TestBuiltinTURNPlanMintsShortLivedCredentials(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	config := BuiltinTURNConfig{
		Enabled: true,
		Host:    "turn.example.com",
		Port:    3478,
		Secret:  "turn-shared-secret",
		TTL:     time.Hour,
	}

	plan := config.Plan(now, "user-123")
	if !plan.TURNConfigured || len(plan.ICEServers) != 1 {
		t.Fatalf("plan = %+v, want one configured TURN server", plan)
	}
	server := plan.ICEServers[0]
	wantURLs := []string{
		"stun:turn.example.com:3478",
		"turn:turn.example.com:3478?transport=udp",
		"turn:turn.example.com:3478?transport=tcp",
	}
	if len(server.URLs) != len(wantURLs) {
		t.Fatalf("urls = %v, want %v", server.URLs, wantURLs)
	}
	for index := range wantURLs {
		if server.URLs[index] != wantURLs[index] {
			t.Fatalf("urls[%d] = %q, want %q", index, server.URLs[index], wantURLs[index])
		}
	}
	expires := now.Add(time.Hour).Unix()
	wantUsername := strconv.FormatInt(expires, 10) + ":user-123"
	if server.Username != wantUsername {
		t.Fatalf("username = %q, want %q", server.Username, wantUsername)
	}
	mac := hmac.New(sha1.New, []byte(config.Secret))
	_, _ = mac.Write([]byte(wantUsername))
	wantCredential := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if server.Credential != wantCredential {
		t.Fatalf("credential = %q, want %q", server.Credential, wantCredential)
	}
}

func TestResolveNetworkPlanPrecedence(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	builtin := BuiltinTURNConfig{Enabled: true, Host: "turn.example.com", Port: 3478, Secret: "secret", TTL: time.Hour}
	custom := NetworkSettings{
		Mode: NetworkModeCustom,
		Servers: []StoredICEServer{{
			URLs:     []string{"turn:custom.example.com:3478?transport=udp"},
			Username: "workspace",
		}},
	}

	tests := []struct {
		name       string
		settings   NetworkSettings
		deployment ICEPlan
		wantSource string
		wantTURN   bool
	}{
		{name: "deployment lock", settings: custom, deployment: ICEPlan{ICEServers: []ICEServer{{URLs: []string{"turn:locked.example.com"}}}, TURNConfigured: true}, wantSource: NetworkSourceEnv, wantTURN: true},
		{name: "custom", settings: custom, wantSource: NetworkSourceCustom, wantTURN: true},
		{name: "builtin default", settings: DefaultNetworkSettings(), wantSource: NetworkSourceBuiltin, wantTURN: true},
		{name: "disabled", settings: NetworkSettings{Mode: NetworkModeDisabled}, wantSource: NetworkSourceDisabled},
		{name: "builtin unavailable", settings: DefaultNetworkSettings(), wantSource: NetworkSourceBuiltinUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := builtin
			if tt.name == "builtin unavailable" {
				config.Secret = ""
			}
			plan, source := ResolveNetworkPlan(tt.settings, tt.deployment, config, []ICEServer{{URLs: []string{"turn:custom.example.com:3478?transport=udp"}, Username: "workspace", Credential: "workspace-secret"}}, now, "user-1")
			if source != tt.wantSource {
				t.Fatalf("source = %q, want %q", source, tt.wantSource)
			}
			if plan.TURNConfigured != tt.wantTURN {
				t.Fatalf("TURNConfigured = %v, want %v", plan.TURNConfigured, tt.wantTURN)
			}
		})
	}
}

func TestParseAndMarshalNetworkSettingsPreservesOtherSettings(t *testing.T) {
	existing := []byte(`{"github_enabled":true,"mirror_network":{"mode":"custom","servers":[{"urls":["turn:custom.example.com:3478"],"username":"u","credential":"p"}]}}`)
	settings, err := ParseNetworkSettings(existing)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if settings.Mode != NetworkModeCustom || len(settings.Servers) != 1 {
		t.Fatalf("settings = %+v", settings)
	}
	next, err := MarshalNetworkSettings(existing, NetworkSettings{Mode: NetworkModeDisabled})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(next, &document); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if document["github_enabled"] != true {
		t.Fatalf("other settings lost: %s", next)
	}
	if mirror, ok := document["mirror_network"].(map[string]any); !ok || mirror["mode"] != NetworkModeDisabled {
		t.Fatalf("mirror settings = %#v", document["mirror_network"])
	}
}
