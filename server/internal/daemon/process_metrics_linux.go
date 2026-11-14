//go:build linux

package daemon

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const linuxClockTicksPerSecond = 100

type linuxProcSample struct {
	pid  int
	ppid int
	cpu  uint64
	rss  uint64
}

func linuxProcUID(pid int) (int, bool) {
	file, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		uid, err := strconv.Atoi(fields[1])
		return uid, err == nil
	}
	return 0, false
}

func readLinuxProcSample(pid int) (linuxProcSample, bool) {
	if pid <= 0 {
		return linuxProcSample{}, false
	}
	uid, ok := linuxProcUID(pid)
	if !ok || uid != os.Getuid() {
		return linuxProcSample{}, false
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return linuxProcSample{}, false
	}
	closeParen := strings.LastIndexByte(string(raw), ')')
	if closeParen < 0 || closeParen+2 >= len(raw) {
		return linuxProcSample{}, false
	}
	fields := strings.Fields(string(raw[closeParen+2:]))
	if len(fields) <= 21 {
		return linuxProcSample{}, false
	}
	ppid, err1 := strconv.Atoi(fields[1])
	utime, err2 := strconv.ParseUint(fields[11], 10, 64)
	stime, err3 := strconv.ParseUint(fields[12], 10, 64)
	rssPages, err4 := strconv.ParseInt(fields[21], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || rssPages < 0 {
		return linuxProcSample{}, false
	}
	return linuxProcSample{
		pid:  pid,
		ppid: ppid,
		cpu:  (utime + stime) * 1000 / linuxClockTicksPerSecond,
		rss:  uint64(rssPages) * uint64(os.Getpagesize()),
	}, true
}

func sampleProcessResources(pid int) (processResourceReading, bool) {
	proc, ok := readLinuxProcSample(pid)
	if !ok {
		return processResourceReading{}, false
	}
	return processResourceReading{memoryBytes: proc.rss, cpuTimeMS: proc.cpu}, true
}

func sampleProcessTreeResources(rootPID int) (processResourceReading, int, bool) {
	if rootPID <= 0 {
		return processResourceReading{}, 0, false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return processResourceReading{}, 0, false
	}
	byPID := make(map[int]linuxProcSample, len(entries))
	children := make(map[int][]int)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		proc, ok := readLinuxProcSample(pid)
		if !ok {
			continue
		}
		byPID[pid] = proc
		children[proc.ppid] = append(children[proc.ppid], pid)
	}
	if _, ok := byPID[rootPID]; !ok {
		return processResourceReading{}, 0, false
	}
	queue := []int{rootPID}
	seen := map[int]struct{}{rootPID: {}}
	var total processResourceReading
	count := 0
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		proc, ok := byPID[pid]
		if !ok {
			continue
		}
		total.memoryBytes += proc.rss
		total.cpuTimeMS += proc.cpu
		count++
		for _, child := range children[pid] {
			if _, exists := seen[child]; exists {
				continue
			}
			seen[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	return total, count, true
}

func isMulticaAppProcess(pid int) bool {
	target, err := filepath.EvalSymlinks(fmt.Sprintf("/proc/%d/exe", pid))
	return err == nil && strings.Contains(target, "Multica")
}
