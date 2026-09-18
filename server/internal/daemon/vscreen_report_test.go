package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type reportResult struct {
	report protocol.VscreenIntervention
	reason string
}

func reportTestConfig(t *testing.T) (vscreenReportConfig, chan reportResult) {
	t.Helper()
	results := make(chan reportResult, 128)
	return vscreenReportConfig{Path: filepath.Join(t.TempDir(), "private", "outbox.json"), BackendIdentity: "https://example.test", AccountID: "account-a", AckTimeout: 40 * time.Millisecond, RetryDelay: 10 * time.Millisecond, MaxAttempts: 3, OnResult: func(report protocol.VscreenIntervention, reason string) { results <- reportResult{report, reason} }}, results
}

func reportTestRecord() protocol.VscreenIntervention {
	return protocol.VscreenIntervention{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "workspace-a", RuntimeID: "runtime-a", RequestID: uuid.NewString()}, InterventionID: uuid.NewString(), AgentID: "agent-a", SourceTaskID: uuid.NewString(), Reason: protocol.VscreenRejectionReason("background_unsupported"), State: protocol.VscreenInterventionAwaitingTakeover, Epoch: protocol.VscreenEpoch{NativeEpoch: "native-a", DisplayGeneration: "display-a", GeometryRevision: 1}}
}

func reportTestNew(t *testing.T, config vscreenReportConfig) *vscreenReporter {
	t.Helper()
	r, err := newVscreenReporter(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return r
}

func reportTestSender(t *testing.T) (func([]byte) (*wsOutbound, error), chan protocol.VscreenIntervention) {
	t.Helper()
	sent := make(chan protocol.VscreenIntervention, 32)
	return func(raw []byte) (*wsOutbound, error) {
		var frame protocol.Message
		if err := json.Unmarshal(raw, &frame); err != nil {
			return nil, err
		}
		var report protocol.VscreenIntervention
		if err := json.Unmarshal(frame.Payload, &report); err != nil {
			return nil, err
		}
		sent <- report
		outbound := &wsOutbound{data: raw}
		outbound.beginWrite()
		return outbound, nil
	}, sent
}

func reportTestReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out awaiting owned report event")
		var empty T
		return empty
	}
}

func reportTestAck(r *vscreenReporter, binding uint64, report protocol.VscreenIntervention, version int64, reason string) bool {
	ack := protocol.VscreenInterventionAck{VscreenEnvelope: report.VscreenEnvelope, InterventionID: report.InterventionID, Accepted: reason == "", Version: version, Reason: reason}
	raw, _ := json.Marshal(ack)
	return r.OnAck(binding, raw)
}

