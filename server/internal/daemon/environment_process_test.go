package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/repocache"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestEnvironmentChildOwnsPhysicalPrepareAndRejectsPrivatePayload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unexpected backend request", 400) }))
	defer backend.Close()
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: backend.URL, Account: "owned-account", DaemonID: "owned-daemon", Service: "environment", Profile: "owned-fixture"}, "fixture/commit")
	if err != nil {
		t.Fatal(err)
	}
	serviceParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(filepath.Join(serviceParent, "service"), identity)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	parent := &Daemon{client: NewClient(backend.URL)}
	facts, err := newEnvironmentFactCallback(parent, identity.InstanceID, bootstrap.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer facts.close()
	environment := map[string]string{}
	for _, entry := range repocache.GitEnvironment(os.Environ()) {
		key, value, _ := strings.Cut(entry, "=")
		environment[key] = value
	}
	t.Logf("REGISTER environment child root=%s instance=%s callback=%s executable=%s", bootstrap.Root, identity.InstanceID, facts.address, executable)
	process, err := runtimeproc.Start(ctx, runtimeproc.LaunchConfig{Executable: executable, SHA256: hex.EncodeToString(hash.Sum(nil)), Environment: environment, Bootstrap: bootstrap, StartupTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := process.Close(); err != nil {
			t.Error(err)
		}
		t.Log("CLEANUP environment child reaped")
	})
	call := func(operation string, input any) (runtimeproc.Response, error) {
		status, err := process.Client.Health(ctx)
		if err != nil {
			return runtimeproc.Response{}, err
		}
		request, err := process.Client.Request(operation, status.Fence, marshalRaw(input))
		if err != nil {
			return runtimeproc.Response{}, err
		}
		return process.Client.Call(ctx, request)
	}
	root := t.TempDir()
	response, err := call("environment.bind", environmentProcessBinding{WorkspacesRoot: root, FactsAddress: facts.address, Runtimes: []environmentRuntimeBinding{{WorkspaceID: "workspace", RuntimeIDs: []string{"runtime"}}}})
	if err != nil || response.Receipt == nil || response.Receipt.Error != nil {
		t.Fatalf("bind failed: %+v %v", response, err)
	}
	inventory, err := process.Client.Read(ctx, "environment.inventory", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var observed struct {
		PID  int    `json:"pid"`
		Root string `json:"root"`
	}
	if err = json.Unmarshal(inventory, &observed); err != nil || observed.PID == 0 || observed.PID == os.Getpid() {
		t.Fatalf("not an actual physical child: %s %v", inventory, err)
	}
	t.Logf("OBSERVE environment PID=%d parent=%d root=%s", observed.PID, os.Getpid(), observed.Root)
	beforeRejected, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	response, err = call("physical.prepare", map[string]any{"workspace_id": "workspace", "runtime_id": "runtime", "task_id": "task", "task": map[string]string{"prompt": "private"}})
	if err != nil || response.Receipt == nil || response.Receipt.Error == nil {
		t.Fatalf("private task accepted: %+v %v", response, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(beforeRejected) {
		t.Fatalf("rejected private request created physical entries: %v", entries)
	}
	response, err = call("physical.prepare", execenv.PhysicalPrepareParams{WorkspaceID: "workspace", RuntimeID: "runtime", TaskID: "task"})
	if err != nil || response.Receipt == nil || response.Receipt.Error != nil {
		t.Fatalf("prepare failed: %+v %v", response, err)
	}
	var physical environmentPhysicalResult
	if err = json.Unmarshal(response.Receipt.Result, &physical); err != nil {
		t.Fatal(err)
	}
	claim, err := execenv.ClaimPhysicalRoot(root, physical.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	response, err = call("physical.root_confirm", physical.Reservation)
	if err != nil || response.Receipt == nil || response.Receipt.Error != nil {
		t.Fatalf("claim confirmation failed: %+v %v", response, err)
	}
	if _, err = os.Stat(physical.WorkDir); err != nil {
		t.Fatal(err)
	}
	t.Logf("OBSERVE child-prepared root=%s task=%s parent-held claim; raw private payload rejected before any write", physical.Reservation.RootDir, physical.Reservation.TaskID)
}
