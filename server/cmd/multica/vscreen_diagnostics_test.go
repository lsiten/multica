package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"testing"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
)

func TestVscreenDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name        string
		supported   bool
		permissions appcontrol.Permissions
		probeErr    error
		wantError   string
	}{
		{name: "unsupported", wantError: "native_unsupported"},
		{name: "permissions granted", supported: true, permissions: appcontrol.Permissions{Accessibility: true, ScreenRecording: true}},
		{name: "accessibility missing", supported: true, permissions: appcontrol.Permissions{ScreenRecording: true}, wantError: "permissions_missing"},
		{name: "screen recording missing", supported: true, permissions: appcontrol.Permissions{Accessibility: true}, wantError: "permissions_missing"},
		{name: "probe failed", supported: true, probeErr: errors.New("sensitive OS detail"), wantError: "permission_probe_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			called := false
			err := writeVscreenDiagnostics(&output, tc.supported, func(ctx context.Context) (appcontrol.Permissions, error) {
				called = true
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("permission probe must have a deadline")
				}
				return tc.permissions, tc.probeErr
			})
			if (err != nil) != (tc.wantError != "") || called != tc.supported {
				t.Fatalf("error=%v probe called=%v", err, called)
			}
			var result vscreenDiagnostics
			decoder := json.NewDecoder(&output)
			if err := decoder.Decode(&result); err != nil {
				t.Fatal(err)
			}
			if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
				t.Fatalf("unexpected trailing stdout: %v", err)
			}
			if result.Error != tc.wantError || result.NativeSupported != tc.supported || result.Version != version || result.Commit != commit || result.OS != runtime.GOOS || result.Arch != runtime.GOARCH {
				t.Fatalf("diagnostics=%+v", result)
			}
			if result.Permissions.Accessibility != tc.permissions.Accessibility || result.Permissions.ScreenRecording != tc.permissions.ScreenRecording {
				t.Fatalf("permissions=%+v", result.Permissions)
			}
		})
	}
}
