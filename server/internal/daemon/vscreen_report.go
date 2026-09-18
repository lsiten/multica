package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const vscreenReportLimit = 128

type vscreenReportError string

func (e vscreenReportError) Error() string { return string(e) }

// Configuration belongs to one authenticated account and its private profile.
// OnResult must return promptly and must not call Close; it reports persistence
// only, never native state.
type vscreenReportConfig struct {
	Path            string
	BackendIdentity string
	AccountID       string
	OnResult        func(protocol.VscreenIntervention, string)
	AckTimeout      time.Duration
	RetryDelay      time.Duration
	MaxAttempts     int
}

type vscreenReportEntry struct {
	Report      protocol.VscreenIntervention `json:"report"`
	Attempts    int                          `json:"attempts"`
	Version     int64                        `json:"version,omitempty"`
	Reason      string                       `json:"reason,omitempty"`
	NextAttempt time.Time                    `json:"next_attempt,omitempty"`
}

type vscreenReportDisk struct {
	Format          int                  `json:"format"`
	BackendIdentity string               `json:"backend_identity"`
	AccountID       string               `json:"account_id"`
	Entries         []vscreenReportEntry `json:"entries"`
}

type vscreenReportConnection struct {
	binding    uint64
	generation string
	send       func([]byte) (*wsOutbound, error)
}

type vscreenReportFlight struct {
	envelope       protocol.VscreenEnvelope
	interventionID string
	binding        uint64
	deadline       time.Time
	outbound       *wsOutbound
}

type vscreenReportAck struct {
	binding uint64
	ack     protocol.VscreenInterventionAck
}

type vscreenReporter struct {
	mu         sync.Mutex
	config     vscreenReportConfig
	disk       vscreenReportDisk
	connection vscreenReportConnection
	flight     *vscreenReportFlight
	sequence   uint64
	current    atomic.Uint64
	wake       chan struct{}
	acks       chan vscreenReportAck
	stop       chan struct{}
	done       chan struct{}
	closed     bool
	failure    error
}

func newVscreenReporter(config vscreenReportConfig) (*vscreenReporter, error) {
	if config.AckTimeout == 0 {
		config.AckTimeout = 12 * time.Second
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = time.Second
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = 8
	}
	if config.AckTimeout <= 0 || config.AckTimeout > time.Minute || config.RetryDelay <= 0 || config.RetryDelay > time.Minute || config.MaxAttempts < 1 || config.MaxAttempts > 16 {
		return nil, vscreenReportError("invalid_configuration")
	}
	disk, err := loadVscreenReportDisk(config)
	if err != nil {
		return nil, err
	}
	r := &vscreenReporter{config: config, disk: disk, wake: make(chan struct{}, 1), acks: make(chan vscreenReportAck, 16), stop: make(chan struct{}), done: make(chan struct{})}
	go r.run()
	return r, nil
}

// Queue is called only after the coordinator has verified provider drain and
// authoritative stopped/returned state. It stores immutable proof before sending.
func (r *vscreenReporter) Queue(ctx context.Context, report protocol.VscreenIntervention) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return vscreenReportError("reporter_closed")
	}
	if r.failure != nil {
		return r.failure
	}
	report.DaemonGeneration = "pending"
	originalRequest := report.RequestID
	if report.RequestID == "" {
		report.RequestID = uuid.NewString()
	}
	if !validQueuedVscreenReport(report) {
		return vscreenReportError("invalid_report")
	}
	var previous *vscreenReportEntry
	for i := range r.disk.Entries {
		entry := &r.disk.Entries[i]
		if entry.Report.RequestID == report.RequestID && (entry.Report.InterventionID != report.InterventionID || entry.Report.State != report.State) {
			return vscreenReportError("report_replayed")
		}
		if entry.Report.InterventionID != report.InterventionID {
			continue
		}
		if entry.Report.State == report.State {
			if originalRequest == "" {
				report.RequestID = entry.Report.RequestID
			}
			if entry.Report != report {
				return vscreenReportError("report_replayed")
			}
			if entry.Reason != "" {
				return vscreenReportError(entry.Reason)
			}
			return nil
		}
		previous = entry
	}
	if previous == nil {
		if report.State != protocol.VscreenInterventionAwaitingTakeover {
			return vscreenReportError("invalid_transition")
		}
	} else if !nextVscreenReport(previous.Report, report) || previous.Reason != "" {
		return vscreenReportError("invalid_transition")
	}
	if len(r.disk.Entries) >= vscreenReportLimit {
		return vscreenReportError("report_capacity")
	}
	next := append(append([]vscreenReportEntry(nil), r.disk.Entries...), vscreenReportEntry{Report: report})
	if err := r.persistLocked(next); err != nil {
		return err
	}
	r.signal()
	return nil
}

