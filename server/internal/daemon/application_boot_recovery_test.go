package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
)

func TestApplicationBootRecoveryReplacesOnlyAProvedEarlierBoot(t *testing.T) {
	for _, test := range []struct {
		name, boot string
		restore    bool
	}{{"same boot", "current", false}, {"unknown boot", "", false}, {"previous boot", "00000000-0000-4000-8000-000000000000", true}} {
		t.Run(test.name, func(t *testing.T) {
			d, command := applicationManagerFixture(t, "http://unused.test")
			command.Config.Restart.Restore = true
			record, err := applicationhost.NewRecord(command, filepath.Dir(command.Config.Environment["APPLICATION_START_FILE"]))
			if err != nil {
				t.Fatal(err)
			}
			if test.boot != "current" {
				record.BootID = test.boot
			}
			record.Address = "http://127.0.0.1:1"
			record.Observation.ProcessState = "running"
			directory, err := d.applicationDirectory(command)
			if err != nil {
				t.Fatal(err)
			}
			if err := applicationhost.WriteRecord(filepath.Join(directory, "host.json"), record); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if test.restore {
				observed, err := d.inspectApplication(ctx, command)
				if err == nil || observed.ProcessState != "stopped" {
					t.Fatalf("reboot evidence did not distinguish exit from lost contact: %+v %v", observed, err)
				}
			}
			observation, err := d.executeApplication(ctx, command)
			if !test.restore {
				if err == nil {
					t.Fatal("unreachable host was replaced without proof of reboot")
				}
				if _, err := os.Stat(command.Config.Environment["APPLICATION_START_FILE"]); !os.IsNotExist(err) {
					t.Fatal("unverified recovery launched a duplicate process")
				}
				return
			}
			t.Cleanup(func() {
				stop := command
				stop.Action = "stop"
				stop.Generation = 2
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := d.executeApplication(ctx, stop); err != nil {
					t.Error(err)
				}
			})
			if err != nil || observation.ProcessState != "running" {
				t.Fatalf("proved reboot recovery=%+v error=%v", observation, err)
			}
			_, replacement, err := d.applicationRecord(command)
			if err != nil || replacement.HostID == record.HostID || replacement.BootID == record.BootID {
				t.Fatalf("recovery retained stale ownership: %+v %v", replacement, err)
			}
		})
	}
}

func TestApplicationStopAfterRebootConfirmsExitWithoutContactingReusedAddress(t *testing.T) {
	d, command := applicationManagerFixture(t, "http://unused.test")
	record, err := applicationhost.NewRecord(command, filepath.Dir(command.Config.Environment["APPLICATION_START_FILE"]))
	if err != nil {
		t.Fatal(err)
	}
	record.BootID = "00000000-0000-4000-8000-000000000000"
	record.Address = "http://127.0.0.1:1"
	record.Observation.ProcessState = "running"
	directory, err := d.applicationDirectory(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := applicationhost.WriteRecord(filepath.Join(directory, "host.json"), record); err != nil {
		t.Fatal(err)
	}
	command.Action = "stop"
	command.Generation = 2
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	observation, err := d.executeApplication(ctx, command)
	if err != nil || observation.ProcessState != "stopped" || observation.Generation != 2 {
		t.Fatalf("rebooted process was treated as live: %+v %v", observation, err)
	}
}
