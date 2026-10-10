package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationServiceClientCanonicalizesActualProcessIdentityAndTextDaemon(t *testing.T) {
	const daemonID = "qa-host.local:custom-profile"
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "http://127.0.0.1", Account: uuid.NewString(), Profile: t.TempDir(), DaemonID: daemonID, Service: "application"}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	runtimeID := uuid.NewString()
	grant := protocol.ApplicationServiceGrantResponse{ApplicationServiceGrantState: protocol.ApplicationServiceGrantState{Capability: protocol.ApplicationServiceGrantCapability, WorkspaceID: uuid.NewString(), RuntimeID: runtimeID, DaemonID: daemonID, ServiceInstanceID: uuid.MustParse(identity.InstanceID).String(), Generation: 1}, Token: "mps_" + strings.Repeat("a", 64), Operations: []string{"sync"}, ExpiresAt: time.Now().Add(time.Minute)}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if strings.HasSuffix(r.URL.Path, "application-service-grants") {
			var input protocol.ApplicationServiceGrantRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.ServiceInstanceID != grant.ServiceInstanceID {
				t.Errorf("process identity was not canonicalized: %s", input.ServiceInstanceID)
			}
			json.NewEncoder(w).Encode(grant)
			return
		}
		if r.URL.Path != "/api/daemon/runtimes/"+runtimeID+"/applications/sync" || r.Header.Get("Authorization") != "Bearer "+grant.Token {
			t.Error("child transport escaped scope")
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["daemon_id"] != daemonID {
			t.Error("text daemon ID changed")
		}
		w.Write([]byte(`[]`))
	}))
	defer server.Close()
	issued, err := NewClient(server.URL).IssueApplicationServiceGrant(context.Background(), runtimeID, protocol.ApplicationServiceGrantRequest{ServiceInstanceID: identity.InstanceID, Operations: []string{"sync"}})
	if err != nil {
		t.Fatal(err)
	}
	child, err := NewApplicationServiceClient(server.URL, daemonID, identity.InstanceID, issued)
	if err != nil {
		t.Fatal(err)
	}
	defer child.CloseIdleConnections()
	if _, err = child.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = child.Claim(context.Background()); err == nil || requests != 2 {
		t.Fatal("ungranted claim reached network")
	}
	if _, err = NewApplicationServiceClient(server.URL, daemonID, uuid.NewString(), grant); err == nil {
		t.Fatal("foreign process instance accepted")
	}
	if _, err = NewApplicationServiceClient(server.URL, "other-daemon", identity.InstanceID, grant); err == nil {
		t.Fatal("foreign daemon grant accepted")
	}
	grant.Token = "mul_" + strings.Repeat("a", 40)
	if _, err = NewApplicationServiceClient(server.URL, daemonID, identity.InstanceID, grant); err == nil {
		t.Fatal("account credential accepted by child")
	}
}

func TestApplicationServiceClientOldServerAndMalformedResponse(t *testing.T) {
	for _, code := range []int{404, 405, 200} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code); w.Write([]byte(`{}`)) }))
		_, err := NewClient(server.URL).ApplicationServiceGrantState(context.Background(), uuid.NewString())
		server.Close()
		if !errors.Is(err, ErrApplicationServiceGrantUnsupported) {
			t.Fatalf("old server %d: %v", code, err)
		}
	}
	for _, value := range []string{"", "\nforged", strings.Repeat("a", 513)} {
		if protocol.ValidApplicationDaemonID(value) {
			t.Fatalf("invalid daemon accepted: length=%d", len(value))
		}
	}
}
