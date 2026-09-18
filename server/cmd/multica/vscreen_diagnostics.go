package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
)

type vscreenDiagnostics struct {
	Version         string `json:"version"`
	Commit          string `json:"commit"`
	BuiltAt         string `json:"built_at"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	NativeSupported bool   `json:"native_supported"`
	Permissions     struct {
		Accessibility   bool `json:"accessibility"`
		ScreenRecording bool `json:"screen_recording"`
	} `json:"permissions"`
	Error string `json:"error,omitempty"`
}

func runVscreenDiagnostics(out io.Writer) error {
	return writeVscreenDiagnostics(out, native.Supported(), appcontrol.ProbePermissions)
}

// This entry point bypasses profile and daemon startup. The probe never requests TCC access.
func writeVscreenDiagnostics(out io.Writer, supported bool, probe func(context.Context) (appcontrol.Permissions, error)) error {
	result := vscreenDiagnostics{Version: version, Commit: commit, BuiltAt: date, OS: runtime.GOOS, Arch: runtime.GOARCH, NativeSupported: supported}
	if !supported {
		result.Error = "native_unsupported"
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		permissions, err := probe(ctx)
		result.Permissions.Accessibility = permissions.Accessibility
		result.Permissions.ScreenRecording = permissions.ScreenRecording
		if err != nil {
			result.Error = "permission_probe_failed"
		} else if !permissions.Accessibility || !permissions.ScreenRecording {
			result.Error = "permissions_missing"
		}
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		return err
	}
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}
