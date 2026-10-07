package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestApplicationLiveSurfaceFixture uses a test-created service executable and never discovers installed agent tools.
func TestApplicationLiveSurfaceFixture(t *testing.T) {
	inputPath := os.Getenv("MULTICA_APPLICATION_SURFACE_FIXTURE")
	if inputPath == "" {
		return
	}
	var input struct {
		ServerURL   string
		Token       string
		WorkspaceID string
		RuntimeID   string
		DaemonID    string
		ReadyPath   string
		StopPath    string
	}
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	d, command := applicationManagerFixture(t, input.ServerURL)
	d.cfg.DaemonID = input.DaemonID
	d.client.SetToken(input.Token)
	d.runtimeIndex = map[string]Runtime{input.RuntimeID: {ID: input.RuntimeID, Status: "online"}}
	d.workspaces = map[string]*workspaceState{input.WorkspaceID: {workspaceID: input.WorkspaceID, runtimeIDs: []string{input.RuntimeID}}}
	d.applicationServerCapabilities.Store(input.RuntimeID, true)
	var source localDirectoryRef
	if err := json.Unmarshal(command.ResourceRef, &source); err != nil {
		t.Fatal(err)
	}
	source.DaemonID = input.DaemonID
	sourceJSON, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	command.Config.AutoPublish = true
	ready, err := json.Marshal(map[string]any{"resource_ref": json.RawMessage(sourceJSON), "config": command.Config})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(input.ReadyPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input.ReadyPath, ready, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t.Cleanup(d.stopOwnedApplications)
	tunnelDone := make(chan struct{})
	go func() { defer close(tunnelDone); d.serveApplicationTunnel(ctx, input.RuntimeID) }()
	defer func() { cancel(); <-tunnelDone }()
	var workers sync.WaitGroup
	defer workers.Wait()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	semaphore := make(chan struct{}, 4)
	for {
		if _, err := os.Stat(input.StopPath); err == nil {
			return
		}
		d.pollRuntimeApplications(ctx, semaphore, &workers, input.RuntimeID)
		select {
		case <-ctx.Done():
			t.Fatal("application surface fixture timed out")
		case <-ticker.C:
		}
	}
}
