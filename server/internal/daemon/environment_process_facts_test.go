package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnvironmentFactsUseFreshTypedCallbacks(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/tasks/task/gc-check" || r.Method != http.MethodGet {
			t.Errorf("unexpected backend operation %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 400)
			return
		}
		if r.Header.Get("Authorization") != "Bearer parent-only" {
			t.Error("parent authentication missing")
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TaskGCStatus{TaskID: "task", WorkspaceID: "workspace", Status: "running"})
	}))
	defer backend.Close()
	parent := NewClient(backend.URL)
	parent.token = "parent-only"
	callback, err := newEnvironmentFactCallback(&Daemon{client: parent}, "environment-instance", "private-callback")
	if err != nil {
		t.Fatal(err)
	}
	defer callback.close()
	client := NewClient("http://physical-facts.invalid")
	client.token = "private-callback"
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client.client = &http.Client{Transport: &environmentFactTransport{address: callback.address, instanceID: "environment-instance", token: "private-callback", http: &http.Client{Transport: transport}}}
	for range 2 {
		status, err := client.GetTaskGCCheck(context.Background(), "task")
		if err != nil || status.Status != "running" {
			t.Fatalf("fresh scoped fact: %+v %v", status, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("facts cached rather than revalidated: calls=%d", calls.Load())
	}
	if err = client.getJSON(context.Background(), "/api/me", &struct{}{}); err == nil {
		t.Fatal("physical client reached account API")
	}
	if calls.Load() != 2 {
		t.Fatal("unrelated request reached backend")
	}
}

func TestEnvironmentFactsRejectPrivateAndStaleRequests(t *testing.T) {
	callback, err := newEnvironmentFactCallback(&Daemon{}, "instance", "callback-only")
	if err != nil {
		t.Fatal(err)
	}
	defer callback.close()
	valid := environmentFactEnvelope{InstanceID: "instance", Deadline: time.Now().Add(time.Second), Fact: environmentFact{Kind: "task", ID: "task"}}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, body, token string
		want              int
	}{
		{"wrong credential", string(encoded), "other", 401},
		{"private task payload", strings.TrimSuffix(string(encoded), "}") + `,"task":{"prompt":"private"}}`, "callback-only", 400},
		{"stale instance", strings.Replace(string(encoded), `"instance"`, `"old-instance"`, 1), "callback-only", 400},
		{"extra JSON", string(encoded) + `{}`, "callback-only", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, callback.address+"/facts", bytes.NewBufferString(test.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+test.token)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != test.want {
				t.Fatalf("status=%d want=%d", response.StatusCode, test.want)
			}
		})
	}
}
