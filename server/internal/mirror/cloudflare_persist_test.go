package mirror

import "testing"

func TestParseNetworkSettingsPreservesCloudflareKey(t *testing.T) {
	raw := []byte(`{"mirror_network":{
		"mode":"builtin",
		"servers":[],
		"cloudflare":{"key_id":"key-123","api_token_enc":"sealed-token"}
	}}`)
	settings, err := ParseNetworkSettings(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if settings.Cloudflare == nil {
		t.Fatal("cloudflare key dropped on parse")
	}
	if settings.Cloudflare.KeyID != "key-123" || settings.Cloudflare.APITokenEncrypted != "sealed-token" {
		t.Fatalf("cloudflare = %+v", settings.Cloudflare)
	}
}

func TestMarshalThenParseRoundTripsCloudflareKey(t *testing.T) {
	existing := []byte(`{"other":true}`)
	next := NetworkSettings{
		Mode:       NetworkModeBuiltin,
		Servers:    []StoredICEServer{},
		Cloudflare: &StoredCloudflareTURN{KeyID: "k", APITokenEncrypted: "enc"},
	}
	merged, err := MarshalNetworkSettings(existing, next)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := ParseNetworkSettings(merged)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if parsed.Cloudflare == nil || parsed.Cloudflare.KeyID != "k" || parsed.Cloudflare.APITokenEncrypted != "enc" {
		t.Fatalf("cloudflare not round-tripped: %+v", parsed.Cloudflare)
	}
}
