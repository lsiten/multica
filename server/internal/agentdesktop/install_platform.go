package agentdesktop

import "runtime"

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "agent-desktop.exe"
	}
	return "agent-desktop"
}

var (
	runtimeGOOS   = func() string { return runtime.GOOS }
	runtimeGOARCH = func() string { return runtime.GOARCH }
)
