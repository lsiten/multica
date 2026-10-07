// Package applicationhost supervises one application through a private local control channel.
package applicationhost

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Entrypoint launches the same installed executable without loading an agent account.
const Entrypoint = "internal-application-host"

// Record is machine-local ownership evidence. Token must never leave the host.
type Record struct {
	HostID      string                             `json:"host_id"`
	BootID      string                             `json:"boot_id"`
	Token       string                             `json:"token"`
	Address     string                             `json:"address"`
	WorkDir     string                             `json:"work_dir"`
	SourceRoot  string                             `json:"source_root"`
	LogRotation uint64                             `json:"log_rotation"`
	Command     protocol.ApplicationControlCommand `json:"command"`
	Observation protocol.ApplicationObservation    `json:"observation"`
}

// NewRecord creates independent random identities for a local host and its control credential.
func NewRecord(command protocol.ApplicationControlCommand, workDir string) (Record, error) {
	var random [48]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Record{}, err
	}
	record := Record{HostID: hex.EncodeToString(random[:16]), Token: hex.EncodeToString(random[16:]), WorkDir: workDir, SourceRoot: workDir, Command: command, Observation: protocol.ApplicationObservation{InstanceID: command.InstanceID, Generation: command.Generation, Revision: command.Revision, ProcessState: "preparing", HealthState: "checking", Metrics: map[string]float64{}}}
	bootID, err := CurrentBootID()
	if err != nil && command.Config.Restart.Restore {
		return Record{}, err
	}
	record.BootID = bootID
	return record, record.Validate()
}

// Validate allows only a configured service and a local, authenticated control address.
func (r Record) Validate() error {
	if r.BootID != "" {
		if _, err := util.ParseUUID(r.BootID); err != nil {
			return errors.New("invalid application host boot identity")
		}
	}
	if len(r.HostID) != 32 || len(r.Token) != 64 {
		return errors.New("invalid application host identity")
	}
	if _, err := hex.DecodeString(r.HostID + r.Token); err != nil {
		return errors.New("invalid application host credential")
	}
	for _, id := range []string{r.Command.InstanceID, r.Command.ApplicationID, r.Command.WorkspaceID, r.Command.RuntimeID} {
		if _, err := util.ParseUUID(id); err != nil {
			return err
		}
	}
	if r.Command.Generation < 1 || r.Command.Revision < 1 || r.Command.Config.Mode != "managed" || !filepath.IsAbs(r.WorkDir) {
		return errors.New("invalid application host configuration")
	}
	if !filepath.IsAbs(r.SourceRoot) {
		return errors.New("application source root must be absolute")
	}
	relative, err := filepath.Rel(r.SourceRoot, r.WorkDir)
	if err != nil || !filepath.IsLocal(relative) {
		return errors.New("application working directory escaped its source root")
	}
	if err := r.Command.Config.Validate("service"); err != nil {
		return err
	}
	if r.Address != "" {
		address, err := url.Parse(r.Address)
		if err != nil || address.Scheme != "http" || address.Hostname() != "127.0.0.1" || address.User != nil || address.Path != "" || address.RawQuery != "" || address.Fragment != "" {
			return errors.New("application host address must be a loopback origin")
		}
		if _, _, err = net.SplitHostPort(address.Host); err != nil {
			return err
		}
	}
	return nil
}

// ReadRecord refuses symlinks, broadly-readable files and record replacement during opening.
func ReadRecord(path string) (Record, error) {
	var record Record
	before, err := os.Lstat(path)
	if err != nil {
		return record, err
	}
	if !before.Mode().IsRegular() {
		return record, errors.New("application host record must be a private regular file")
	}
	stream, err := os.Open(path)
	if err != nil {
		return record, err
	}
	defer stream.Close()
	after, err := stream.Stat()
	if err != nil || !os.SameFile(before, after) {
		return record, errors.New("application host record changed while opening")
	}
	if err := validatePrivateFile(stream); err != nil {
		return record, err
	}
	raw, err := io.ReadAll(io.LimitReader(stream, (256<<10)+1))
	if err != nil {
		return record, err
	}
	if len(raw) > 256<<10 {
		return record, errors.New("application host record exceeds 256 KiB")
	}
	if err = json.Unmarshal(raw, &record); err != nil {
		return record, fmt.Errorf("decode application host record: %w", err)
	}
	return record, record.Validate()
}

// WriteRecord atomically publishes private local state after syncing it to disk.
func WriteRecord(path string, record Record) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return errors.New("application host directory is not owned storage")
	}
	if previous, err := os.Lstat(path); err == nil {
		if !previous.Mode().IsRegular() {
			return errors.New("application host record is not a private regular file")
		}
		stream, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		privateErr := validatePrivateFile(stream)
		stream.Close()
		if privateErr != nil {
			return privateErr
		}
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".host-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = protectPrivateFile(file); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
