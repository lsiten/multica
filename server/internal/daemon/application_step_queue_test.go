package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationStepFinishesOfflineReclaimsSlotAndRecoversWithoutRestart(t *testing.T) {
	var resultCalls atomic.Int32
	var offline atomic.Bool
	offline.Store(true)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/lease") {
			w.WriteHeader(204)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/result") {
			resultCalls.Add(1)
			if offline.Load() {
				http.Error(w, "offline", 503)
			}
			return
		}
		t.Errorf("unexpected backend request: %s", r.URL.Path)
	}))
	defer backend.Close()
	d, command := applicationManagerFixture(t, backend.URL)
	d.cfg.ServerBaseURL = backend.URL
	d.accountID = "fixture-account"
	d.terminalReports = newTerminalReportStore(d.cfg)
	d.terminalReports.bindAccount(d.accountID)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := d.executeApplication(ctx, command); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stopOwnedApplications)
	command.Action, command.Generation = "restart", 2
	claim := protocol.ApplicationClaim{StepID: uuid.NewString(), OperationID: uuid.NewString(), ClaimToken: uuid.NewString(), Command: command}
	semaphore := make(chan struct{}, 1)
	d.runApplicationClaim(ctx, semaphore, command.RuntimeID, claim)
	if len(semaphore) != 0 || resultCalls.Load() != 0 {
		t.Fatal("application result waited for backend or held execution slot")
	}
	items, err := d.terminalReports.list()
	if err != nil || len(items) != 1 || items[0].report.applicationStep.Result.State != "completed" {
		t.Fatalf("local result was not durable: %+v %v", items, err)
	}
	if pending, delivered := d.replayPendingTerminalReports(ctx); pending != 1 || delivered != 0 {
		t.Fatalf("outage dropped result: %d/%d", pending, delivered)
	}
	restarted := &Daemon{cfg: d.cfg, client: NewClient(backend.URL), logger: d.logger, workspaces: d.workspaces, runtimeIndex: d.runtimeIndex, accountID: d.accountID, terminalReports: newTerminalReportStore(d.cfg), applicationHostLauncher: d.applicationHostLauncher}
	restarted.terminalReports.bindAccount(restarted.accountID)
	claim.ClaimToken = uuid.NewString()
	restarted.runApplicationClaim(ctx, semaphore, command.RuntimeID, claim)
	starts, err := os.ReadFile(command.Config.Environment["APPLICATION_START_FILE"])
	if err != nil || strings.Count(string(starts), "start\n") != 2 {
		t.Fatalf("reclaimed receipt repeated restart: %q %v", starts, err)
	}
	offline.Store(false)
	if pending, delivered := restarted.replayPendingTerminalReports(ctx); pending != 0 || delivered != 1 {
		t.Fatalf("recovered results not uploaded: %d/%d", pending, delivered)
	}
}
