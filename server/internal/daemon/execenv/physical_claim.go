package execenv

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const physicalReservationFile = ".physical-reservation.json"
const physicalCurrentFile = ".physical-current.json"

// PhysicalRootReservation bridges service preparation and worker lock ownership.
// It is not permission to reset a root. The service keeps its original FileInfo
// and verifies the worker-claimed directory before retiring the reservation.
type PhysicalRootReservation struct {
	ID          string `json:"id"`
	RootDir     string `json:"root_dir"`
	WorkspaceID string `json:"workspace_id"`
	TaskID      string `json:"task_id"`
}

// ReservePhysicalRoot publishes a durable pending reservation while the service
// still owns the root's kernel claim. The caller releases that claim afterwards.
func (env *Environment) ReservePhysicalRoot(workspaceID, taskID string) (PhysicalRootReservation, os.FileInfo, error) {
	if env == nil || env.lockFile == nil {
		return PhysicalRootReservation{}, nil, errors.New("physical root is not claimed")
	}
	owner, err := ReadEnvRootOwner(env.RootDir)
	if err != nil {
		return PhysicalRootReservation{}, nil, err
	}
	if owner.WorkspaceID != workspaceID || owner.TaskID != taskID {
		return PhysicalRootReservation{}, nil, errors.New("physical root owner mismatch")
	}
	info, err := os.Stat(env.RootDir)
	if err != nil {
		return PhysicalRootReservation{}, nil, err
	}
	reservation := PhysicalRootReservation{ID: rand.Text(), RootDir: env.RootDir, WorkspaceID: workspaceID, TaskID: taskID}
	if _, err := os.Lstat(filepath.Join(env.RootDir, physicalReservationFile)); err == nil {
		return PhysicalRootReservation{}, nil, errors.New("physical root already reserved")
	} else if !errors.Is(err, os.ErrNotExist) {
		return PhysicalRootReservation{}, nil, err
	}
	data, err := json.Marshal(reservation)
	if err != nil {
		return PhysicalRootReservation{}, nil, err
	}
	if err = writeFileAtomic(filepath.Join(env.RootDir, physicalCurrentFile), data, 0600); err != nil {
		return PhysicalRootReservation{}, nil, err
	}
	if err = writeFileAtomic(filepath.Join(env.RootDir, physicalReservationFile), data, 0600); err != nil {
		return PhysicalRootReservation{}, nil, err
	}
	return reservation, info, nil
}

// PhysicalRootReserved treats malformed or unreadable reservations as protected.
func PhysicalRootReserved(path string) (bool, error) {
	_, err := os.Lstat(filepath.Join(path, physicalReservationFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return true, err
}

// ClaimPhysicalRoot takes only the existing root lock. It never resets content,
// changes task ownership or creates an environment selected by an untrusted path.
func ClaimPhysicalRoot(workspacesRoot string, reservation PhysicalRootReservation) (*EnvRootClaim, error) {
	if reservation.ID == "" || reservation.WorkspaceID == "" || reservation.TaskID == "" {
		return nil, errors.New("invalid physical root reservation")
	}
	canonicalRoot, err := filepath.EvalSymlinks(workspacesRoot)
	if err != nil {
		return nil, err
	}
	canonicalReserved, err := filepath.EvalSymlinks(reservation.RootDir)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(canonicalRoot, canonicalReserved)
	if err != nil || !filepath.IsLocal(relative) {
		return nil, errors.New("physical root is outside workspace storage")
	}
	root, err := os.OpenRoot(workspacesRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	claim, lockedInfo, err := LockEnvRootForReuse(root, relative, reservation.RootDir)
	if err != nil {
		return nil, err
	}
	if claim == nil {
		return nil, errors.New("reserved physical root disappeared")
	}
	success := false
	defer func() {
		if !success {
			claim.Release()
		}
	}()
	child, err := root.OpenRoot(relative)
	if err != nil {
		return nil, err
	}
	defer child.Close()
	childInfo, err := child.Stat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(lockedInfo, childInfo) {
		return nil, errors.New("physical root changed while claiming")
	}
	data, err := child.ReadFile(physicalReservationFile)
	if err != nil {
		return nil, err
	}
	var current PhysicalRootReservation
	if err = json.Unmarshal(data, &current); err != nil {
		return nil, err
	}
	if current != reservation {
		return nil, errors.New("physical root reservation changed")
	}
	data, err = child.ReadFile(envRootOwnerFile)
	if err != nil {
		return nil, err
	}
	var owner EnvRootOwner
	if err = json.Unmarshal(data, &owner); err != nil {
		return nil, err
	}
	if owner.WorkspaceID != reservation.WorkspaceID || owner.TaskID != reservation.TaskID {
		return nil, errors.New("physical root owner changed")
	}
	success = true
	return claim, nil
}

// ConfirmPhysicalRoot verifies the original directory and a still-held worker
// lock before retiring a pending reservation. Missing service identity is unknown,
// never permission to clean up or reset the root.
func ConfirmPhysicalRoot(reservation PhysicalRootReservation, original os.FileInfo) error {
	return verifyPhysicalRootClaim(reservation, original, true)
}

// VerifyPhysicalRootClaim checks the current worker claim without retiring the
// reservation while the physical owner still selects or publishes its checkout.
func VerifyPhysicalRootClaim(reservation PhysicalRootReservation, original os.FileInfo) error {
	return verifyPhysicalRootClaim(reservation, original, false)
}

func verifyPhysicalRootClaim(reservation PhysicalRootReservation, original os.FileInfo, retire bool) error {
	if original == nil {
		return errors.New("physical root identity unavailable")
	}
	root, err := os.OpenRoot(reservation.RootDir)
	if err != nil {
		return err
	}
	defer root.Close()
	current, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(original, current) {
		return errors.New("physical root identity changed")
	}
	data, err := root.ReadFile(physicalReservationFile)
	if err != nil {
		return err
	}
	var pending PhysicalRootReservation
	if err = json.Unmarshal(data, &pending); err != nil {
		return err
	}
	if pending != reservation {
		return errors.New("physical root reservation changed")
	}
	file, err := root.OpenFile(envRootLockFile, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	locked, err := lockFileExclusiveNonBlocking(file)
	if err != nil {
		file.Close()
		return err
	}
	if locked {
		releaseLockFile(file)
		return errors.New("physical root has no worker claim")
	}
	file.Close()
	if retire {
		return root.Remove(physicalReservationFile)
	}
	return nil
}

// VerifyPhysicalGeneration fences late physical callbacks after a later claim
// reuses the same task/root inode. The current marker survives reservation ACK.
func VerifyPhysicalGeneration(reservation PhysicalRootReservation, original os.FileInfo) error {
	if original == nil {
		return errors.New("physical root identity unavailable")
	}
	root, err := os.OpenRoot(reservation.RootDir)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(original, info) {
		return errors.New("physical root identity changed")
	}
	data, err := root.ReadFile(physicalCurrentFile)
	if err != nil {
		return err
	}
	var current PhysicalRootReservation
	if err = json.Unmarshal(data, &current); err != nil {
		return err
	}
	if current != reservation {
		return errors.New("physical root generation changed")
	}
	return nil
}
