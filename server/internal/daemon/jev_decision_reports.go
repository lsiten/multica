package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type pendingJevDecision struct {
	TaskID string                  `json:"task_id"`
	Record protocol.JevDecisionLog `json:"record"`
}

func (d *Daemon) jevDecisionReportDir() string {
	identity := strings.TrimRight(d.cfg.ServerBaseURL, "/") + "\x00" + d.cfg.Profile + "\x00" + d.cfg.DaemonID
	sum := sha256.Sum256([]byte(identity))
	return filepath.Join(d.cfg.WorkspacesRoot, ".pending-jev-decisions", "v1", hex.EncodeToString(sum[:16]))
}

func (d *Daemon) enqueueJevDecision(taskID string, record protocol.JevDecisionLog) error {
	if d.cfg.WorkspacesRoot == "" {
		return errors.New("Jev decision report directory is not configured")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	dir := d.jevDecisionReportDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("Jev decision report directory is not private")
	}
	raw, err := json.Marshal(pendingJevDecision{TaskID: taskID, Record: record})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".decision-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
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
	phase := "running"
	if record.CompletedAt != nil {
		phase = "completed"
	}
	return os.Rename(file.Name(), filepath.Join(dir, record.ID+"."+phase+".json"))
}

func (d *Daemon) flushJevDecisionReports(ctx context.Context) error {
	if d.cfg.WorkspacesRoot == "" {
		return nil
	}
	dir := d.jevDecisionReportDir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var report pendingJevDecision
		if err := strictVscreenJSON(raw, &report); err != nil || report.TaskID == "" || report.Record.Validate() != nil {
			return errors.New("invalid queued Jev decision report")
		}
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = d.client.postJSON(requestCtx, fmt.Sprintf("/api/daemon/tasks/%s/jev-decision-logs", report.TaskID), report.Record, nil)
		cancel()
		if err != nil {
			var response *requestError
			if errors.As(err, &response) && (response.StatusCode == 400 || response.StatusCode == 403 || response.StatusCode == 404 || response.StatusCode == 409) {
				failed := filepath.Join(dir, "failed")
				if err := os.MkdirAll(failed, 0700); err != nil {
					return err
				}
				if err := os.Rename(path, filepath.Join(failed, entry.Name())); err != nil {
					return err
				}
				d.logger.Error("Jev decision report rejected; retained locally", "decision_id", report.Record.ID, "task_id", report.TaskID, "http_status", response.StatusCode)
				continue
			}
			return errors.New("Jev decision report upload deferred")
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (d *Daemon) jevDecisionReportLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := d.flushJevDecisionReports(ctx); err != nil && ctx.Err() == nil {
			d.logger.Warn("Jev decision reports remain pending", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
