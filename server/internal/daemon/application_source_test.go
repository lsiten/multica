package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/repocache"
)

func TestApplicationSourceTracksDirtyLocalCodeAndIsolatesRequestedGitReferences(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	var local localDirectoryRef
	if err := json.Unmarshal(command.ResourceRef, &local); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		process := exec.Command("git", append([]string{"-C", local.LocalPath}, args...)...)
		process.Env = append(os.Environ(), "GIT_AUTHOR_NAME=application-fixture", "GIT_AUTHOR_EMAIL=application@example.test", "GIT_COMMITTER_NAME=application-fixture", "GIT_COMMITTER_EMAIL=application@example.test")
		output, err := process.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git command failed: %v", err)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-b", "main")
	file := filepath.Join(local.LocalPath, "version.txt")
	if err := os.WriteFile(file, []byte("version-one"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "version.txt")
	git("commit", "-m", "version-one")
	first := git("rev-parse", "HEAD")
	git("tag", "release-one")
	_, _, version, dirty, err := d.applicationSource(context.Background(), command)
	if err != nil || version != first || dirty {
		t.Fatalf("committed local source metadata: version=%s dirty=%v error=%v", version, dirty, err)
	}
	if err := os.WriteFile(file, []byte("uncommitted-edit"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, version, dirty, err = d.applicationSource(context.Background(), command)
	if err != nil || version != first || !dirty {
		t.Fatalf("dirty local source was hidden: version=%s dirty=%v error=%v", version, dirty, err)
	}
	git("add", "version.txt")
	git("commit", "-m", "version-two")
	second := git("rev-parse", "HEAD")
	d.repoCache = repocache.New(filepath.Join(d.cfg.WorkspacesRoot, "source-cache"), d.logger)
	command.Config.Ref = "release-one"
	root, _, version, dirty, err := d.applicationSource(context.Background(), command)
	if err != nil || version != first || dirty {
		t.Fatalf("requested tag selected wrong code: version=%s dirty=%v error=%v", version, dirty, err)
	}
	if root == local.LocalPath {
		t.Fatal("requested ref changed the original in-place repository")
	}
	content, err := os.ReadFile(filepath.Join(root, "version.txt"))
	if err != nil || string(content) != "version-one" || git("rev-parse", "HEAD") != second {
		t.Fatalf("tag checkout or original repository was changed: %v", err)
	}
	next := command
	next.Revision++
	next.Config.Ref = "main"
	nextRoot, _, version, _, err := d.applicationSource(context.Background(), next)
	if err != nil || version != second || root == nextRoot {
		t.Fatalf("new config revision did not isolate its checkout: version=%s error=%v", version, err)
	}
	another := command
	another.InstanceID = uuid.NewString()
	anotherRoot, _, version, _, err := d.applicationSource(context.Background(), another)
	if err != nil || version != first || anotherRoot == root {
		t.Fatalf("independent instance shared mutable checkout files: version=%s error=%v", version, err)
	}
	missing := next
	missing.Revision++
	missing.Config.Ref = "nonexistent-application-reference"
	if _, _, _, _, err := d.applicationSource(context.Background(), missing); err == nil {
		t.Fatal("missing Git reference silently used another branch")
	}
	content, err = os.ReadFile(filepath.Join(root, "version.txt"))
	if err != nil || string(content) != "version-one" {
		t.Fatal("later checkout changed the earlier application's code")
	}
}

func TestApplicationMovedLocalResourceKeepsOwnedShutdownAndRequiresUpdatedSource(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Cleanup(d.stopOwnedApplications)
	if _, err := d.executeApplication(ctx, command); err != nil {
		t.Fatal(err)
	}
	var resource localDirectoryRef
	if err := json.Unmarshal(command.ResourceRef, &resource); err != nil {
		t.Fatal(err)
	}
	moved := resource.LocalPath + "-moved"
	if err := os.Rename(resource.LocalPath, moved); err != nil {
		t.Fatal(err)
	}
	stop := command
	stop.Action = "stop"
	stop.Generation = 2
	if observation, err := d.executeApplication(ctx, stop); err != nil || observation.ProcessState != "stopped" {
		t.Fatalf("moving code lost private process ownership: state=%s error=%v", observation.ProcessState, err)
	}
	restart := command
	restart.Generation = 3
	if _, err := d.executeApplication(ctx, restart); err == nil {
		t.Fatal("missing original source silently adopted another directory")
	}
	resource.LocalPath = moved
	var err error
	restart.ResourceRef, err = json.Marshal(resource)
	if err != nil {
		t.Fatal(err)
	}
	restart.Generation = 4
	restart.Revision = 2
	restart.Config.Environment["APPLICATION_START_FILE"] = filepath.Join(moved, "starts")
	if observation, err := d.executeApplication(ctx, restart); err != nil || observation.ProcessState != "running" {
		t.Fatalf("explicitly updated local resource could not run: state=%s error=%v", observation.ProcessState, err)
	}
	stop = restart
	stop.Action = "stop"
	stop.Generation = 5
	if _, err := d.executeApplication(ctx, stop); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatal("shutdown deleted the moved original project")
	}
}
