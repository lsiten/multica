package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceMembershipLossStopsOnlyItsOwnedApplicationServices(t *testing.T) {
	keptWorkspaceID := uuid.NewString()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/workspaces") {
			if err := json.NewEncoder(w).Encode([]WorkspaceInfo{{ID: keptWorkspaceID, Name: "retained workspace"}}); err != nil {
				t.Error(err)
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/observe") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Errorf("unexpected scope synchronization request: %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()
	d, removed := applicationManagerFixture(t, server.URL)
	d.runtimeSet = newRuntimeSetWatcher()
	_, kept := applicationManagerFixture(t, server.URL)
	kept.WorkspaceID = keptWorkspaceID
	kept.RuntimeID = uuid.NewString()
	kept.ApplicationID = uuid.NewString()
	kept.InstanceID = uuid.NewString()
	d.runtimeIndex[kept.RuntimeID] = Runtime{ID: kept.RuntimeID, Status: "online"}
	d.workspaces[kept.WorkspaceID] = &workspaceState{workspaceID: kept.WorkspaceID, runtimeIDs: []string{kept.RuntimeID}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Cleanup(d.stopOwnedApplications)
	for _, command := range []protocol.ApplicationControlCommand{removed, kept} {
		if _, err := d.executeApplication(ctx, command); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.syncWorkspacesFromAPI(ctx, false); err != nil {
		t.Fatal(err)
	}
	_, stopped, err := d.applicationRecord(removed)
	if err != nil || stopped.Observation.ProcessState != "stopped" {
		t.Fatalf("removed workspace left an unmanaged service running: state=%s error=%v", stopped.Observation.ProcessState, err)
	}
	response, err := (&http.Client{Timeout: time.Second}).Get("http://127.0.0.1:" + strconv.Itoa(kept.Config.Port))
	if err != nil {
		t.Fatalf("workspace revocation stopped another workspace's service: %v", err)
	}
	response.Body.Close()
}

func TestApplicationScopePreparationFixture(t *testing.T) {
	path := os.Getenv("APPLICATION_SCOPE_PREPARATION_MARKER")
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte("preparing"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}

func TestWorkspaceRevocationDuringPreparationCannotLeaveAServiceBehind(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	d, command := applicationManagerFixture(t, server.URL)
	marker := filepath.Join(d.cfg.WorkspacesRoot, "preparation-entered")
	command.Config.Environment["APPLICATION_SCOPE_PREPARATION_MARKER"] = marker
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command.Config.Prepare = []protocol.ApplicationCommand{{Args: []string{executable, "-test.run=^TestApplicationScopePreparationFixture$"}, TimeoutSeconds: 30}}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	t.Cleanup(d.stopOwnedApplications)
	completed := make(chan error, 1)
	go func() { _, err := d.executeApplication(ctx, command); completed <- err }()
	waitApplicationManager(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	})
	d.mu.Lock()
	delete(d.workspaces, command.WorkspaceID)
	delete(d.runtimeIndex, command.RuntimeID)
	d.mu.Unlock()
	d.stopUnavailableWorkspaceApplications(ctx, map[string]string{})
	select {
	case <-completed:
	case <-ctx.Done():
		t.Fatal("revoked preparation did not settle")
	}
	_, record, err := d.applicationRecord(command)
	if err != nil || record.Observation.ProcessState != "stopped" {
		t.Fatalf("revoked preparation left an unmanaged process: state=%s error=%v", record.Observation.ProcessState, err)
	}
	if _, err := os.Stat(command.Config.Environment["APPLICATION_START_FILE"]); !os.IsNotExist(err) {
		t.Fatalf("application service launched after its workspace was revoked: %v", err)
	}
}

func TestRuntimeDisappearanceStopsItsServiceAndPreservesSiblingRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	d, removed := applicationManagerFixture(t, server.URL)
	_, kept := applicationManagerFixture(t, server.URL)
	kept.RuntimeID = uuid.NewString()
	kept.ApplicationID = uuid.NewString()
	kept.InstanceID = uuid.NewString()
	d.runtimeIndex[kept.RuntimeID] = Runtime{ID: kept.RuntimeID, Status: "online"}
	d.workspaces[kept.WorkspaceID].runtimeIDs = []string{removed.RuntimeID, kept.RuntimeID}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d.rootCtx = ctx
	t.Cleanup(d.stopOwnedApplications)
	for _, command := range []protocol.ApplicationControlCommand{removed, kept} {
		if _, err := d.executeApplication(ctx, command); err != nil {
			t.Fatal(err)
		}
	}
	workspaceID, changed := d.removeStaleRuntime(removed.RuntimeID)
	if !changed || workspaceID != removed.WorkspaceID {
		t.Fatal("runtime disappearance did not remove its local registration")
	}
	_, record, err := d.applicationRecord(removed)
	if err != nil || record.Observation.ProcessState != "stopped" {
		t.Fatalf("disappeared runtime left an unmanaged service: state=%s error=%v", record.Observation.ProcessState, err)
	}
	response, err := (&http.Client{Timeout: time.Second}).Get("http://127.0.0.1:" + strconv.Itoa(kept.Config.Port))
	if err != nil {
		t.Fatalf("runtime disappearance stopped the sibling service: %v", err)
	}
	response.Body.Close()
}
