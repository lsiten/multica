//go:build darwin && cgo

package daemon

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/resource.h>
#include <sys/sysctl.h>
#include <string.h>
#include <unistd.h>

// read_one_process fills cpu_ns (user+system, ns) and rss (bytes) for a single
// process owned by getuid(). It returns 1 when the process is unreadable or not
// owned by getuid(), so the caller skips it and cannot enumerate its children.
static int read_one_process(int pid, unsigned long long *cpu_ns, unsigned long long *rss) {
	struct proc_bsdinfo b = {0};
	if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &b, sizeof(b)) != sizeof(b) || b.pbi_uid != getuid()) {
		return 1;
	}
	struct rusage_info_v4 usage = {0};
	if (proc_pid_rusage(pid, RUSAGE_INFO_V4, (rusage_info_t *)&usage) != 0) {
		return 1;
	}
	*cpu_ns = usage.ri_user_time + usage.ri_system_time;
	*rss = usage.ri_resident_size;
	return 0;
}

// proc_resource samples a single process's own reading. It is the single-pid
// path and keeps the original argument order (rss, then cpu_ns).
static int proc_resource(int pid, unsigned long long *rss, unsigned long long *cpu_ns) {
	return read_one_process(pid, cpu_ns, rss);
}

// proc_tree_resource sums cpu_ns and rss across the whole descendant tree of
// root_pid, counting only processes owned by getuid(). It walks breadth-first
// via sysctl KERN_PROC_PID, and stops descending into any process it cannot
// read (a different uid, or one that vanished). A process tree has no shared
// descendants, so no per-pid dedup is needed. Returns 0 when the root itself is
// readable, so a vanished root reads as unavailable.
static int proc_tree_resource(int root_pid, unsigned long long *cpu_ns, unsigned long long *rss, int *count) {
	*cpu_ns = 0;
	*rss = 0;
	*count = 0;
	int queue[2048];
	int head = 0;
	int tail = 0;
	queue[tail++] = root_pid;
	while (head < tail) {
		int pid = queue[head++];
		unsigned long long mycpu = 0;
		unsigned long long myrss = 0;
		if (read_one_process(pid, &mycpu, &myrss) != 0) {
			// Unreadable or not ours: we cannot enumerate its children either.
			continue;
		}
		*cpu_ns += mycpu;
		*rss += myrss;
		*count += 1;
		struct kinfo_proc kids[128];
		size_t ksize = sizeof(kids);
		int mib[4] = {CTL_KERN, KERN_PROC, KERN_PROC_PID, pid};
		if (sysctl(mib, 4, kids, &ksize, NULL, 0) == 0) {
			int n = (int)(ksize / sizeof(struct kinfo_proc));
			for (int i = 0; i < n && tail < 2048; i++) {
				queue[tail++] = kids[i].kp_proc.p_pid;
			}
		}
	}
	return 0;
}

// is_multica_app reports whether pid's executable path contains "Multica", used
// to tell the desktop app (whose tree spans the whole Multica footprint) from a
// dev/`make` launch whose parent is a shell or editor. proc_pidinfo(PROC_PIDPATHINFO)
// returns 0 on BOTH success and error on this platform, so the return value is not a
// success signal: a readable pid fills the buffer, an unreadable one (nonexistent or
// not ours) leaves it empty, so the check reads the buffer content, not the return.
static int is_multica_app(int pid) {
	char path[1024] = {0};
	proc_pidinfo(pid, PROC_PIDPATHINFO, 0, path, sizeof(path));
	return path[0] != '\0' && strstr(path, "Multica") != NULL;
}
*/
import "C"

// sampleProcessResources samples a single process's own reading.
func sampleProcessResources(pid int) (processResourceReading, bool) {
	var rss, cpuNS C.ulonglong
	if pid <= 0 || C.proc_resource(C.int(pid), &rss, &cpuNS) != 0 {
		return processResourceReading{}, false
	}
	return processResourceReading{
		memoryBytes: uint64(rss),
		cpuTimeMS:   uint64(cpuNS) / 1_000_000,
	}, true
}

// sampleProcessTreeResources aggregates the whole descendant tree of rootPID.
func sampleProcessTreeResources(rootPID int) (processResourceReading, int, bool) {
	var cpuNS, rss C.ulonglong
	var count C.int
	if rootPID <= 0 || C.proc_tree_resource(C.int(rootPID), &cpuNS, &rss, &count) != 0 {
		return processResourceReading{}, 0, false
	}
	return processResourceReading{
		memoryBytes: uint64(rss),
		cpuTimeMS:   uint64(cpuNS) / 1_000_000,
	}, int(count), true
}

// isMulticaAppProcess reports whether pid's executable path contains "Multica".
func isMulticaAppProcess(pid int) bool {
	if pid <= 0 {
		return false
	}
	return C.is_multica_app(C.int(pid)) != 0
}
