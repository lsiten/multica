//go:build windows

package daemon

import (
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	psapi                = syscall.NewLazyDLL("psapi.dll")
	getProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

// windowsProcessMemoryCounters is the stable PROCESS_MEMORY_COUNTERS layout
// used by psapi.GetProcessMemoryInfo. uintptr keeps the structure correct for
// both 32-bit and 64-bit builds.
type windowsProcessMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

type windowsProcessSample struct {
	pid  uint32
	ppid uint32
	cpu  uint64
	rss  uint64
}

func enumerateWindowsProcesses() (map[uint32][]uint32, bool) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, false
	}
	defer windows.CloseHandle(snapshot)

	children := make(map[uint32][]uint32)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, false
	}
	for {
		if entry.ProcessID != 0 && entry.ProcessID != entry.ParentProcessID {
			children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if err != windows.ERROR_NO_MORE_FILES {
				return nil, false
			}
			break
		}
	}
	return children, true
}

func readWindowsProcessSample(pid uint32) (windowsProcessSample, bool) {
	if pid == 0 {
		return windowsProcessSample{}, false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return windowsProcessSample{}, false
	}
	defer windows.CloseHandle(handle)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return windowsProcessSample{}, false
	}
	var memory windowsProcessMemoryCounters
	memory.cb = uint32(unsafe.Sizeof(memory))
	result, _, _ := getProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&memory)),
		uintptr(memory.cb),
	)
	if result == 0 {
		return windowsProcessSample{}, false
	}
	return windowsProcessSample{
		pid: pid,
		cpu: windowsFiletimeMilliseconds(kernel) + windowsFiletimeMilliseconds(user),
		rss: uint64(memory.workingSetSize),
	}, true
}

func windowsFiletimeMilliseconds(value windows.Filetime) uint64 {
	units := uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
	return units / 10_000
}

func sampleProcessResources(pid int) (processResourceReading, bool) {
	sample, ok := readWindowsProcessSample(uint32(pid))
	if !ok {
		return processResourceReading{}, false
	}
	return processResourceReading{memoryBytes: sample.rss, cpuTimeMS: sample.cpu}, true
}

func sampleProcessTreeResources(rootPID int) (processResourceReading, int, bool) {
	if rootPID <= 0 {
		return processResourceReading{}, 0, false
	}
	children, ok := enumerateWindowsProcesses()
	if !ok {
		return processResourceReading{}, 0, false
	}
	queue := []uint32{uint32(rootPID)}
	seen := map[uint32]struct{}{uint32(rootPID): {}}
	var total processResourceReading
	count := 0
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		sample, readable := readWindowsProcessSample(pid)
		if !readable {
			continue
		}
		total.memoryBytes += sample.rss
		total.cpuTimeMS += sample.cpu
		count++
		for _, child := range children[pid] {
			if _, exists := seen[child]; exists {
				continue
			}
			seen[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	if count == 0 {
		return processResourceReading{}, 0, false
	}
	return total, count, true
}

func isMulticaAppProcess(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, windows.MAX_PATH)
	length := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &length); err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(windows.UTF16ToString(buffer[:length])), "multica")
}
