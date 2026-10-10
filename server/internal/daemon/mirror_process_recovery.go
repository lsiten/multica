package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"io"
	"os"
	"path/filepath"
)

type mirrorShutdownProof struct {
	Identity     runtimeproc.Identity `json:"identity"`
	NativeClosed bool                 `json:"native_closed"`
	ActorsClosed bool                 `json:"actors_closed"`
}

func (s *mirrorProcessService) writeShutdownProof() error {
	proof := mirrorShutdownProof{Identity: s.bootstrap.Identity, NativeClosed: true, ActorsClosed: true}
	raw, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	path := filepath.Join(s.bootstrap.Root, "shutdown-"+s.bootstrap.Identity.InstanceID+".json")
	if _, err = os.Lstat(path); err == nil {
		return errors.New("mirror shutdown proof already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(s.bootstrap.Root, ".mirror-stop-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = publishMirrorProof(file.Name(), path); err != nil {
		return err
	}
	return syncMirrorProofDirectory(s.bootstrap.Root)
}
func reconcileStoppedMirror(ctx context.Context, root string, scope runtimeproc.Scope) error {
	record, err := runtimeproc.InspectRecord(root, scope)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	pending := false
	for _, receipt := range record.Operations {
		if receipt.State != "completed" {
			pending = true
		}
	}
	if record.State != "stopped" {
		return errors.New("mirror previous owner is live or unconfirmed")
	}
	if !pending {
		return nil
	}
	return runtimeproc.Reconcile(ctx, root, record.Identity, func(context.Context, runtimeproc.Record) (runtimeproc.Reconciliation, error) {
		lock, err := lockMirrorProcessDomain(filepath.Join(root, "authority.lock"))
		if err != nil {
			return runtimeproc.Reconciliation{}, err
		}
		fail := func(err error) (runtimeproc.Reconciliation, error) {
			lock.Close()
			return runtimeproc.Reconciliation{}, err
		}
		path := filepath.Join(root, "shutdown-"+record.Identity.InstanceID+".json")
		before, err := os.Lstat(path)
		if err != nil || !before.Mode().IsRegular() {
			return fail(errors.New("mirror shutdown proof unavailable"))
		}
		f, err := os.Open(path)
		if err != nil {
			return fail(err)
		}
		after, statErr := f.Stat()
		raw, readErr := io.ReadAll(io.LimitReader(f, 8193))
		f.Close()
		if statErr != nil || readErr != nil || !os.SameFile(before, after) || len(raw) > 8192 {
			return fail(errors.New("invalid mirror shutdown proof"))
		}
		var proof mirrorShutdownProof
		if json.Unmarshal(raw, &proof) != nil || proof.Identity != record.Identity || !proof.NativeClosed || !proof.ActorsClosed {
			return fail(errors.New("mirror cleanup remains unconfirmed"))
		}
		return runtimeproc.Reconciliation{Inventory: raw, Release: lock.Close}, nil
	})
}
