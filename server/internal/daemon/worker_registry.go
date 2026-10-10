package daemon

import (
	"sync"
	"time"
)

// activeWorker is the daemon's live view of one per-execution task worker (the F3
// move-out). It is registered when a worker is launched on the hot path and
// reclaimed when the worker closes, so a local caller can see which physical
// worker processes this runtime is currently driving. It carries no secret: the
// runtime channel and credential live only on the worker itself.
type activeWorker struct {
	execID     string
	instanceID string
	taskID     string
	provider   string
	worker     *workerProcessClient
	startedAt  time.Time
}

// workerProcessRegistry is the daemon's mutex-guarded live set of per-execution
// task workers. It is a read-side view for local process status: it only observes
// launch and close, never drives the hot path.
type workerProcessRegistry struct {
	mu      sync.Mutex
	entries map[string]*activeWorker // keyed by execID
}

func newWorkerProcessRegistry() *workerProcessRegistry {
	return &workerProcessRegistry{entries: make(map[string]*activeWorker)}
}

// register records a launched worker. A second registration for the same execID
// replaces the prior entry, so a relaunch never leaves a stale record.
func (r *workerProcessRegistry) register(entry *activeWorker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[entry.execID] = entry
}

// reclaim removes a worker from the live set. It is a no-op for an execID that was
// never registered, so an idempotent close can call it safely.
func (r *workerProcessRegistry) reclaim(execID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, execID)
}

// snapshot returns a copy of the current entries; callers then read each worker's
// live state without holding the registry lock.
func (r *workerProcessRegistry) snapshot() []*activeWorker {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*activeWorker, 0, len(r.entries))
	for _, entry := range r.entries {
		out = append(out, entry)
	}
	return out
}

// registerWorkerProcess records a just-launched worker in the live registry and
// arms the worker's one-time reclaim so a close (deferred or explicit) drops it.
func (d *Daemon) registerWorkerProcess(worker *workerProcessClient, execID, taskID, provider string) {
	if d.workerProcessRegistry == nil || worker == nil {
		return
	}
	d.workerProcessRegistry.register(&activeWorker{
		execID:     execID,
		instanceID: worker.instanceID,
		taskID:     taskID,
		provider:   provider,
		worker:     worker,
		startedAt:  time.Now(),
	})
	worker.reclaim = func() { d.workerProcessRegistry.reclaim(execID) }
}
