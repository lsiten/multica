package daemon

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
)

func TestApplicationProcessRecoveryAfterMachineReboot(t *testing.T) {
	for _, test := range []struct {
		name     string
		bootID   string
		state    string
		holdLock bool
		wantErr  bool
	}{
		{name: "prior boot running host", bootID: "00000000-0000-4000-8000-000000000000", state: "running"},
		{name: "prior boot stopped host", bootID: "00000000-0000-4000-8000-000000000000", state: "stopped"},
		{name: "same boot unreachable host", state: "running", wantErr: true},
		{name: "unknown boot unreachable host", bootID: "unknown", state: "running", wantErr: true},
		{name: "host lock still owned", bootID: "00000000-0000-4000-8000-000000000000", state: "running", holdLock: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, child, backend := newApplicationProcessFixture(t)
			waitApplicationManager(t, func() bool {
				backend.mu.Lock()
				defer backend.mu.Unlock()
				return len(backend.results) > 0
			})
			backend.mu.Lock()
			command := backend.command
			backend.mu.Unlock()
			if err := child.close(); err != nil {
				t.Fatal(err)
			}
			path, record, err := d.applicationRecord(command)
			if err != nil {
				t.Fatal(err)
			}
			switch test.bootID {
			case "":
			case "unknown":
				record.BootID = ""
			default:
				record.BootID = test.bootID
			}
			record.Observation.ProcessState = test.state
			if err := applicationhost.WriteRecord(path, record); err != nil {
				t.Fatal(err)
			}
			if test.holdLock {
				lock, err := lockMirrorProcessDomain(filepath.Join(filepath.Dir(path), "host.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			}
			d.applicationProcessMu.Lock()
			d.applicationProcess = nil
			d.applicationProcessMu.Unlock()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			replacement, err := d.ensureApplicationProcess(ctx)
			if replacement != nil {
				defer func() {
					if err := replacement.close(); err != nil {
						t.Error(err)
					}
				}()
			}
			if (err != nil) != test.wantErr {
				t.Fatalf("application owner restart error = %v, want error = %v", err, test.wantErr)
			}
			after, err := applicationhost.ReadRecord(path)
			if err != nil {
				t.Fatal(err)
			}
			if after.HostID != record.HostID || after.BootID != record.BootID || !reflect.DeepEqual(after.Command, record.Command) {
				t.Fatal("recovery changed retained host identity or application configuration")
			}
			if test.wantErr {
				if !reflect.DeepEqual(after, record) {
					t.Fatal("unproved ownership changed the retained host record")
				}
				return
			}
			if after.Observation.ProcessState != "stopped" || after.Address != "" {
				t.Fatalf("prior-boot host was not reconciled: state=%s address=%s", after.Observation.ProcessState, after.Address)
			}
			if _, err := os.Stat(record.WorkDir); err != nil {
				t.Fatalf("recovery removed application files: %v", err)
			}
		})
	}
}
