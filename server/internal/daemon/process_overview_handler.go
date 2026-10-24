package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// processOverviewEntry is one row of the unified local process overview a desktop
// panel reads from the daemon's /runtimes/processes/overview endpoint. It is a
// read-side view: it carries no credential. Resource usage (pid/memory/cpu) is a
// best-effort local reading — any field that cannot be sampled is left unset so
// the panel degrades to "—" rather than showing a wrong zero.
//
// MemoryBytes/CpuTimeMS are the single-pid reading. TreeMemoryBytes/TreeCpuTimeMS
// are the aggregate of that pid AND its whole descendant tree (children the
// process runs), so a parent row reflects the children too instead of a bare
// single-process reading that an activity monitor would show as much smaller.
type processOverviewEntry struct {
	Category     string   `json:"category"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	State        string   `json:"state"`
	Ready        bool     `json:"ready"`
	Capabilities []string `json:"capabilities,omitempty"`
	Detail       string   `json:"detail,omitempty"`
	StartedAt    string   `json:"started_at,omitempty"`
	Pid          int      `json:"pid,omitempty"`
	MemoryBytes  uint64   `json:"memory_bytes,omitempty"`
	CpuTimeMS    uint64   `json:"cpu_time_ms,omitempty"`
	// Tree aggregate: this process and its whole descendant tree. The panel
	// shows it as "含子进程" so a parent row is not misleadingly small.
	TreeMemoryBytes uint64 `json:"tree_memory_bytes,omitempty"`
	TreeCpuTimeMS   uint64 `json:"tree_cpu_time_ms,omitempty"`
	ProcessCount    int    `json:"process_count,omitempty"`
	Stoppable       bool   `json:"stoppable,omitempty"`
}

// processOverviewTotal is the deduped aggregate across the whole Multica
// footprint. It is the number the panel compares against an activity monitor.
// It is NOT a sum of the per-row tree aggregates (those overlap, so summing
// would double-count): it is a single walk of the top of the process forest.
type processOverviewTotal struct {
	Available    bool   `json:"available"`
	ProcessCount int    `json:"process_count,omitempty"`
	MemoryBytes  uint64 `json:"memory_bytes,omitempty"`
	CpuTimeMS    uint64 `json:"cpu_time_ms,omitempty"`
}

// safeProcessPID returns the OS pid of p, tolerating a torn-down process handle.
// runtimeproc.Process.PID dereferences cmd, which can be nil after a close, so
// this guards against a panic in the read-side overview handler.
func safeProcessPID(p *runtimeproc.Process) int {
	if p == nil {
		return 0
	}
	defer func() { _ = recover() }()
	return p.PID()
}

// fillTreeMetrics adds the tree aggregate (this pid + its whole descendant tree)
// to entry so a parent row reflects the children it runs, not just itself.
func fillTreeMetrics(entry *processOverviewEntry, pid int) {
	if pid <= 0 {
		return
	}
	if tree := sampleProcessTree(pid); tree.Available {
		entry.TreeMemoryBytes = tree.MemoryBytes
		entry.TreeCpuTimeMS = tree.CpuTimeMS
		entry.ProcessCount = tree.ProcessCount
	}
}

// describeRuntimeProcess fills a best-effort resource reading and an authenticated
// state probe for one child runtimeproc.Process. It never gates the listing: a
// probe failure or a vanished child leaves the row reported as "unavailable"
// with resource fields unset, exactly like the task-worker handler.
func describeRuntimeProcess(ctx context.Context, p *runtimeproc.Process, entry processOverviewEntry) processOverviewEntry {
	pid := safeProcessPID(p)
	if pid > 0 {
		if metrics := sampleProcessMetrics(pid); metrics.Available {
			entry.Pid = metrics.PID
			entry.MemoryBytes = metrics.MemoryBytes
			entry.CpuTimeMS = metrics.CPUTimeMS
		}
		fillTreeMetrics(&entry, pid)
	}
	if p == nil || p.Client == nil {
		entry.State = "unavailable"
		return entry
	}
	probeCtx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancel()
	status, err := p.Client.Health(probeCtx)
	if err != nil {
		// A probe failure means the owner is suspect, not stopped; the child is
		// still registered, so report it rather than hide it.
		entry.State = "unavailable"
		return entry
	}
	entry.State = status.State
	entry.Ready = status.State == "ready" || status.State == "running"
	entry.Capabilities = status.Capabilities
	return entry
}

// processesOverviewHandler lists every category the daemon can supervise as one
// unified, category-tagged list with best-effort resource usage. It is a local
// read: it serves the local process-status panel and never touches the server.
// In-process services (MCP / JEV) are not child processes and are served by
// their own endpoints, so they stay off this list.
//
// Every managed category is always present, so the panel never hides a
// supervised category behind a nil child. The daemon's own process is first; the
// long-lived service children (application, gateway, environment, mirror) are
// always reported, as "stopped" when they are not started on this daemon; task
// workers are the only stoppable rows and appear as they run.
//
// The response also carries a deduped "total" (processOverviewTotal): the whole
// Multica footprint, so the panel shows a number that matches an activity
// monitor instead of a bare sum of per-row readings.
func (d *Daemon) processesOverviewHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var out []processOverviewEntry
		out = append(out, d.daemonProcessEntry(r))
		out = append(out, d.taskWorkerOverviewEntries(r)...)
		out = append(out, d.singletonProcessEntry(r, "application", "application service", d.snapshotApplicationProcess, d.applicationProcessMode))
		out = append(out, d.singletonProcessEntry(r, "gateway", "gateway", d.snapshotGatewayProcess, d.gatewayProcessMode))
		out = append(out, d.singletonProcessEntry(r, "environment", "environment service", d.snapshotEnvironmentProcess, d.environmentProcessMode))
		out = append(out, d.singletonProcessEntry(r, "mirror", "mirror service", d.snapshotMirrorProcess, d.mirrorProcessMode))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"processes": out, "total": d.processOverviewTotal()})
	}
}

// daemonProcessEntry reports the daemon's own process as the first row of the
// overview. Every other row belongs to a process this daemon supervises, so the
// supervising process must be visible too, with its own resource reading. The
// daemon is not stoppable from a per-row action — it is managed by the app's
// start/stop/restart lifecycle — so stoppable stays false.
func (d *Daemon) daemonProcessEntry(r *http.Request) processOverviewEntry {
	entry := processOverviewEntry{
		Category:  "daemon",
		ID:        "daemon",
		Name:      "Multica daemon",
		Stoppable: false,
	}
	pid := os.Getpid()
	if metrics := sampleProcessMetrics(pid); metrics.Available {
		entry.Pid = metrics.PID
		entry.MemoryBytes = metrics.MemoryBytes
		entry.CpuTimeMS = metrics.CPUTimeMS
	}
	fillTreeMetrics(&entry, pid)
	entry.State = "starting"
	if d.ready.Load() {
		entry.State = "running"
		entry.Ready = true
	}
	return entry
}

// taskWorkerOverviewEntries maps the live task workers into overview rows. They
// are the only stoppable category; the id is the exec id the stop endpoint takes.
//
// The per-worker health probes run concurrently: each keeps its own timeout and
// is bounded by the request context, so a slow or hung worker can no longer
// stretch the whole overview past the panel's refetch interval (the sequential
// form was O(N x 2s)). Each goroutine writes only its own result slot, so the
// shared slice has no locking.
func (d *Daemon) taskWorkerOverviewEntries(r *http.Request) []processOverviewEntry {
	if d.workerProcessRegistry == nil {
		return nil
	}
	entries := d.workerProcessRegistry.snapshot()
	out := make([]processOverviewEntry, 0, len(entries))
	if len(entries) == 0 {
		return out
	}
	results := make([]processOverviewEntry, len(entries))
	var wg sync.WaitGroup
	for i, entry := range entries {
		wg.Add(1)
		go func(i int, entry *activeWorker) {
			defer wg.Done()
			state := d.workerProcessStateResponse(r, entry)
			outEntry := processOverviewEntry{
				Category:     "task_worker",
				ID:           state.ExecID,
				Name:         state.Provider,
				State:        state.State,
				Ready:        state.Ready,
				Capabilities: state.Capabilities,
				Detail:       state.TaskID,
				StartedAt:    state.StartedAt,
				Pid:          state.Pid,
				MemoryBytes:  state.MemoryBytes,
				CpuTimeMS:    state.CpuTimeMS,
				Stoppable:    true,
			}
			fillTreeMetrics(&outEntry, state.Pid)
			results[i] = outEntry
		}(i, entry)
	}
	wg.Wait()
	for _, e := range results {
		out = append(out, e)
	}
	return out
}

// processOverviewTotal is the deduped aggregate across the whole Multica
// footprint — the number the panel compares against an activity monitor.
//
// The desktop app is the daemon's parent: when it is, the parent's process tree
// spans the whole footprint (desktop main + renderer + GPU helpers + the daemon
// + every child the daemon runs), so one walk is the total an activity monitor
// shows. The parent's tree is always a superset of the daemon's (the daemon is
// the parent's descendant); the desktop app's tree is also a meaningfully larger
// one (the renderer alone is several times the daemon's own footprint).
//
// The parent is confirmed as the desktop app by either signal: the executable
// path contains "Multica", or the parent tree is a clearly larger footprint.
// PROC_PIDPATHINFO is unreliable on macOS (a just-started or copied executable
// can return an empty path non-deterministically), so the size signal is the
// robust backstop when a path read comes back empty. When neither holds the
// parent is a dev/`make` launch from a shell, whose tree would count unrelated
// siblings; the total then falls back to the daemon's own tree, which is never
// wrong (only smaller). The parent walk is guarded by parentPID > 1 so the total
// never walks launchd when the daemon has been adopted.
func (d *Daemon) processOverviewTotal() processOverviewTotal {
	daemonTree := sampleProcessTree(os.Getpid())
	parentPID := int(syscall.Getppid())
	if parentPID <= 1 {
		// The daemon was adopted by launchd; never walk the whole system.
		return toOverviewTotal(daemonTree)
	}
	parentTree := sampleProcessTree(parentPID)
	if parentTree.Available && (isMulticaAppProcess(parentPID) || largerFootprint(parentTree, daemonTree)) {
		return toOverviewTotal(parentTree)
	}
	if daemonTree.Available {
		return toOverviewTotal(daemonTree)
	}
	// Neither tree is a confirmed larger footprint and the daemon tree is
	// unreadable; the only reading we have is the parent tree, so use it rather
	// than reporting an unavailable total.
	if parentTree.Available {
		return toOverviewTotal(parentTree)
	}
	return processOverviewTotal{}
}

// toOverviewTotal converts a sampled tree aggregate into the panel total, or an
// unavailable total when the tree could not be sampled, so the panel degrades to
// "—" rather than a wrong zero.
func toOverviewTotal(tree ProcessTreeMetrics) processOverviewTotal {
	if !tree.Available {
		return processOverviewTotal{}
	}
	return processOverviewTotal{
		Available:    true,
		ProcessCount: tree.ProcessCount,
		MemoryBytes:  tree.MemoryBytes,
		CpuTimeMS:    tree.CpuTimeMS,
	}
}

// largerFootprint reports whether candidate is a meaningfully larger aggregate
// than baseline. The parent's tree is always a superset of the daemon's (the
// daemon is the parent's descendant), so a wider, several-times-larger memory
// tree is the desktop app's footprint rather than a dev shell that merely owns
// the daemon as one of its children.
func largerFootprint(candidate, baseline ProcessTreeMetrics) bool {
	return candidate.ProcessCount > baseline.ProcessCount && candidate.MemoryBytes >= baseline.MemoryBytes*2
}

// singletonProcessEntry reports one long-lived service the daemon can host. It
// always reports a row: a missing child means the category is a managed
// service, reported rather than omitted, so the panel shows every supervised
// category in one place.
//
// A missing child is NOT "stopped": the fail-closed design guarantees the
// in-process owner runs whenever the separate process is absent, so the
// service is functional. processModeActive distinguishes the two cases:
// true means the daemon is still in process mode (a child is being started or
// the start is retrying) so the row is "starting"; false means the separate
// process failed and the daemon fell back to the in-process owner, which is
// running. This stops the panel from showing a working service that the
// in-process fallback runs as "stopped".
func (d *Daemon) singletonProcessEntry(r *http.Request, category, name string, resolve func() *runtimeproc.Process, processModeActive func() bool) processOverviewEntry {
	entry := processOverviewEntry{
		Category: category,
		ID:       category,
		Name:     name,
	}
	process := resolve()
	if process != nil {
		return describeRuntimeProcess(r.Context(), process, entry)
	}
	// Still in process mode: a child is being started (or the start is retrying)
	// so the row is "starting" rather than "stopped".
	if processModeActive() {
		entry.State = "starting"
		return entry
	}
	// In-process mode: the fail-closed in-process owner is the active owner once
	// the daemon is up. Report it as running so the panel never shows a working
	// service as "stopped". d.ready guards the not-yet-initialised case (a bare
	// &Daemon{} with no running owner), which should still read as "stopped".
	if d.ready.Load() {
		entry.State = "running"
		entry.Ready = true
		entry.Detail = "in-process"
		return entry
	}
	entry.State = "stopped"
	return entry
}

// snapshotApplicationProcess returns the live application child process, or nil.
func (d *Daemon) snapshotApplicationProcess() *runtimeproc.Process {
	d.applicationProcessMu.Lock()
	defer d.applicationProcessMu.Unlock()
	if d.applicationProcess == nil {
		return nil
	}
	return d.applicationProcess.process
}

// snapshotGatewayProcess returns the live gateway child process, or nil.
func (d *Daemon) snapshotGatewayProcess() *runtimeproc.Process {
	d.gatewayProcessMu.Lock()
	defer d.gatewayProcessMu.Unlock()
	if d.gatewayProcess == nil {
		return nil
	}
	return d.gatewayProcess.process
}

// snapshotEnvironmentProcess returns the live environment child process, or nil.
func (d *Daemon) snapshotEnvironmentProcess() *runtimeproc.Process {
	d.environmentProcessMu.Lock()
	defer d.environmentProcessMu.Unlock()
	if d.environmentProcess == nil {
		return nil
	}
	return d.environmentProcess.process
}

// snapshotMirrorProcess returns the live mirror child process, or nil.
func (d *Daemon) snapshotMirrorProcess() *runtimeproc.Process {
	d.mirrorProcessMu.Lock()
	defer d.mirrorProcessMu.Unlock()
	if d.mirrorProcess == nil {
		return nil
	}
	return d.mirrorProcess.process
}