func TestVscreenReportDurableRestartReconnectAndOrder(t *testing.T) {
	config, results := reportTestConfig(t)
	config.AckTimeout = time.Second
	r := reportTestNew(t, config)
	first := reportTestRecord()
	human := first
	human.State = protocol.VscreenInterventionHuman
	human.RequestID = uuid.NewString()
	ready := human
	ready.State = protocol.VscreenInterventionReadyToContinue
	ready.RequestID = uuid.NewString()
	ready.ReturnReceiptID = "native-receipt"
	for _, report := range []protocol.VscreenIntervention{first, human, ready} {
		if err := r.Queue(context.Background(), report); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{config.Path: 0600, filepath.Dir(config.Path): 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("private path %s mode=%v err=%v", path, info, err)
		}
	}
	restored := reportTestNew(t, config)
	send, sent := reportTestSender(t)
	old := restored.Bind("generation-1", send)
	lost := reportTestReceive(t, sent)
	restored.Unbind(old)
	current := restored.Bind("generation-2", send)
	replay := reportTestReceive(t, sent)
	if replay.RequestID != lost.RequestID || replay.Epoch != lost.Epoch || replay.DaemonGeneration == lost.DaemonGeneration {
		t.Fatalf("reconnect mutated proof: old=%+v new=%+v", lost, replay)
	}
	if reportTestAck(restored, old, lost, 1, "") {
		t.Fatal("old socket acknowledgement admitted")
	}
	wrong := replay
	wrong.WorkspaceID = "foreign-workspace"
	reportTestAck(restored, current, wrong, 1, "")
	if !reportTestAck(restored, current, replay, 1, "") {
		t.Fatal("current ack not queued")
	}
	if result := reportTestReceive(t, results); result.reason != "" || result.report.State != first.State {
		t.Fatalf("result=%+v", result)
	}
	for index, want := range []protocol.VscreenIntervention{human, ready} {
		report := reportTestReceive(t, sent)
		if report.RequestID != want.RequestID || report.State != want.State || report.ReturnReceiptID != want.ReturnReceiptID {
			t.Fatalf("ordered proof=%+v want=%+v", report, want)
		}
		reportTestAck(restored, current, report, int64(index+2), "")
		if result := reportTestReceive(t, results); result.reason != "" {
			t.Fatalf("result=%+v", result)
		}
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	again := reportTestNew(t, config)
	for _, report := range []protocol.VscreenIntervention{first, human, ready} {
		if !again.Acknowledged(report.InterventionID, report.State) {
			t.Fatal("recreated durable checkpoint missing")
		}
	}
	if again.Acknowledged("foreign-intervention", first.State) {
		t.Fatal("foreign checkpoint accepted")
	}
	again.mu.Lock()
	defer again.mu.Unlock()
	if len(again.disk.Entries) != 3 {
		t.Fatalf("entries=%d", len(again.disk.Entries))
	}
	for index, entry := range again.disk.Entries {
		if entry.Version != int64(index+1) {
			t.Fatalf("durable version=%d", entry.Version)
		}
	}
	t.Log("0600 file/0700 directory; recreation retained immutable ordered proof; lost ack retried on current generation; old/foreign ack did not clear pending; versions 1/2/3 durable")
}

func TestVscreenReportDeadlinesAndTerminalReasons(t *testing.T) {
	for _, reason := range []string{"ack_timeout", "source_not_stopped", "permission_denied", "stale_generation", "source_mismatch"} {
		t.Run(reason, func(t *testing.T) {
			config, results := reportTestConfig(t)
			r := reportTestNew(t, config)
			report := reportTestRecord()
			if err := r.Queue(context.Background(), report); err != nil {
				t.Fatal(err)
			}
			send, sent := reportTestSender(t)
			binding := r.Bind("generation", send)
			attempts := 1
			if retryableVscreenReportFailure(reason) {
				attempts = config.MaxAttempts
			}
			for range attempts {
				emitted := reportTestReceive(t, sent)
				if emitted.RequestID != report.RequestID {
					t.Fatal("retry changed request ID")
				}
				if reason != "ack_timeout" {
					reportTestAck(r, binding, emitted, 0, reason)
				}
			}
			result := reportTestReceive(t, results)
			if result.reason != reason {
				t.Fatalf("reason=%s", result.reason)
			}
			r.mu.Lock()
			entry := r.disk.Entries[0]
			r.mu.Unlock()
			if entry.Attempts != attempts || entry.Reason != reason {
				t.Fatalf("retry budget=%+v", entry)
			}
			if err := r.Queue(context.Background(), report); err == nil {
				t.Fatal("terminal report silently reset retry budget")
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			restored := reportTestNew(t, config)
			restored.mu.Lock()
			persisted := restored.disk.Entries[0]
			restored.mu.Unlock()
			if persisted.Attempts != attempts || persisted.Reason != reason {
				t.Fatal("restart reset rejection budget")
			}
			t.Logf("fixed reason=%s persisted after %d attempts; no unbounded retry or reset on recreation", reason, attempts)
		})
	}
}

func TestVscreenReportScopeCancellationAndEpochInvalidation(t *testing.T) {
	config, results := reportTestConfig(t)
	config.AckTimeout = time.Second
	r := reportTestNew(t, config)
	a := reportTestRecord()
	b := reportTestRecord()
	b.WorkspaceID = "workspace-b"
	b.RuntimeID = "runtime-b"
	for _, report := range []protocol.VscreenIntervention{a, b} {
		if err := r.Queue(context.Background(), report); err != nil {
			t.Fatal(err)
		}
	}
	send, sent := reportTestSender(t)
	binding := r.Bind("generation", send)
	removed := reportTestReceive(t, sent)
	if err := r.CancelScope(a.WorkspaceID, a.RuntimeID); err != nil {
		t.Fatal(err)
	}
	survivor := reportTestReceive(t, sent)
	if survivor.InterventionID != b.InterventionID {
		t.Fatal("wrong scope survived")
	}
	reportTestAck(r, binding, removed, 1, "")
	reportTestAck(r, binding, survivor, 1, "")
	if result := reportTestReceive(t, results); result.report.InterventionID != b.InterventionID {
		t.Fatal("removed scope emitted accepted result")
	}
	if err := r.InvalidateEpoch(b.WorkspaceID, b.RuntimeID, "new-native"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	count := len(r.disk.Entries)
	r.mu.Unlock()
	if count != 0 {
		t.Fatalf("obsolete epoch records=%d", count)
	}
	if err := r.Queue(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := r.CancelScope("", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	restored := reportTestNew(t, config)
	restored.mu.Lock()
	defer restored.mu.Unlock()
	if len(restored.disk.Entries) != 0 {
		t.Fatal("logout failed to purge durable metadata")
	}
	t.Log("runtime removal rejects racing ack; unrelated runtime preserved; native epoch invalidates exact scope; logout purges account metadata")
}

func TestVscreenReportInvalidTransitionsAndCancellation(t *testing.T) {
	config, _ := reportTestConfig(t)
	r := reportTestNew(t, config)
	report := reportTestRecord()
	human := report
	human.State = protocol.VscreenInterventionHuman
	if err := r.Queue(context.Background(), human); err == nil {
		t.Fatal("human without awaiting accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Queue(ctx, report); err != context.Canceled {
		t.Fatalf("cancelled queue=%v", err)
	}
	if err := r.Queue(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	if err := r.Queue(context.Background(), human); err == nil {
		t.Fatal("request ID reused for a distinct state")
	}
	human.RequestID = uuid.NewString()
	human.Epoch.NativeEpoch = "changed"
	if err := r.Queue(context.Background(), human); err == nil {
		t.Fatal("native epoch changed inside one intervention")
	}
	queued := make(chan *wsOutbound, 1)
	r.Bind("generation", func(raw []byte) (*wsOutbound, error) { out := &wsOutbound{data: raw}; queued <- out; return out, nil })
	out := reportTestReceive(t, queued)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if out.beginWrite() {
		t.Fatal("closed reporter left an unsent frame live")
	}
	if err := r.Queue(context.Background(), report); err == nil {
		t.Fatal("queue after close")
	}
	t.Log("invalid transitions/reused IDs/changed native proof rejected; cancelled context does not queue; close joins worker and cancels unsent frame")
}

func TestVscreenReportAckReadPumpDoesNotWaitForStoreLock(t *testing.T) {
	config, results := reportTestConfig(t)
	config.AckTimeout = time.Second
	r := reportTestNew(t, config)
	if err := r.Queue(context.Background(), reportTestRecord()); err != nil {
		t.Fatal(err)
	}
	send, sent := reportTestSender(t)
	binding := r.Bind("generation", send)
	report := reportTestReceive(t, sent)
	r.mu.Lock()
	returned := make(chan bool, 1)
	go func() { returned <- reportTestAck(r, binding, report, 1, "") }()
	select {
	case queued := <-returned:
		if !queued {
			r.mu.Unlock()
			t.Fatal("ack was not admitted")
		}
	case <-time.After(time.Second):
		r.mu.Unlock()
		t.Fatal("ack blocked on persistence lock")
	}
	r.mu.Unlock()
	if result := reportTestReceive(t, results); result.reason != "" {
		t.Fatalf("result=%+v", result)
	}
	t.Log("read-pump ack admission returned while durable state mutex was held")
}

func TestVscreenReportSendBackpressureIsBounded(t *testing.T) {
	config, results := reportTestConfig(t)
	r := reportTestNew(t, config)
	if err := r.Queue(context.Background(), reportTestRecord()); err != nil {
		t.Fatal(err)
	}
	r.Bind("generation", func([]byte) (*wsOutbound, error) { return nil, errWSRPCWriteBufferFull })
	if result := reportTestReceive(t, results); result.reason != "send_failed" {
		t.Fatalf("result=%+v", result)
	}
	r.mu.Lock()
	attempts := r.disk.Entries[0].Attempts
	r.mu.Unlock()
	if attempts != config.MaxAttempts {
		t.Fatalf("backpressure attempts=%d", attempts)
	}
}
