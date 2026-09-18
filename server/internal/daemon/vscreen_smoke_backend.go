package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// This backend is deliberately a scoped wire fixture, not an in-memory claim of
// production database or browser acceptance. Native state still comes from the daemon.
type takeoverSmokeBackend struct {
	mu                                                                                sync.Mutex
	writeMu                                                                           sync.Mutex
	URL, nonce, workspaceID, runtimeID, agentID, sourceID, continuationID, generation string
	daemon                                                                            *Daemon
	server                                                                            *http.Server
	listener                                                                          net.Listener
	done                                                                              chan struct{}
	peer                                                                              *websocket.Conn
	client                                                                            *websocket.Conn
	writes                                                                            chan *wsOutbound
	writerDone, readerDone                                                            chan struct{}
	readerCancel                                                                      context.CancelFunc
	binding                                                                           uint64
	stages                                                                            []string
	providerStopped, transcriptDrained, terminalReported, freshObserveBeforeInput     bool
	sourceObserved, continuationObserved                                              smokeProviderEvent
	interventionID                                                                    string
	versions                                                                          map[protocol.VscreenInterventionState]int64
	failure                                                                           error
	cleanupCommand                                                                    string
	cleanupAccepted                                                                   bool
}

func newTakeoverSmokeBackend(nonce string) (*takeoverSmokeBackend, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := &takeoverSmokeBackend{nonce: nonce, URL: "http://" + listener.Addr().String(), generation: uuid.NewString(), listener: listener, done: make(chan struct{}), versions: map[protocol.VscreenInterventionState]int64{}}
	b.server = &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second}
	go func() { defer close(b.done); _ = b.server.Serve(listener) }()
	return b, nil
}
func (b *takeoverSmokeBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.nonce)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.URL.Path == "/ws" {
		b.serveWS(w, r)
		return
	}
	if r.URL.Path == "/api/me" {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "owned-smoke-account"})
		return
	}
	if r.URL.Path == "/smoke/event" {
		var event smokeProviderEvent
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&event) != nil {
			http.Error(w, "invalid event", 400)
			return
		}
		if err := b.event(r.Context(), event); err != nil {
			b.fail(err)
			http.Error(w, "invalid smoke stage", 409)
			return
		}
		w.WriteHeader(200)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/daemon/tasks/"+b.sourceID+"/") && !strings.HasPrefix(r.URL.Path, "/api/daemon/tasks/"+b.continuationID+"/") {
		http.Error(w, "unknown smoke scope", 404)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/messages") {
		var body struct {
			Messages []TaskMessageData `json:"messages"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body) != nil {
			http.Error(w, "invalid messages", 400)
			return
		}
		b.mu.Lock()
		for _, message := range body.Messages {
			if message.Content == "smoke provider cleanup drained" {
				b.transcriptDrained = true
			}
		}
		b.mu.Unlock()
	}
	if strings.HasSuffix(r.URL.Path, "/fail") {
		var body struct {
			Reason string `json:"failure_reason"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid terminal", 400)
			return
		}
		b.mu.Lock()
		valid := body.Reason == protocol.VscreenPauseReasonHumanIntervention && b.providerStopped && b.transcriptDrained && strings.Contains(r.URL.Path, b.sourceID)
		if valid {
			b.terminalReported = true
			b.stages = append(b.stages, "terminal-http")
		}
		b.mu.Unlock()
		if !valid {
			b.fail(errors.New("terminal_before_provider_drain"))
			http.Error(w, "terminal ordering", 409)
			return
		}
	}
	w.WriteHeader(200)
}
func (b *takeoverSmokeBackend) event(ctx context.Context, event smokeProviderEvent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch event.Stage {
	case "source-observed":
		if event.TaskID != b.sourceID || event.Window == "" || event.Revision == 0 || event.Element == "" {
			return errors.New("invalid_source_observation")
		}
		b.sourceObserved = event
	case "provider-stopped":
		if event.TaskID != b.sourceID || b.sourceObserved.Revision == 0 {
			return errors.New("unexpected_provider_stop")
		}
		actor, err := b.daemon.VscreenActor(b.workspaceID, b.runtimeID)
		if err != nil || !actor.Status().Frozen {
			return errors.New("stop_before_native_freeze")
		}
		s := b.daemon.vscreenRuntime()
		s.interventions.mu.Lock()
		record := s.interventions.records[b.runtimeID]
		s.interventions.mu.Unlock()
		if record == nil || !smokeNativeRefused(s.client.Renew(ctx, record.Authority, time.Second)) {
			return errors.New("stop_before_native_revoke")
		}
		b.providerStopped = true
	case "continuation-observed":
		if event.TaskID != b.continuationID || event.Window != b.sourceObserved.Window || event.Revision <= b.sourceObserved.Revision || b.versions[protocol.VscreenInterventionReadyToContinue] == 0 {
			return errors.New("invalid_fresh_observation")
		}
		b.continuationObserved = event
	case "continuation-input":
		if event.TaskID != b.continuationID || b.continuationObserved.Revision == 0 {
			return errors.New("input_before_fresh_observe")
		}
		b.freshObserveBeforeInput = true
	default:
		return errors.New("unknown_smoke_stage")
	}
	b.stages = append(b.stages, event.Stage)
	return nil
}
func (b *takeoverSmokeBackend) fail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failure == nil {
		b.failure = err
	}
}
func (b *takeoverSmokeBackend) send(conn *websocket.Conn, event string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	return conn.WriteJSON(protocol.Message{Type: event, Payload: raw})
}
func (b *takeoverSmokeBackend) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, http.Header{protocol.DaemonGenerationHeader: []string{b.generation}})
	if err != nil {
		b.fail(err)
		return
	}
	defer conn.Close()
	b.mu.Lock()
	b.peer = conn
	b.mu.Unlock()
	for {
		var frame protocol.Message
		if err = conn.ReadJSON(&frame); err != nil {
			return
		}
		switch frame.Type {
		case protocol.EventVscreenIntervention:
			var report protocol.VscreenIntervention
			if json.Unmarshal(frame.Payload, &report) != nil || report.Validate() != nil || report.WorkspaceID != b.workspaceID || report.RuntimeID != b.runtimeID || report.AgentID != b.agentID || report.SourceTaskID != b.sourceID || report.DaemonGeneration != b.generation {
				b.fail(errors.New("invalid_smoke_report"))
				return
			}
			b.mu.Lock()
			valid := b.terminalReported && (b.interventionID == "" || b.interventionID == report.InterventionID)
			version := int64(1)
			switch report.State {
			case protocol.VscreenInterventionAwaitingTakeover:
			case protocol.VscreenInterventionHuman:
				valid = valid && b.versions[protocol.VscreenInterventionAwaitingTakeover] > 0
				version = 2
			case protocol.VscreenInterventionReadyToContinue:
				valid = valid && b.versions[protocol.VscreenInterventionHuman] > 0 && report.ReturnReceiptID != ""
				version = 3
			default:
				valid = false
			}
			b.mu.Unlock()
			if !valid {
				b.fail(errors.New("invalid_report_order"))
				return
			}
			state, err := b.query(conn, report.VscreenEnvelope)
			if err != nil {
				b.fail(err)
				return
			}
			if state.InterventionID == nil || *state.InterventionID != report.InterventionID || state.InterventionState != report.State || state.ReturnReceiptID != report.ReturnReceiptID || state.NativeEpoch != report.Epoch.NativeEpoch || state.DisplayGeneration != report.Epoch.DisplayGeneration || state.GeometryRevision != report.Epoch.GeometryRevision {
				b.fail(errors.New("native_report_proof_mismatch"))
				return
			}
			b.mu.Lock()
			b.interventionID = report.InterventionID
			b.versions[report.State] = version
			b.stages = append(b.stages, string(report.State)+"-ack")
			b.mu.Unlock()
			if err = b.send(conn, protocol.EventVscreenInterventionAck, protocol.VscreenInterventionAck{VscreenEnvelope: report.VscreenEnvelope, InterventionID: report.InterventionID, Accepted: true, Version: version}); err != nil {
				b.fail(err)
				return
			}
		case protocol.EventVscreenResult:
			var receipt protocol.VscreenCommandReceipt
			if json.Unmarshal(frame.Payload, &receipt) != nil || receipt.Validate() != nil {
				return
			}
			if receipt.State == protocol.VscreenReceiptPending {
				continue
			}
			b.mu.Lock()
			expected := b.cleanupCommand
			b.mu.Unlock()
			if receipt.CommandID != expected || receipt.State != protocol.VscreenReceiptSucceeded {
				b.fail(errors.New("native_cleanup_failed"))
				return
			}
			state, err := b.query(conn, receipt.VscreenEnvelope)
			if err != nil || state.State != protocol.VscreenStateDisabled {
				b.fail(errors.New("native_cleanup_readback_failed"))
				return
			}
			if err = b.send(conn, protocol.EventVscreenResult, receipt); err != nil {
				b.fail(err)
				return
			}
			b.mu.Lock()
			b.cleanupAccepted = true
			b.mu.Unlock()
		default:
			b.fail(errors.New("unexpected_smoke_frame"))
			return
		}
	}
}
func (b *takeoverSmokeBackend) query(conn *websocket.Conn, envelope protocol.VscreenEnvelope) (protocol.VscreenStateSnapshot, error) {
	envelope.RequestID = uuid.NewString()
	if err := b.send(conn, protocol.EventVscreenQuery, protocol.VscreenQuery{VscreenEnvelope: envelope, Kind: "state"}); err != nil {
		return protocol.VscreenStateSnapshot{}, err
	}
	var frame protocol.Message
	if err := conn.ReadJSON(&frame); err != nil {
		return protocol.VscreenStateSnapshot{}, err
	}
	var result protocol.VscreenQueryResult
	if frame.Type != protocol.EventVscreenQueryResult || json.Unmarshal(frame.Payload, &result) != nil || result.VscreenEnvelope != envelope || result.State == nil || result.Reason != "" {
		return protocol.VscreenStateSnapshot{}, errors.New("smoke_query_failed")
	}
	return *result.State, nil
}
func (b *takeoverSmokeBackend) connect(ctx context.Context, d *Daemon) error {
	reporter, err := d.initVscreenReporter(ctx)
	if err != nil {
		return err
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(b.URL, "http")+"/ws", http.Header{"Authorization": []string{"Bearer " + b.nonce}})
	if err != nil {
		return err
	}
	g, readerCtx, cancel := d.beginMirrorControlConnection(ctx)
	d.vscreenServerGeneration = response.Header.Get(protocol.DaemonGenerationHeader)
	b.client = conn
	b.readerCancel = cancel
	b.writes = make(chan *wsOutbound, 8)
	b.writerDone = make(chan struct{})
	b.readerDone = make(chan struct{})
	go d.runWSWriter(conn, b.writes, b.writerDone)
	enqueue := func(raw []byte) (*wsOutbound, error) {
		out := &wsOutbound{data: raw}
		select {
		case b.writes <- out:
			return out, nil
		default:
			return nil, errWSRPCWriteBufferFull
		}
	}
	b.binding = reporter.Bind(d.vscreenServerGeneration, enqueue)
	go func() {
		defer close(b.readerDone)
		defer d.suspendVscreens(g)
		_ = d.readTaskWakeupMessagesForConnectionAndWriter(readerCtx, taskWakeupReader{conn: conn, mirrorControlGeneration: g, enqueue: enqueue, vscreenReporter: reporter, vscreenReportBinding: b.binding})
	}()
	return nil
}
func (b *takeoverSmokeBackend) waitAck(ctx context.Context, state protocol.VscreenInterventionState) error {
	return waitSmokeCondition(ctx, func() (bool, error) {
		b.mu.Lock()
		id, err := b.interventionID, b.failure
		b.mu.Unlock()
		if err != nil {
			return false, err
		}
		return b.daemon.vscreenReporter.Acknowledged(id, state), nil
	})
}
func waitSmokeCondition(ctx context.Context, ready func() (bool, error)) error {
	wait, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		ok, err := ready()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-wait.Done():
			return wait.Err()
		case <-tick.C:
		}
	}
}
func (b *takeoverSmokeBackend) connected() bool { return b.client != nil }
func (b *takeoverSmokeBackend) disable(ctx context.Context, d *Daemon) error {
	b.mu.Lock()
	peer := b.peer
	b.cleanupCommand = uuid.NewString()
	id := b.cleanupCommand
	b.mu.Unlock()
	if peer == nil {
		return errors.New("smoke_socket_missing")
	}
	command := protocol.VscreenCommand{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: b.workspaceID, RuntimeID: b.runtimeID, DaemonGeneration: b.generation, RequestID: uuid.NewString()}, CommandID: id, Kind: protocol.VscreenCommandDisable}
	if err := b.send(peer, protocol.EventVscreenCommand, command); err != nil {
		return err
	}
	return waitSmokeCondition(ctx, func() (bool, error) {
		b.mu.Lock()
		accepted, err := b.cleanupAccepted, b.failure
		b.mu.Unlock()
		if err != nil {
			return false, err
		}
		s := d.vscreenRuntime()
		s.interventions.mu.Lock()
		retired := s.interventions.records[b.runtimeID] == nil
		s.interventions.mu.Unlock()
		return accepted && retired, nil
	})
}
func (b *takeoverSmokeBackend) closeLink() {
	if b.client == nil {
		return
	}
	if b.daemon.vscreenReporter != nil {
		b.daemon.vscreenReporter.Unbind(b.binding)
	}
	b.client.Close()
	b.readerCancel()
	<-b.readerDone
	close(b.writes)
	<-b.writerDone
	b.client = nil
}
func (b *takeoverSmokeBackend) Close() { b.closeLink(); _ = b.server.Close(); <-b.done }
