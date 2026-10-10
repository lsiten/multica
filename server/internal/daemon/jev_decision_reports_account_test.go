package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// acceptBackend accepts every Jev decision report and returns 200.
func acceptJevDecisionBackend(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report protocol.JevDecisionLog
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			w.WriteHeader(400)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
}

func mkJevDecisionRecord() protocol.JevDecisionLog {
	return protocol.JevDecisionLog{
		ID:          uuid.NewString(),
		Tool:        llm2jevMCPToolName,
		Source:      "agent_context",
		Model:       "model",
		StartedAt:   time.Now().UTC(),
		ResultClass: "running",
		Input:       "{}",
		Requests:    []protocol.JevDecisionRequestLog{},
	}
}

// TestJevDecisionReportsAccountAwareQuarantinesOther verifies the F2 account
// boundary for the Jev decision namespace: two daemons on the same
// backend/profile/daemon share one report dir, but an account-aware daemon
// adopts only its own account's record and preserves (never uploads, never
// deletes) another account's record.
func TestJevDecisionReportsAccountAwareQuarantinesOther(t *testing.T) {
	backend := acceptJevDecisionBackend(t)
	defer backend.Close()
	cfg := Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: backend.URL, Profile: "qa", DaemonID: "daemon"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Daemon{cfg: cfg, client: NewClient(backend.URL), logger: logger, accountID: "account-a"}
	b := &Daemon{cfg: cfg, client: NewClient(backend.URL), logger: logger, accountID: "account-b"}

	if err := a.enqueueJevDecision(uuid.NewString(), mkJevDecisionRecord()); err != nil {
		t.Fatal(err)
	}
	if err := b.enqueueJevDecision(uuid.NewString(), mkJevDecisionRecord()); err != nil {
		t.Fatal(err)
	}

	if err := a.flushJevDecisionReports(context.Background()); err != nil {
		t.Fatalf("account-aware flush: %v", err)
	}

	files, err := os.ReadDir(a.jevDecisionReportDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("pending after account-aware flush = %d, want 1 preserved record", len(files))
	}
	raw, err := os.ReadFile(filepath.Join(a.jevDecisionReportDir(), files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var report pendingJevDecision
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal preserved record: %v", err)
	}
	if report.AccountID != "account-b" {
		t.Fatalf("preserved record account = %q, want account-b (the quarantined one)", report.AccountID)
	}
}

// TestJevDecisionReportsLegacyModeUploadsAll verifies that a daemon with no
// resolved account uploads every record, so the account boundary is opt-in and
// never silently changes pre-account behaviour.
func TestJevDecisionReportsLegacyModeUploadsAll(t *testing.T) {
	backend := acceptJevDecisionBackend(t)
	defer backend.Close()
	cfg := Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: backend.URL, Profile: "qa", DaemonID: "daemon"}
	d := &Daemon{cfg: cfg, client: NewClient(backend.URL), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if err := d.enqueueJevDecision(uuid.NewString(), mkJevDecisionRecord()); err != nil {
		t.Fatal(err)
	}
	if err := d.flushJevDecisionReports(context.Background()); err != nil {
		t.Fatalf("legacy flush: %v", err)
	}
	files, err := os.ReadDir(d.jevDecisionReportDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("legacy mode left %d records pending, want 0", len(files))
	}
}
