package daemonws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentDesktopReadinessQueryAuthenticatedRoundTrip(t *testing.T) {
	hub := NewHub()
	conn := dialMirrorTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	result := make(chan error, 1)
	go func() {
		readiness, err := hub.QueryAgentDesktopReadiness(context.Background(), "ws", "rt", "daemon")
		if err == nil && (!readiness.Ready || readiness.Version != "1.3.1") {
			err = errors.New("unexpected readiness")
		}
		result <- err
	}()
	var frame protocol.Message
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var query protocol.AgentDesktopReadinessQuery
	if err := json.Unmarshal(frame.Payload, &query); err != nil {
		t.Fatal(err)
	}
	response := protocol.AgentDesktopReadinessResult{
		RuntimeEnvelope: query.RuntimeEnvelope,
		Readiness: protocol.AgentDesktopReadiness{
			Platform: "macos",
			Version:  "1.3.1",
			Permissions: protocol.AgentDesktopPermissions{
				Accessibility:   protocol.AgentDesktopPermissionGranted,
				ScreenRecording: protocol.AgentDesktopPermissionGranted,
				Automation:      protocol.AgentDesktopPermissionUnknown,
			},
			SurfaceCount: 1,
			Ready:        true,
		},
	}
	if err := conn.WriteJSON(protocol.Message{Type: protocol.EventAgentDesktopReadinessResult, Payload: mustMarshalRaw(response)}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no correlated agent-desktop readiness result")
	}
}
