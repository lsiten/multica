package runtimeproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Reconciliation retains domain ownership until both durable transport writes finish.
type Reconciliation struct {
	Inventory json.RawMessage
	Release   func() error
}

// Reconcile explicitly retires an exited owner only while the caller proves its
// domain inventory safe under domain locks. It preserves all original receipts,
// including uncertain operations, in a private immutable archive. It never replays
// an operation. The callback must retain its domain locks until this call returns.
func Reconcile(ctx context.Context, root string, expected Identity, inspect func(context.Context, Record) (Reconciliation, error)) error {
	return reconcile(ctx, root, expected, inspect, writeRecord)
}
func reconcile(ctx context.Context, root string, expected Identity, inspect func(context.Context, Record) (Reconciliation, error), write func(string, Record) error) (resultErr error) {
	scope := expected.Scope
	if err := expected.Validate(); err != nil {
		return err
	}
	if inspect == nil {
		return errors.New("domain reconciliation is required")
	}
	if err := validateStorage(root, scope); err != nil {
		return err
	}
	dir := scopeDirectory(root, scope)
	launch, err := lockFile(filepath.Join(dir, "launch.lock"))
	if err != nil {
		return errors.New("runtime launch is live or unknown")
	}
	defer launch.Close()
	owner, err := lockFile(filepath.Join(dir, "owner.lock"))
	if err != nil {
		return errors.New("runtime owner is live or unknown")
	}
	defer owner.Close()
	raw, err := readPrivate(RecordPath(root, scope))
	if err != nil {
		return err
	}
	var previous Record
	if json.Unmarshal(raw, &previous) != nil || previous.Identity != expected {
		return errors.New("invalid reconciliation record")
	}
	if _, err = ReadRecord(root, previous.Identity); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	proof, err := inspect(ctx, previous)
	if err != nil {
		return err
	}
	if proof.Release == nil {
		return errors.New("reconciliation must retain a domain ownership handle")
	}
	defer func() { resultErr = errors.Join(resultErr, proof.Release()) }()
	if err = ctx.Err(); err != nil {
		return err
	}
	inventory := proof.Inventory
	if len(inventory) > maxBody || !json.Valid(inventory) {
		return errors.New("invalid reconciliation inventory")
	}
	archive := filepath.Join(dir, "reconciled-"+previous.Identity.InstanceID+".json")
	if archivedRaw, archiveErr := readPrivate(archive); archiveErr == nil {
		var archived Record
		if json.Unmarshal(archivedRaw, &archived) != nil {
			return errors.New("invalid reconciliation archive")
		}
		archived.Reconciliation = nil
		comparison := previous
		comparison.Reconciliation = nil
		archivedJSON, _ := json.Marshal(archived)
		currentJSON, _ := json.Marshal(comparison)
		alreadyRetired := previous.State == "stopped" && len(previous.Operations) == 0 && len(previous.Reconciliation) > 0 && archived.Identity == previous.Identity && archived.Token == previous.Token
		if !bytes.Equal(archivedJSON, currentJSON) && !alreadyRetired {
			return errors.New("reconciliation archive belongs to different state")
		}
	} else if !errors.Is(archiveErr, os.ErrNotExist) {
		return archiveErr
	} else {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return readErr
		}
		count := 0
		var size int64
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "reconciled-") && strings.HasSuffix(entry.Name(), ".json") {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return errors.New("invalid reconciliation archive entry")
				}
				count++
				size += info.Size()
			}
		}
		if count >= 64 || size+int64(len(raw)) > 64<<20 {
			return errors.New("reconciliation archive quota reached")
		}
		historical := previous
		historical.Reconciliation = inventory
		if err = write(archive, historical); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	previous.Reconciliation = inventory
	previous.State = "stopped"
	previous.Operations = map[string]Receipt{}
	return write(RecordPath(root, scope), previous)
}

// ReadReconciledRecord retrieves historical uncertain receipts for an exact instance.
func ReadReconciledRecord(root string, identity Identity) (Record, error) {
	var record Record
	if err := identity.Validate(); err != nil {
		return record, err
	}
	if err := validateStorage(root, identity.Scope); err != nil {
		return record, err
	}
	raw, err := readPrivate(filepath.Join(scopeDirectory(root, identity.Scope), "reconciled-"+identity.InstanceID+".json"))
	if err != nil {
		return record, err
	}
	if json.Unmarshal(raw, &record) != nil || record.Identity != identity {
		return Record{}, errors.New("invalid reconciliation archive")
	}
	return record, nil
}

// InspectRecord reads a protected owner identity without authorizing adoption or replacement.
func InspectRecord(root string, scope Scope) (Record, error) {
	var record Record
	if err := validateStorage(root, scope); err != nil {
		return record, err
	}
	raw, err := readPrivate(RecordPath(root, scope))
	if err != nil {
		return record, err
	}
	if json.Unmarshal(raw, &record) != nil || record.Identity.Scope != scope {
		return record, errors.New("invalid runtime scope")
	}
	return ReadRecord(root, record.Identity)
}
