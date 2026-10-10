package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newWorkerProcessClientForRegistryTest() *workerProcessClient {
	return &workerProcessClient{
		transport:  &fakeWorkerTransport{},
		wait:       func(context.Context) error { return nil },
		gate:       make(chan struct{}, 1),
		instanceID: "instance-1",
	}
}

func TestWorkerProcessRegistryRegistersAndReclaimsOnClose(t *testing.T) {
	c := newWorkerProcessClientForRegistryTest()
	d := &Daemon{workerProcessRegistry: newWorkerProcessRegistry()}
	d.registerWorkerProcess(c, "exec-1", "task-1", "claude")
	if got := len(d.workerProcessRegistry.snapshot()); got != 1 {
		t.Fatalf("expected 1 registered worker, got %d", got)
	}
	// A close must reclaim the live entry; a second close is a no-op.
	c.close()
	c.close()
	if got := len(d.workerProcessRegistry.snapshot()); got != 0 {
		t.Fatalf("expected worker reclaimed after close, got %d", got)
	}
}

func TestWorkerProcessRegistryNilSafe(t *testing.T) {
	d := &Daemon{} // no workerProcessRegistry
	// register and the handler paths must not panic when the registry is nil.
	c := newWorkerProcessClientForRegistryTest()
	d.registerWorkerProcess(c, "exec-1", "task-1", "claude")
	rec := httptest.NewRecorder()
	d.workerProcessesHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtimes/processes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestWorkerProcessesHandlerListsActiveWorkerState(t *testing.T) {
	c := &workerProcessClient{
		transport:  &fakeWorkerTransport{healthState: "running"},
		wait:       func(context.Context) error { return nil },
		gate:       make(chan struct{}, 1),
		instanceID: "instance-1",
	}
	d := &Daemon{workerProcessRegistry: newWorkerProcessRegistry()}
	d.registerWorkerProcess(c, "exec-1", "task-1", "claude")

	rec := httptest.NewRecorder()
	d.workerProcessesHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtimes/processes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Processes []workerProcessStateResponse `json:"processes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Processes) != 1 {
		t.Fatalf("expected 1 process, got %d", len(body.Processes))
	}
	p := body.Processes[0]
	if p.State != "running" || !p.Ready || p.ExecID != "exec-1" || p.InstanceID != "instance-1" || p.TaskID != "task-1" || p.Provider != "claude" {
		t.Fatalf("unexpected worker state: %+v", p)
	}
}

func TestWorkerProcessesHandlerRejectsNonGet(t *testing.T) {
	d := &Daemon{workerProcessRegistry: newWorkerProcessRegistry()}
	rec := httptest.NewRecorder()
	d.workerProcessesHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/runtimes/processes", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestWorkerProcessStopHandlerReclaimsWorker(t *testing.T) {
	c := newWorkerProcessClientForRegistryTest()
	d := &Daemon{workerProcessRegistry: newWorkerProcessRegistry()}
	d.registerWorkerProcess(c, "exec-1", "task-1", "claude")

	body, _ := json.Marshal(workerProcessStopRequest{ExecID: "exec-1"})
	rec := httptest.NewRecorder()
	d.workerProcessStopHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/runtimes/processes/stop", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// The stop runs a close in a goroutine; wait for the reclaim to land.
	deadline := time.Now().Add(time.Second)
	for {
		if len(d.workerProcessRegistry.snapshot()) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker was not reclaimed after stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerProcessStopHandlerUnknownExecId(t *testing.T) {
	d := &Daemon{workerProcessRegistry: newWorkerProcessRegistry()}
	body, _ := json.Marshal(workerProcessStopRequest{ExecID: "missing"})
	rec := httptest.NewRecorder()
	d.workerProcessStopHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/runtimes/processes/stop", bytes.NewReader(body)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
