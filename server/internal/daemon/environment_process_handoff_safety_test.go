package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestEnvironmentUnclaimedHandoffRequiresCurrentUnusedOwnership(t *testing.T) {
	for _, mode := range []string{"unused", "worker_claim", "selected", "attached", "wrong_generation", "wrong_reservation", "missing_journal", "recovered_owner", "interrupted_disposition"} {
		t.Run(mode, func(t *testing.T) {
			// Given a real durable allocation and user-owned content to preserve.
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			owner, err := newEnvironmentPhysicalOwner(t.TempDir(), t.TempDir(), logger)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(owner.close)
			result, err := owner.prepare(t.Context(), execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "task", AgentName: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			note := filepath.Join(result.WorkDir, "user.txt")
			if err = os.WriteFile(note, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			preparation := owner.preparations[result.Reservation.ID]
			input := environmentHandoffRecovery{Reservation: result.Reservation}
			switch mode {
			case "worker_claim":
				claim, err := execenv.ClaimPhysicalRoot(owner.root, result.Reservation)
				if err != nil {
					t.Fatal(err)
				}
				defer claim.Release()
			case "selected":
				preparation.Selected = true
			case "attached":
				preparation.Attached = true
			case "wrong_generation":
				changed := result.Reservation
				changed.ID = "different-generation"
				if err = os.WriteFile(filepath.Join(result.Reservation.RootDir, ".physical-current.json"), marshalRaw(changed), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong_reservation":
				input.Reservation.TaskID = "other-task"
			case "missing_journal":
				if err = os.Remove(filepath.Join(owner.journal, result.Reservation.ID+".json")); err != nil {
					t.Fatal(err)
				}
			case "recovered_owner":
				replacement, err := newEnvironmentPhysicalOwner(owner.root, owner.journal, logger)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(replacement.close)
				owner = replacement
			case "interrupted_disposition":
				preparation.HandoffRecovering = true
				if err = owner.persist(preparation); err != nil {
					t.Fatal(err)
				}
				claim, err := execenv.ClaimPhysicalRoot(owner.root, result.Reservation)
				if err != nil {
					t.Fatal(err)
				}
				if err = execenv.ConfirmPhysicalRoot(result.Reservation, preparation.identity); err != nil {
					claim.Release()
					t.Fatal(err)
				}
				claim.Release()
			}
			// When recovery attempts only the reservation disposition.
			recovery, recoveryErr := owner.recoverUnclaimed(t.Context(), input)
			// Then used, superseded, or unprovable allocations remain protected.
			wantSettled := mode == "unused" || mode == "interrupted_disposition"
			if recovery.Settled != wantSettled {
				t.Fatalf("settled=%v want=%v error=%v", recovery.Settled, wantSettled, recoveryErr)
			}
			reserved, err := execenv.PhysicalRootReserved(result.Reservation.RootDir)
			if err != nil || reserved == wantSettled {
				t.Fatalf("reserved=%v error=%v", reserved, err)
			}
			if body, err := os.ReadFile(note); err != nil || string(body) != "preserve" {
				t.Fatalf("user content lost: %v", err)
			}
			if wantSettled {
				again, err := owner.recoverUnclaimed(t.Context(), input)
				if err != nil || !again.Settled {
					t.Fatalf("recovery was not idempotent: %+v %v", again, err)
				}
			}
		})
	}
}
