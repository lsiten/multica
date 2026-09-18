//go:build darwin && cgo

package daemon

/*
#include <mach/mach_time.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/resource.h>
#include <unistd.h>
#include <stdlib.h>
#include <stdio.h>
static unsigned long long perf_clock_ns(void) {
 mach_timebase_info_data_t info; if(mach_timebase_info(&info)!=KERN_SUCCESS || !info.denom)return 0;
 return (unsigned long long)((__uint128_t)mach_continuous_time()*info.numer/info.denom);
}
static int perf_process(int pid, unsigned long long *start, unsigned long long *rss, unsigned long long *cpu, int *fds) {
 struct proc_bsdinfo b={0};if(proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&b,sizeof(b))!=sizeof(b)||b.pbi_uid!=getuid())return 1;
 *start=b.pbi_start_tvsec*1000000ull+b.pbi_start_tvusec;
 struct rusage_info_v4 usage={0};if(proc_pid_rusage(pid,RUSAGE_INFO_V4,(rusage_info_t *)&usage)!=0)return 1;
 mach_timebase_info_data_t timebase;if(mach_timebase_info(&timebase)!=KERN_SUCCESS || !timebase.denom)return 1;
 *rss=usage.ri_resident_size;*cpu=(unsigned long long)(((__uint128_t)usage.ri_user_time+usage.ri_system_time)*timebase.numer/timebase.denom);
 int bytes=proc_pidinfo(pid,PROC_PIDLISTFDS,0,NULL,0);*fds=-1;
 if(bytes>0 && bytes<8192*sizeof(struct proc_fdinfo)) {
  int capacity=bytes+32*sizeof(struct proc_fdinfo);void *buffer=malloc(capacity);
  if(buffer){int got=proc_pidinfo(pid,PROC_PIDLISTFDS,0,buffer,capacity);if(got>=0 && got<capacity)*fds=got/sizeof(struct proc_fdinfo);free(buffer);}
 }
 struct proc_bsdinfo after={0};if(proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&after,sizeof(after))!=sizeof(after) || after.pbi_uid!=b.pbi_uid || after.pbi_start_tvsec!=b.pbi_start_tvsec || after.pbi_start_tvusec!=b.pbi_start_tvusec)return 1;
 return 0;
}
*/
import "C"

func performanceClockNS() uint64 { return uint64(C.perf_clock_ns()) }
func performanceProcess(pid int) (performanceProcessReading, error) {
	var start, rss, cpu C.ulonglong
	var fds C.int
	if pid <= 0 || C.perf_process(C.int(pid), &start, &rss, &cpu, &fds) != 0 {
		return performanceProcessReading{}, errPerformanceMetricUnavailable
	}
	return performanceProcessReading{Start: uint64(start), RSS: uint64(rss), CPU: uint64(cpu), FD: int(fds)}, nil
}
