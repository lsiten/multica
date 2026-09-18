//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenRunTaskPreparedCLIStopsReportsAndAcknowledges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"owned-runtime":{"command":"/owned/not-executed"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' vscreen-provider-fixture \"$@\"\n"
	if err = os.WriteFile(fixture, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d := vscreenFixtureDaemon(t)
	var mu sync.Mutex
	var stages []string
	var messages []TaskMessageData
	var terminal map[string]any
	var stopped bool
	serverResult := make(chan error, 1)
	serverReport := make(chan protocol.VscreenIntervention, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ws":
			if r.Header.Get("Authorization") != "Bearer owned-daemon" {
				http.Error(w, "unauthorized", 401)
				return
			}
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, http.Header{protocol.DaemonGenerationHeader: []string{"owned-generation"}})
			if err != nil {
				serverResult <- err
				return
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			var frame protocol.Message
			if err = conn.ReadJSON(&frame); err != nil {
				serverResult <- err
				return
			}
			var report protocol.VscreenIntervention
			if frame.Type != protocol.EventVscreenIntervention || json.Unmarshal(frame.Payload, &report) != nil {
				serverResult <- errors.New("missing intervention report")
				return
			}
			mu.Lock()
			valid := terminal["failure_reason"] == protocol.VscreenPauseReasonHumanIntervention && stopped
			stages = append(stages, "intervention-ws")
			mu.Unlock()
			if !valid {
				serverResult <- errors.New("intervention published before confirmed source stop")
				return
			}
			query := protocol.VscreenQuery{VscreenEnvelope: report.VscreenEnvelope, Kind: "state"}
			query.RequestID = "owned-native-readback"
			if err = conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenQuery, Payload: marshalRaw(query)}); err != nil {
				serverResult <- err
				return
			}
			if err = conn.ReadJSON(&frame); err != nil {
				serverResult <- err
				return
			}
			var proof protocol.VscreenQueryResult
			if frame.Type != protocol.EventVscreenQueryResult || json.Unmarshal(frame.Payload, &proof) != nil || proof.State == nil || proof.State.ControlState != protocol.VscreenControlAwaitingTakeover || proof.State.InterventionID == nil || *proof.State.InterventionID != report.InterventionID || proof.State.InterventionState != report.State {
				serverResult <- fmt.Errorf("native snapshot does not confirm stopped report: %s", frame.Payload)
				return
			}
			ack := protocol.VscreenInterventionAck{VscreenEnvelope: report.VscreenEnvelope, InterventionID: report.InterventionID, Accepted: true, Version: 1}
			if err = conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenInterventionAck, Payload: marshalRaw(ack)}); err != nil {
				serverResult <- err
				return
			}
			serverReport <- report
			serverResult <- nil
			// Keep the socket alive until the test has observed durable acknowledgement.
			_, _, _ = conn.ReadMessage()
		case r.URL.Path == "/api/me":
			json.NewEncoder(w).Encode(map[string]string{"id": "owned-account"})
		case r.URL.Path == "/fixture/provider-started":
			mu.Lock()
			stages = append(stages, "provider-started")
			mu.Unlock()
			w.WriteHeader(200)
		case r.URL.Path == "/fixture/provider-stopped":
			actor, err := d.VscreenActor("ws", "rt")
			if err != nil || !actor.Status().Frozen {
				http.Error(w, "actor not frozen", 409)
				return
			}
			s := d.vscreenRuntime()
			s.interventions.mu.Lock()
			record := s.interventions.records["rt"]
			s.interventions.mu.Unlock()
			if record == nil {
				http.Error(w, "intervention missing", 409)
				return
			}
			if err = s.client.Renew(r.Context(), record.Authority, time.Second); err == nil {
				http.Error(w, "native authority not revoked", 409)
				return
			}
			mu.Lock()
			stopped = true
			stages = append(stages, "provider-stopped-after-native-revoke")
			mu.Unlock()
			w.WriteHeader(200)
		case strings.HasSuffix(r.URL.Path, "/messages"):
			var body struct {
				Messages []TaskMessageData `json:"messages"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				http.Error(w, "bad messages", 400)
				return
			}
			mu.Lock()
			messages = append(messages, body.Messages...)
			stages = append(stages, "transcript-flush")
			mu.Unlock()
			w.WriteHeader(200)
		case strings.HasSuffix(r.URL.Path, "/fail"):
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				http.Error(w, "bad terminal", 400)
				return
			}
			mu.Lock()
			terminal = body
			stages = append(stages, "terminal-http")
			mu.Unlock()
			w.WriteHeader(200)
		default:
			w.WriteHeader(200)
		}
	}))
	defer server.Close()
	d.cfg.ServerBaseURL = server.URL
	d.client = NewClient(server.URL)
	d.client.SetToken("owned-daemon")
	d.cfg.WorkspacesRoot = t.TempDir()
	d.cfg.AgentTimeout = 20 * time.Second
	d.cfg.Agents = map[string]AgentEntry{"claude": {Path: fixture}}
	d.activeEnvRoots = map[string]int{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key, err := d.vscreenResource("ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	s := d.vscreenRuntime()
	s.mu.Lock()
	err = d.startVscreenHost(ctx, s)
	s.enabled[key] = true
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	actor, err := s.manager.For(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = actor.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	reporter, err := d.initVscreenReporter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.closeVscreenReporter()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", http.Header{"Authorization": []string{"Bearer owned-daemon"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	generation, readerCtx, stopReader := d.beginMirrorControlConnection(ctx)
	defer stopReader()
	d.vscreenServerGeneration = response.Header.Get(protocol.DaemonGenerationHeader)
	writes := make(chan *wsOutbound, 8)
	writerDone := make(chan struct{})
	go d.runWSWriter(conn, writes, writerDone)
	enqueue := func(raw []byte) (*wsOutbound, error) {
		out := &wsOutbound{data: raw}
		select {
		case writes <- out:
			return out, nil
		default:
			return nil, errWSRPCWriteBufferFull
		}
	}
	binding := reporter.Bind(d.vscreenServerGeneration, enqueue)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		_ = d.readTaskWakeupMessagesForConnectionAndWriter(readerCtx, taskWakeupReader{conn: conn, mirrorControlGeneration: generation, enqueue: enqueue, vscreenReporter: reporter, vscreenReportBinding: binding})
	}()
	defer func() {
		reporter.Unbind(binding)
		conn.Close()
		stopReader()
		<-readerDone
		close(writes)
		<-writerDone
	}()
	task := Task{ID: "owned-run-task", RuntimeID: "rt", WorkspaceID: "ws", IssueID: "owned-issue", AgentID: "owned-agent", AuthToken: "mat_owned_fixture", Agent: &AgentData{ID: "owned-agent", Name: "Owned fixture", McpConfig: json.RawMessage(`{"mcpServers":{"owned-agent":{"command":"/owned/not-executed"}}}`), CustomEnv: map[string]string{"VSCREEN_FIXTURE_ENDPOINT": server.URL}}}
	result, err := d.runTask(ctx, task, "claude", 0, d.logger)
	if err != nil {
		t.Fatalf("prepared runTask: %v", err)
	}
	if result.FailureReason != protocol.VscreenPauseReasonHumanIntervention || result.Status != "blocked" || result.SessionID != "owned-vscreen-session" || result.WorkDir == "" || len(result.Usage) == 0 {
		t.Fatalf("stopped provider result: %+v", result)
	}
	d.reportTaskResult(ctx, task.ID, result, d.logger)
	select {
	case err = <-serverResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("full-chain acknowledgement timed out")
	}
	report := <-serverReport
	if report.Reason != protocol.VscreenActionUncertainReason {
		t.Fatalf("persisted intervention reason=%s, want action_uncertain", report.Reason)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !reporter.Acknowledged(report.InterventionID, report.State) {
		select {
		case <-deadline.C:
			t.Fatal("ack not durably consumed")
		case <-tick.C:
		}
	}
	mu.Lock()
	captured := append([]TaskMessageData(nil), messages...)
	ordered := append([]string(nil), stages...)
	mu.Unlock()
	encoded, _ := json.Marshal(captured)
	if strings.Contains(string(encoded), "private-gui-marker") {
		t.Fatal("native input leaked into transcript")
	}
	if !strings.Contains(string(encoded), "owned-provider-cleanup-tail") {
		t.Fatalf("provider cleanup transcript lost: stages=%v messages=%s", ordered, encoded)
	}
	if d.runningTasks.Load() != 0 {
		t.Fatal("execution slot still active")
	}
	t.Logf("prepared owned CLI inherited runtime+agent MCP, HTTP->FD6 unknown action froze/revoked before process termination; transcript and session drained; terminal HTTP preceded WS native query and durable ACK; stages=%v", ordered)
}