// Bind replaces the socket without replacing logical report IDs or native proof.
func (r *vscreenReporter) Bind(generation string, send func([]byte) (*wsOutbound, error)) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.failure != nil || generation == "" || send == nil {
		return 0
	}
	r.cancelFlightLocked()
	r.sequence++
	r.connection = vscreenReportConnection{binding: r.sequence, generation: generation, send: send}
	r.current.Store(r.sequence)
	r.signal()
	return r.sequence
}

func (r *vscreenReporter) Unbind(binding uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if binding == 0 || r.connection.binding != binding {
		return
	}
	r.cancelFlightLocked()
	r.connection = vscreenReportConnection{}
	r.current.Store(0)
	r.signal()
}

// OnAck never performs filesystem I/O or waits for the worker. Matching is
// repeated by the worker immediately before the durable acknowledgement write.
func (r *vscreenReporter) OnAck(binding uint64, raw json.RawMessage) bool {
	if binding == 0 || r.current.Load() != binding || len(raw) > 8192 {
		return false
	}
	var ack protocol.VscreenInterventionAck
	if json.Unmarshal(raw, &ack) != nil || ack.Validate() != nil {
		return false
	}
	select {
	case r.acks <- vscreenReportAck{binding: binding, ack: ack}:
		return true
	default:
		return false
	}
}

func (r *vscreenReporter) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}
func (r *vscreenReporter) cancelFlightLocked() {
	if r.flight != nil && r.flight.outbound != nil {
		r.flight.outbound.cancel()
	}
	r.flight = nil
}

// CancelScope purges only a removed runtime or workspace. Empty values select
// all scopes for this reporter's fixed backend/account, as required on logout.
func (r *vscreenReporter) CancelScope(workspaceID, runtimeID string) error {
	return r.removeScope(workspaceID, runtimeID, "")
}

// InvalidateEpoch discards proof from an obsolete native host without touching
// another workspace/runtime. A current-epoch report remains available to retry.
func (r *vscreenReporter) InvalidateEpoch(workspaceID, runtimeID, nativeEpoch string) error {
	if workspaceID == "" || runtimeID == "" || nativeEpoch == "" {
		return vscreenReportError("invalid_scope")
	}
	return r.removeScope(workspaceID, runtimeID, nativeEpoch)
}

func (r *vscreenReporter) removeScope(workspaceID, runtimeID, keepEpoch string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return vscreenReportError("reporter_closed")
	}
	next := make([]vscreenReportEntry, 0, len(r.disk.Entries))
	for _, entry := range r.disk.Entries {
		report := entry.Report
		remove := (workspaceID == "" || workspaceID == report.WorkspaceID) && (runtimeID == "" || runtimeID == report.RuntimeID) && (keepEpoch == "" || keepEpoch != report.Epoch.NativeEpoch)
		if !remove {
			next = append(next, entry)
		}
	}
	if err := r.persistLocked(next); err != nil {
		return err
	}
	if r.flight != nil {
		found := false
		for _, entry := range next {
			if entry.Report.RequestID == r.flight.envelope.RequestID {
				found = true
				break
			}
		}
		if !found {
			r.cancelFlightLocked()
		}
	}
	r.signal()
	return nil
}

func (r *vscreenReporter) Close() error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.current.Store(0)
		r.cancelFlightLocked()
		close(r.stop)
	}
	r.mu.Unlock()
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failure
}

// Acknowledged is a persistence checkpoint only. The coordinator must separately
// verify native authority before advancing a physical intervention transition.
func (r *vscreenReporter) Acknowledged(interventionID string, state protocol.VscreenInterventionState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.failure != nil {
		return false
	}
	for _, entry := range r.disk.Entries {
		if entry.Report.InterventionID == interventionID && entry.Report.State == state {
			return entry.Version > 0
		}
	}
	return false
}
