//go:build darwin && cgo

package daemon

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/resource.h>
#include <unistd.h>

static int proc_resource(int pid, unsigned long long *rss, unsigned long long *cpu_ns) {
	struct proc_bsdinfo b = {0};
	if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &b, sizeof(b)) != sizeof(b) || b.pbi_uid != getuid()) {
		return 1;
	}
	struct rusage_info_v4 usage = {0};
	if (proc_pid_rusage(pid, RUSAGE_INFO_V4, (rusage_info_t *)&usage) != 0) {
		return 1;
	}
	*rss = usage.ri_resident_size;
	*cpu_ns = usage.ri_user_time + usage.ri_system_time;
	return 0;
}
*/
import "C"

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
