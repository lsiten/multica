//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// runMirrorCancelAdapterFixture adds only test triggers. The queued outbound and
// cancelRemote closure are created by the actual child enqueue adapter, and both
// requests cross the production event/reply bridge before their results return.
func runMirrorCancelAdapterFixture(ctx context.Context, b runtimeproc.Bootstrap) error {
	if err := runtimeproc.PrepareRoot(b.Root); err != nil {
		return err
	}
	lock, err := lockMirrorProcessDomain(filepath.Join(b.Root, "authority.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	d := &Daemon{cfg: Config{ServerBaseURL: b.Identity.Scope.Backend, DaemonID: b.Identity.Scope.DaemonID, Profile: b.Identity.Scope.Profile}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workspaces: map[string]*workspaceState{}, runtimeIndex: map[string]Runtime{}, runtimeMirrors: map[string]*mirror.RuntimeMirror{}, inputArbiter: mirror.NewArbiter()}
	service := &mirrorProcessService{daemon: d, bootstrap: b, acquiring: map[string]bool{}, jobs: map[string]*mirrorProcessJob{}, executions: map[string]*mirrorChildExecution{}, stop: make(chan struct{}), stopped: make(chan struct{})}
	d.mirrorChild = service
	d.mirrorReportBridge = &mirrorChildReports{service: service}
	stored := map[string]*wsOutbound{}
	handler := func(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
		if request.Operation == "mirror.bind" || request.Operation == "mirror.unbind" {
			return service.mutate(ctx, request)
		}
		var input struct {
			Name       string `json:"name"`
			Generation uint64 `json:"generation"`
		}
		if json.Unmarshal(request.Payload, &input) != nil || input.Name == "" {
			return nil, mirrorIPCError(errors.New("invalid fixture request"))
		}
		switch request.Operation {
		case "fixture.enqueue":
			service.mu.Lock()
			current := service.bound && service.generation == input.Generation
			service.mu.Unlock()
			if !current {
				return nil, mirrorIPCError(errors.New("fixture binding not current"))
			}
			frame := marshalRaw(protocol.Message{Type: protocol.EventMirrorViewer, Payload: marshalRaw(protocol.MirrorViewerPayload{WorkspaceID: "ws", RuntimeID: "rt", DaemonID: b.Identity.Scope.DaemonID, ViewerID: input.Name, Active: true})})
			outbound, err := service.enqueue(input.Generation)(frame)
			if err != nil {
				return nil, mirrorIPCError(err)
			}
			stored[input.Name] = outbound
			return marshalRaw(map[string]any{"queued": true, "child_pid": os.Getpid()}), nil
		case "fixture.cancel":
			outbound := stored[input.Name]
			if outbound == nil {
				return nil, mirrorIPCError(errors.New("fixture outbound missing"))
			}
			return marshalRaw(map[string]bool{"cancelled": outbound.cancel()}), nil
		}
		return nil, mirrorIPCError(errors.New("unknown fixture trigger"))
	}
	transport, err := runtimeproc.NewService(runtimeproc.Config{Bootstrap: b, Capabilities: []string{"mirror.bind", "mirror.unbind", "fixture.enqueue", "fixture.cancel"}, ReadCapabilities: []string{"mirror.endpoint", "mirror.inventory"}, Handler: handler, ReadHandler: service.read, Ready: service.ready, Shutdown: service.shutdown})
	if err != nil {
		return err
	}
	return transport.Serve(ctx)
}

func TestMirrorProcessChildCancelAdapterRoundTrip(t *testing.T) {
	d, client, b := mirrorProcessFixtureEnv(t, map[string]string{"MIRROR_CANCEL_ADAPTER_FIXTURE": "1"})
	var queueMu sync.Mutex
	pending := map[string]*wsOutbound{}
	client.mu.Lock()
	client.enqueue = func(raw []byte) (*wsOutbound, error) {
		var frame protocol.Message
		var viewer protocol.MirrorViewerPayload
		if json.Unmarshal(raw, &frame) != nil || json.Unmarshal(frame.Payload, &viewer) != nil {
			return nil, errors.New("invalid queued frame")
		}
		outbound := &wsOutbound{data: raw}
		queueMu.Lock()
		pending[viewer.ViewerID] = outbound
		queueMu.Unlock()
		return outbound, nil
	}
	client.mu.Unlock()
	invoke := func(operation, name string, generation uint64, out any) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		defer cancel()
		status, err := client.process.Client.Health(ctx)
		if err != nil {
			t.Fatal(err)
		}
		request, err := client.process.Client.Request(operation, status.Fence, marshalRaw(map[string]any{"name": name, "generation": generation}))
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.process.Client.Call(ctx, request)
		if err != nil || response.Receipt == nil || response.Receipt.State != "completed" || response.Receipt.Error != nil {
			t.Fatalf("child fixture operation %s failed: %v", operation, err)
		}
		if err = json.Unmarshal(response.Receipt.Result, out); err != nil {
			t.Fatal(err)
		}
	}
	var queued struct {
		Queued   bool `json:"queued"`
		ChildPID int  `json:"child_pid"`
	}
	invoke("fixture.enqueue", "old", 1, &queued)
	if !queued.Queued || queued.ChildPID == os.Getpid() || queued.ChildPID < 1 {
		t.Fatal("enqueue did not originate in child process")
	}
	queueMu.Lock()
	old := pending["old"]
	queueMu.Unlock()
	if old == nil {
		t.Fatal("parent writer did not receive actual queued frame")
	}
	var cancelled struct {
		Cancelled bool `json:"cancelled"`
	}
	invoke("fixture.cancel", "old", 1, &cancelled)
	if !cancelled.Cancelled || old.beginWrite() {
		t.Fatal("actual child cancel hook did not cancel parent pending outbound")
	}
	resources, err := d.mirrorRoster()
	if err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	enqueue := client.enqueue
	client.mu.Unlock()
	if err = client.bind(t.Context(), mirrorControlBinding{Generation: 2, ServerGeneration: "replacement", Resources: resources}, enqueue); err != nil {
		t.Fatal(err)
	}
	invoke("fixture.enqueue", "replacement", 2, &queued)
	queueMu.Lock()
	replacement := pending["replacement"]
	queueMu.Unlock()
	if replacement == nil {
		t.Fatal("replacement outbound not enqueued")
	}
	// The trigger RPC reaches the child again, but the retired-generation
	// cancel event is rejected by the child bridge before event transport.
	invoke("fixture.cancel", "old", 1, &cancelled)
	if cancelled.Cancelled || !replacement.beginWrite() {
		t.Fatal("old child cancellation affected replacement-generation queue")
	}
	record, err := runtimeproc.ReadRecord(b.Root, b.Identity)
	if err != nil {
		t.Fatal(err)
	}
	controlAddress := record.Address
	eventAddress := client.endpoint
	pid := queued.ChildPID
	if err = client.close(); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child PID not reaped: %v", err)
	}
	for _, origin := range []string{controlAddress, eventAddress} {
		address := origin[len("http://"):]
		connection, err := net.DialTimeout("tcp", address, 150*time.Millisecond)
		if err == nil {
			connection.Close()
			t.Fatal("child listener survived teardown")
		}
	}
	proof := map[string]any{"controller_pid": os.Getpid(), "child_pid": pid, "old_parent_begin_write": false, "child_cancel_returned_true": true, "replacement_parent_begin_write": true, "old_generation_cancel_returned_false": true, "second_cancel_event_path": "retired generation rejected inside child bridge before cancel_outbound IPC; parent stale-event fencing is covered by the separate canonical matrix", "child_reaped": true, "control_listener_closed": true, "event_listener_closed": true, "scope": "actual child service.enqueue returns wsOutbound.cancelRemote; cancel invoked inside child; production event and reply handlers process real parent queue object"}
	t.Logf("child_pid=%d controller_pid=%d old.beginWrite=false replacement.beginWrite=true child/listeners reaped", pid, os.Getpid())
	if directory := os.Getenv("MIRROR_CANCEL_EVIDENCE_DIR"); directory != "" {
		raw, err := json.MarshalIndent(proof, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		name := "roundtrip-" + strconv.Itoa(os.Getpid()) + "-" + fmt.Sprint(time.Now().UnixNano()) + ".json"
		if err = os.WriteFile(filepath.Join(directory, name), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
