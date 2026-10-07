package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationRetentionPreservesLiveHostsSharedCodeAndOriginalProject(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if _, err := d.executeApplication(ctx, command); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stopOwnedApplications)
	path, record, err := d.applicationRecord(command)
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(filepath.Dir(path), "revisions", "1")
	if err := os.MkdirAll(checkout, 0700); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(checkout, "owned-artifact")
	if err := os.WriteFile(artifact, []byte("private checkout"), 0600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	d.rememberApplicationRegistry(command.RuntimeID, []protocol.ApplicationRuntimeInstance{{DesiredState: "stopped", ConfirmedStopped: true, Command: command}})
	d.gcApplicationStorage(ctx, time.Now())
	if _, err := os.Stat(artifact); err != nil {
		t.Fatal("retention deleted an active host's private code")
	}
	stop := command
	stop.Action = "stop"
	stop.Generation = 2
	if _, err := d.executeApplication(ctx, stop); err != nil {
		t.Fatal(err)
	}
	_, record, err = d.applicationRecord(stop)
	if err != nil {
		t.Fatal(err)
	}
	record.Command.ResourceType = "github_repo"
	record.Command.ResourceRef = json.RawMessage("{\"url\":\"https://github.com/example/fixture.git\"}")
	if err := applicationhost.WriteRecord(path, record); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	d.rememberApplicationRegistry(command.RuntimeID, []protocol.ApplicationRuntimeInstance{{DesiredState: "running", Command: command}})
	d.gcApplicationStorage(ctx, time.Now())
	if _, err := os.Stat(artifact); err != nil {
		t.Fatal("retention erased a still-desired instance's generation evidence")
	}
	d.rememberApplicationRegistry(command.RuntimeID, []protocol.ApplicationRuntimeInstance{{DesiredState: "stopped", ConfirmedStopped: true, Command: stop}})
	lease, err := execenv.UseSharedDirectory(ctx, checkout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lease.Finish(context.Background(), nil) })
	d.gcApplicationStorage(ctx, time.Now())
	if _, err := os.Stat(artifact); err != nil {
		t.Fatal("retention deleted code with another live borrower")
	}
	if err := lease.Finish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	d.gcApplicationStorage(ctx, time.Now())
	if _, err := os.Stat(checkout); !os.IsNotExist(err) {
		t.Fatalf("expired unused checkout was retained: %v", err)
	}
	if _, err := os.Stat(record.SourceRoot); err != nil {
		t.Fatal("retention deleted the original in-place project directory")
	}
	if retained, err := applicationhost.ReadRecord(path); err != nil || retained.Command.Generation != stop.Generation {
		t.Fatalf("retention removed the stopped ownership receipt: error=%v", err)
	}
	interrupted := filepath.Join(filepath.Dir(checkout), ".application-gc-interrupted")
	if err := os.MkdirAll(interrupted, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(interrupted, "undeleted-artifact"), []byte("interrupted cleanup"), 0600); err != nil {
		t.Fatal(err)
	}
	d.gcApplicationStorage(ctx, time.Now())
	if _, err := os.Stat(interrupted); !os.IsNotExist(err) {
		t.Fatalf("later retention did not recover interrupted cleanup: %v", err)
	}
}
