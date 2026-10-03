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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestJevDecisionReportsSurviveRestartAndRetryWithoutCredentials(t *testing.T) {
	var received []protocol.JevDecisionLog
	reject := true
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reject {
			w.WriteHeader(503)
			return
		}
		var report protocol.JevDecisionLog
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		received = append(received, report)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer backend.Close()
	cfg := Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: backend.URL, Profile: "qa", DaemonID: "daemon"}
	d := &Daemon{cfg: cfg, client: NewClient(backend.URL), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"decisions\":[{\"candidate_id\":\"a\",\"verdict\":\"yes\"}]}"}}]}`)
	}))
	defer model.Close()
	task := Task{ID: uuid.NewString(), AuthToken: "private-task-token", Agent: &AgentData{Model: "model", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": "private-provider-key"}}}
	ctx := withJevDecisionReporter(context.Background(), d.enqueueJevDecision)
	config, set, err := startTaskLLM2JevMCP(ctx, task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{"name": llm2jevMCPToolName, "arguments": map[string]any{"question": "Check private-task-token", "candidates": []map[string]string{{"id": "a", "content": "x"}}}})
	files, err := os.ReadDir(d.jevDecisionReportDir())
	if err != nil || len(files) != 2 {
		t.Fatalf("decisions were not saved before upload: %v / %d", err, len(files))
	}
	for _, file := range files {
		path := filepath.Join(d.jevDecisionReportDir(), file.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var report pendingJevDecision
		if err := json.Unmarshal(raw, &report); err != nil || report.Record.Validate() != nil {
			t.Fatalf("invalid persisted report: %v", err)
		}
		if strings.Contains(string(raw), "private-task-token") || strings.Contains(string(raw), "private-provider-key") {
			t.Fatal("credential persisted")
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("report file is not private")
		}
	}
	if d.flushJevDecisionReports(context.Background()) == nil {
		t.Fatal("temporary backend outage was not reported")
	}
	restarted := &Daemon{cfg: cfg, client: NewClient(backend.URL), logger: d.logger}
	reject = false
	if err := restarted.flushJevDecisionReports(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, err = os.ReadDir(d.jevDecisionReportDir())
	if err != nil || len(files) != 0 || len(received) != 2 {
		t.Fatalf("reports were lost or not acknowledged: %v / %d / %d", err, len(files), len(received))
	}
	if received[0].ID != received[1].ID {
		t.Fatal("start and completion were not correlated")
	}
	other := &Daemon{cfg: cfg}
	other.cfg.ServerBaseURL = "https://another-backend.example"
	if other.jevDecisionReportDir() == d.jevDecisionReportDir() {
		t.Fatal("different backends share their reports")
	}
}

func TestJevDecisionReportsRetainPermanentRejections(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer backend.Close()
	d := &Daemon{cfg: Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: backend.URL}, client: NewClient(backend.URL), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	record := protocol.JevDecisionLog{ID: uuid.NewString(), Tool: llm2jevMCPToolName, Source: "agent_context", Model: "model", StartedAt: time.Now().UTC(), ResultClass: "running", Input: "{}", Requests: []protocol.JevDecisionRequestLog{}}
	if err := d.enqueueJevDecision(uuid.NewString(), record); err != nil {
		t.Fatal(err)
	}
	if err := d.flushJevDecisionReports(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(d.jevDecisionReportDir(), "failed"))
	if err != nil || len(files) != 1 {
		t.Fatalf("rejected audit was discarded: %v / %d", err, len(files))
	}
}
