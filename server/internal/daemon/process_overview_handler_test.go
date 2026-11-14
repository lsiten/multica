package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestProcessesOverviewHandlerNilSafe(t *testing.T) {
	d := &Daemon{} // no registry, no singleton clients
	rec := httptest.NewRecorder()
	d.processesOverviewHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtimes/processes/overview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Processes []processOverviewEntry `json:"processes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	// An empty daemon still lists every managed category: the daemon's own
	// process plus the five long-lived service children, all reported as failed
	// required owners, so the panel never hides a supervised category behind a
	// nil child.
	if len(body.Processes) != 6 {
		t.Fatalf("expected 6 category rows for an empty daemon, got %d: %+v", len(body.Processes), body.Processes)
	}
	if body.Processes[0].Category != "daemon" {
		t.Fatalf("daemon row must be first, got %q", body.Processes[0].Category)
	}
	if body.Processes[0].Stoppable {
		t.Fatalf("the daemon process must not be stoppable from a per-row action")
	}
	for _, category := range []string{"application", "gateway", "environment", "mirror", "ai"} {
		row := findOverviewRow(t, body.Processes, category)
		if row.State != "failed" || row.Detail != "required-owner-missing" {
			t.Fatalf("singleton %q must report missing required owner, got state=%q detail=%q", category, row.State, row.Detail)
		}
	}
}

func TestProcessesOverviewReportsRequiredServicesAsStartingUntilChildrenReady(t *testing.T) {
	d := &Daemon{}
	d.ready.Store(true)
	d.cfg.ProcessServices = []string{"ai", "mirror", "application", "environment", "gateway"}
	d.applicationProcessActive.Store(true)
	d.gatewayProcessActive.Store(true)
	d.environmentProcessActive.Store(true)
	rec := httptest.NewRecorder()
	d.processesOverviewHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtimes/processes/overview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Processes []processOverviewEntry `json:"processes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, category := range []string{"application", "gateway", "environment", "mirror", "ai"} {
		row := findOverviewRow(t, body.Processes, category)
		if row.State != "starting" {
			t.Fatalf("singleton %q must report starting before the child is ready, got %q", category, row.State)
		}
		if row.Detail != "" {
			t.Fatalf("singleton %q must not claim an in-process owner, got %q", category, row.Detail)
		}
	}
}

func TestProcessesOverviewReportsConfigMirrorAsStarting(t *testing.T) {
	// The mirror is config-based and on-demand: when the role is configured but
	// no child is up yet it is "starting". The other long-lived services are
	// marked configured as well, so every required row has the same state.
	d := &Daemon{}
	d.ready.Store(true)
	d.cfg.ProcessServices = []string{"ai", "mirror", "application", "environment", "gateway"}
	d.applicationProcessActive.Store(true)
	d.gatewayProcessActive.Store(true)
	d.environmentProcessActive.Store(true)
	rec := httptest.NewRecorder()
	d.processesOverviewHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtimes/processes/overview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Processes []processOverviewEntry `json:"processes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if row := findOverviewRow(t, body.Processes, "mirror"); row.State != "starting" {
		t.Fatalf("config mirror must report starting, got %q", row.State)
	}
	for _, category := range []string{"application", "gateway", "environment", "ai"} {
		if row := findOverviewRow(t, body.Processes, category); row.State != "starting" {
			t.Fatalf("singleton %q must report starting, got %q", category, row.State)
		}
	}
}

func findOverviewRow(t *testing.T, rows []processOverviewEntry, category string) processOverviewEntry {
	t.Helper()
	for _, row := range rows {
		if row.Category == category {
			return row
		}
	}
	t.Fatalf("no overview row for category %q", category)
	return processOverviewEntry{}
}

func TestProcessesOverviewHandlerRejectsNonGet(t *testing.T) {
	d := &Daemon{workerProcessRegistry: newWorkerProcessRegistry()}
	rec := httptest.NewRecorder()
	d.processesOverviewHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/runtimes/processes/overview", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestProcessesOverviewHandlerRequiresLocalAuthorization(t *testing.T) {
	d := &Daemon{client: NewClient(""), cfg: Config{Profile: "profile-test"}}
	d.client.SetToken("local-secret")
	unauthorized := httptest.NewRecorder()
	d.processesOverviewHandler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/runtimes/processes/overview", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized overview request to return 401, got %d", unauthorized.Code)
	}
	authorized := httptest.NewRequest(http.MethodGet, "/runtimes/processes/overview", nil)
	authorized.Header.Set("Authorization", "Bearer local-secret")
	authorized.Header.Set("X-Multica-Profile", "profile-test")
	response := httptest.NewRecorder()
	d.processesOverviewHandler().ServeHTTP(response, authorized)
	if response.Code != http.StatusOK {
		t.Fatalf("expected authorized overview request to return 200, got %d", response.Code)
	}
}

func TestProcessesOverviewHandlerListsTaskWorker(t *testing.T) {
	c := &workerProcessClient{
		transport:  &fakeWorkerTransport{healthState: "running"},
		wait:       func(context.Context) error { return nil },
		gate:       make(chan struct{}, 1),
		instanceID: "instance-1",
	}
	d := &Daemon{workerProcessRegistry: newWorkerProcessRegistry()}
	d.registerWorkerProcess(c, "exec-1", "task-1", "claude")

	rec := httptest.NewRecorder()
	d.processesOverviewHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtimes/processes/overview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Processes []processOverviewEntry `json:"processes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	// The daemon-self row plus the four long-lived service children are always
	// present alongside the task worker, so the row must be found by category.
	p := findOverviewRow(t, body.Processes, "task_worker")
	if p.Category != "task_worker" || p.ID != "exec-1" || p.Name != "claude" || p.Detail != "task-1" {
		t.Fatalf("unexpected task worker overview row: %+v", p)
	}
	if p.State != "running" || !p.Ready {
		t.Fatalf("unexpected task worker state: %+v", p)
	}
	if !p.Stoppable {
		t.Fatalf("task worker must be stoppable in the overview")
	}
}

func TestSafeProcessPIDsNil(t *testing.T) {
	if got := safeProcessPID(nil); got != 0 {
		t.Fatalf("expected 0 for nil process, got %d", got)
	}
}

func TestDescribeRuntimeProcessNilProcess(t *testing.T) {
	out := describeRuntimeProcess(context.Background(), nil, processOverviewEntry{Category: "application"}, newProcessMetricsCache())
	if out.State != "unavailable" {
		t.Fatalf("expected unavailable for nil process, got %q", out.State)
	}
}

func TestDescribeRuntimeProcessNilClient(t *testing.T) {
	// A process whose control Client is nil must not panic and must degrade to
	// unavailable, exactly like a probe failure.
	p := &runtimeproc.Process{}
	out := describeRuntimeProcess(context.Background(), p, processOverviewEntry{Category: "gateway"}, newProcessMetricsCache())
	if out.State != "unavailable" {
		t.Fatalf("expected unavailable for nil client, got %q", out.State)
	}
}
