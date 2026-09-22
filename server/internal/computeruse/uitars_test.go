package computeruse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUITARSPredictWire(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing configured credential")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "fixture-model" || body["stream"] != false {
			t.Error("invalid inference contract")
		}
		messages := body["messages"].([]any)
		content := messages[0].(map[string]any)["content"].([]any)
		if !strings.Contains(content[0].(map[string]any)["text"].(string), "click Allow") {
			t.Error("goal lost")
		}
		if content[1].(map[string]any)["type"] != "image_url" {
			t.Error("screenshot lost")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Action: click(start_box='(10,20)')"}}]}`))
	}))
	defer server.Close()
	client, err := NewUITARS(UITARSConfig{Endpoint: server.URL, Model: "fixture-model", APIKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Predict(t.Context(), "click Allow", []byte{137, 80, 78, 71, 13, 10, 26, 10})
	if err != nil || got != "Action: click(start_box='(10,20)')" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestUITARSRejectsUnsafeEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com?key=secret", "file:///tmp/model"} {
		if _, err := NewUITARS(UITARSConfig{Endpoint: endpoint, Model: "model"}); err == nil {
			t.Errorf("accepted %s", endpoint)
		}
	}
}

func TestUITARSCancellation(t *testing.T) {
	client, err := NewUITARS(UITARSConfig{Endpoint: "http://127.0.0.1:1", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.Predict(ctx, "click", []byte{137, 80, 78, 71, 13, 10, 26, 10})
	if err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}
