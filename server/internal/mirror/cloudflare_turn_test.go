package mirror

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseCloudflareICEResponseArrayShape(t *testing.T) {
	body := []byte(`{"iceServers":[
		{"urls":["stun:stun.cloudflare.com:3478"]},
		{"urls":["turn:turn.cloudflare.com:3478?transport=udp","turn:turn.cloudflare.com:3478?transport=tcp","turns:turn.cloudflare.com:5349?transport=tcp","turn:turn.cloudflare.com:80?transport=tcp","turns:turn.cloudflare.com:443?transport=tcp"],"username":"u1","credential":"c1"}
	]}`)
	plan, err := parseCloudflareICEResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !plan.TURNConfigured {
		t.Fatalf("TURNConfigured = false, want true")
	}
	if len(plan.ICEServers) != 2 {
		t.Fatalf("servers = %d, want 2", len(plan.ICEServers))
	}
	turnServer := plan.ICEServers[1]
	if turnServer.Username != "u1" || turnServer.Credential != "c1" {
		t.Fatalf("credentials not parsed: %+v", turnServer)
	}
	for _, u := range turnServer.URLs {
		if isPort53ICEURL(u) {
			t.Fatalf("port 53 URL should not appear in parsed set: %s", u)
		}
	}
}

func TestParseCloudflareICEResponseObjectShape(t *testing.T) {
	body := []byte(`{"iceServers":{"urls":["turn:turn.example.com:3478?transport=udp"],"username":"u","credential":"c"}}`)
	plan, err := parseCloudflareICEResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(plan.ICEServers) != 1 || !plan.TURNConfigured {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestParseCloudflareICEResponseRejectsEmpty(t *testing.T) {
	if _, err := parseCloudflareICEResponse([]byte(`{"iceServers":[]}`)); err == nil {
		t.Fatal("expected error for empty iceServers")
	}
}

func TestCloudflareTURNProviderCachesAndRefreshes(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("Authorization = %q", got)
		}
		var req map[string]int64
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"iceServers": []map[string]any{
				{"urls": []string{"stun:stun.example.com:3478"}},
				{"urls": []string{"turn:turn.example.com:3478?transport=udp"}, "username": "u", "credential": "c"},
			},
		})
	}))
	defer server.Close()

	base := time.Unix(1_900_000_000, 0)
	now := base
	provider := &CloudflareTURNProvider{
		config: CloudflareTURNConfig{
			KeyID:    "key-1",
			APIToken: "secret-token",
			TTL:      time.Hour,
			Endpoint: server.URL,
		},
		client: server.Client(),
		now:    func() time.Time { return now },
	}
	plan := provider.Plan(base, "viewer")
	if len(plan.ICEServers) != 2 || !plan.TURNConfigured {
		t.Fatalf("plan = %+v", plan)
	}
	// Within the TTL window (minus refresh margin) no new request must fire.
	_ = provider.Plan(base.Add(30*time.Minute), "viewer")
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want cached single call", got)
	}
	// Past the refresh margin: credentials are minted again.
	now = base.Add(time.Hour)
	_ = provider.Plan(now, "viewer")
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls = %d, want refresh to 2", got)
	}
}

func TestCloudflareTURNProviderServesUnexpiredOnRefreshFailure(t *testing.T) {
	fail := int32(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&fail) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"iceServers": []map[string]any{
				{"urls": []string{"turn:turn.example.com:3478?transport=udp"}, "username": "u", "credential": "c"},
			},
		})
	}))
	defer server.Close()

	provider := &CloudflareTURNProvider{
		config: CloudflareTURNConfig{KeyID: "k", APIToken: "t", TTL: time.Hour, Endpoint: server.URL},
		client: &http.Client{Timeout: time.Second},
		now:    time.Now,
	}
	if err := provider.refresh(context.Background()); err == nil {
		t.Fatal("first refresh should fail")
	}
	atomic.StoreInt32(&fail, 0)
	plan := provider.Plan(time.Now(), "viewer")
	if !plan.TURNConfigured {
		t.Fatalf("plan after recovery = %+v", plan)
	}
}

func TestBuiltinChainPrecedence(t *testing.T) {
	empty := stubBuiltin{configured: true}
	full := stubBuiltin{configured: true, plan: ICEPlan{ICEServers: []ICEServer{{URLs: []string{"turn:chain.example.com"}}}, TURNConfigured: true}}
	chain := NewBuiltinChain(empty, full)
	if !chain.Configured() {
		t.Fatal("chain should be configured")
	}
	plan := chain.Plan(time.Now(), "id")
	if !plan.TURNConfigured {
		t.Fatalf("chain did not fall through to the working provider: %+v", plan)
	}
}

type stubBuiltin struct {
	configured bool
	plan       ICEPlan
}

func (s stubBuiltin) Configured() bool { return s.configured }
func (s stubBuiltin) Plan(time.Time, string) ICEPlan {
	return s.plan
}
