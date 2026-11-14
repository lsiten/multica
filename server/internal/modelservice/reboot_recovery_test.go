package modelservice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/internal/util"
)

func TestModelOwnerRecoversDescendantsAfterMachineReboot(t *testing.T) {
	for _, test := range []struct {
		name    string
		bootID  string
		legacy  bool
		oldFile bool
		wantErr bool
	}{
		{name: "prior boot", bootID: "00000000-0000-4000-8000-000000000000"},
		{name: "legacy prior boot", legacy: true, oldFile: true},
		{name: "legacy current boot", legacy: true, wantErr: true},
		{name: "same boot", wantErr: true},
		{name: "invalid boot identity", bootID: "invalid", oldFile: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, b := fixture(t, false)
			_, err := client.Acquire(t.Context(), jevmodels.Selection{ModelID: jevmodels.ModelID, Revision: jevmodels.Revision, Device: "cpu"}, execution())
			if !errors.Is(err, jevmodels.ErrNotInstalled) {
				t.Fatal(err)
			}
			if err := client.process.Close(); err != nil {
				t.Fatal(err)
			}
			client.closeOnce.Do(func() {})
			path := filepath.Join(b.Root, "model-service.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var persisted map[string]json.RawMessage
			if err := json.Unmarshal(raw, &persisted); err != nil {
				t.Fatal(err)
			}
			job := InstallJob{ID: "interrupted-install", ModelID: jevmodels.ModelID, Revision: jevmodels.Revision, State: "downloading"}
			persisted["jobs"], err = json.Marshal(map[string]InstallJob{job.ID: job})
			if err != nil {
				t.Fatal(err)
			}
			if test.legacy {
				delete(persisted, "boot_id")
			} else if test.bootID != "" {
				persisted["boot_id"], err = json.Marshal(test.bootID)
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, err = json.Marshal(persisted)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if test.oldFile {
				old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
				if err := os.Chtimes(path, old, old); err != nil {
					t.Fatal(err)
				}
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			replacement, err := Launch(context.Background(), b.Root, b.Identity.Scope, executable, "fixture")
			if replacement != nil {
				defer replacement.Close()
			}
			if (err != nil) != test.wantErr {
				t.Fatalf("model owner reboot recovery error=%v, want error=%v", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			inv, err := readInventory(b.Root)
			if err != nil || inv.DescendantsPossible || inv.InstanceID == b.Identity.InstanceID {
				t.Fatalf("replacement inventory=%+v error=%v", inv, err)
			}
			currentBoot, err := util.CurrentBootID()
			if err != nil || inv.BootID != currentBoot {
				t.Fatalf("replacement boot identity=%q, current=%q, error=%v", inv.BootID, currentBoot, err)
			}
			recoveredJob, ok := inv.Jobs[job.ID]
			if !ok || recoveredJob.ID != job.ID || recoveredJob.ModelID != job.ModelID || recoveredJob.Revision != job.Revision || recoveredJob.State != "interrupted" {
				t.Fatalf("install recovery lost the original job: %+v", recoveredJob)
			}
			archived, err := runtimeproc.ReadReconciledRecord(b.Root, b.Identity)
			if err != nil || len(archived.Reconciliation) == 0 {
				t.Fatalf("previous owner inventory was lost: %v", err)
			}
		})
	}
}
