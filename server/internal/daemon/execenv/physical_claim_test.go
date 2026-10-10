package execenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPhysicalRootHandoffNeverResetsWorkerContent(t *testing.T) {
	root := t.TempDir()
	env, err := preparePhysical(PhysicalPrepareParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "task"}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer env.ReleaseLock()
	protected := filepath.Join(env.WorkDir, "user.txt")
	if err = os.WriteFile(protected, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	reservation, identity, err := env.ReservePhysicalRoot("workspace", "task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ClaimPhysicalRoot(root, reservation); err == nil {
		t.Fatal("worker acquired service-held kernel lock")
	}
	env.ReleaseLock()
	if reset, err := ClaimEnvRoot(RootDirParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "task"}); err == nil {
		reset.Release()
		t.Fatal("legacy claim reset a pending handoff")
	}
	if err = ConfirmPhysicalRoot(reservation, identity); err == nil {
		t.Fatal("service accepted absent worker claim")
	}
	changed := reservation
	changed.ID = "stale"
	if _, err = ClaimPhysicalRoot(root, changed); err == nil {
		t.Fatal("stale reservation accepted")
	}
	claim, err := ClaimPhysicalRoot(root, reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if err = ConfirmPhysicalRoot(reservation, nil); err == nil {
		t.Fatal("unknown physical identity accepted")
	}
	if err = ConfirmPhysicalRoot(reservation, identity); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(protected); err != nil || string(data) != "keep" {
		t.Fatalf("handoff reset content: %q %v", data, err)
	}
	if reserved, err := PhysicalRootReserved(env.RootDir); err != nil || reserved {
		t.Fatalf("confirmed reservation remained: %v %v", reserved, err)
	}
}

func TestPhysicalRootConfirmationRejectsSameNameReplacement(t *testing.T) {
	root := t.TempDir()
	env, err := PreparePhysical(PhysicalPrepareParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "replacement-task"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer env.ReleaseLock()
	reservation, identity, err := env.ReservePhysicalRoot("workspace", "replacement-task")
	if err != nil {
		t.Fatal(err)
	}
	env.ReleaseLock()
	old := env.RootDir + "-old"
	if err = os.Rename(env.RootDir, old); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(env.RootDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{envRootOwnerFile, physicalReservationFile} {
		data, err := os.ReadFile(filepath.Join(old, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(env.RootDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	claim, err := ClaimPhysicalRoot(root, reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if err = ConfirmPhysicalRoot(reservation, identity); err == nil {
		t.Fatal("copied reservation conferred original directory identity")
	}
	if reserved, err := PhysicalRootReserved(env.RootDir); err != nil || !reserved {
		t.Fatalf("replacement reservation removed: %v %v", reserved, err)
	}
}
