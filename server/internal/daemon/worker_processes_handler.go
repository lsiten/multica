package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// workerProcessStateResponse is the JSON shape a local caller gets for one
// active per-execution task worker. It carries no credential: the runtime token
// lives only on the worker's private channel.
type workerProcessStateResponse struct {
	ExecID       string   `json:"exec_id"`
	InstanceID   string   `json:"instance_id"`
	TaskID       string   `json:"task_id"`
	Provider     string   `json:"provider"`
	State        string   `json:"state"`
	Ready        bool     `json:"ready"`
	Capabilities []string `json:"capabilities,omitempty"`
	StartedAt    string   `json:"started_at"`
}

func (d *Daemon) workerProcessStateResponse(r *http.Request, entry *activeWorker) workerProcessStateResponse {
	out := workerProcessStateResponse{
		ExecID:     entry.execID,
		InstanceID: entry.instanceID,
		TaskID:     entry.taskID,
		Provider:   entry.provider,
		StartedAt:  entry.startedAt.Format(time.RFC3339),
	}
	if entry.worker == nil {
		out.State = "unavailable"
		return out
	}
	probeCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	status, err := entry.worker.transport.Health(probeCtx)
	if err != nil {
		// A probe failure means the owner is suspect, not stopped; the worker
		// is still registered, so report it rather than hide it.
		out.State = "unavailable"
		return out
	}
	out.State = status.State
	out.Ready = status.State == "ready" || status.State == "running"
	out.Capabilities = status.Capabilities
	return out
}

// workerProcessesHandler lists the daemon's live per-execution task workers and
// their current runtimeproc state. It is a local read: it serves the local
// process-status panel and never touches the server.
func (d *Daemon) workerProcessesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if d.workerProcessRegistry == nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"processes": []workerProcessStateResponse{}})
			return
		}
		entries := d.workerProcessRegistry.snapshot()
		workers := make([]workerProcessStateResponse, 0, len(entries))
		for _, entry := range entries {
			workers = append(workers, d.workerProcessStateResponse(r, entry))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"processes": workers})
	}
}

// workerProcessStopRequest is the body of a stop request for one worker.
type workerProcessStopRequest struct {
	ExecID string `json:"exec_id"`
}

// workerProcessStopHandler stops one live per-execution task worker by exec ID.
// A close is idempotent (it reclaims the registry entry and joins the child), so
// a stop that races the worker's own deferred close is safe. It is a local
// control action; it never touches the server.
func (d *Daemon) workerProcessStopHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if d.workerProcessRegistry == nil {
			http.Error(w, "no worker processes", http.StatusNotFound)
			return
		}
		var req workerProcessStopRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.ExecID == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var target *activeWorker
		for _, entry := range d.workerProcessRegistry.snapshot() {
			if entry.execID == req.ExecID {
				target = entry
				break
			}
		}
		if target == nil {
			http.NotFound(w, r)
			return
		}
		go target.worker.close()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"stopping": true, "exec_id": req.ExecID})
	}
}
