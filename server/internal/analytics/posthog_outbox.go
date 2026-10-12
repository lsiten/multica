package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const analyticsOutboxLimit = 64 << 20

type queuedAnalyticsBatch struct {
	Version     int           `json:"version"`
	ID          string        `json:"id"`
	CreatedAt   time.Time     `json:"created_at"`
	Items       []captureItem `json:"items"`
	Attempts    int           `json:"attempts,omitempty"`
	NextAttempt time.Time     `json:"next_attempt,omitempty"`
}

type batchDeliveryError struct {
	status     int
	retryAfter string
}

func (e *batchDeliveryError) Error() string { return fmt.Sprintf("analytics batch HTTP %d", e.status) }

func analyticsRetryDelay(attempt int, err error) time.Duration {
	delay := min(time.Second*time.Duration(1<<min(max(attempt, 1), 8)), 5*time.Minute)
	var response *batchDeliveryError
	if errors.As(err, &response) {
		if seconds, parseErr := strconv.ParseInt(response.retryAfter, 10, 64); parseErr == nil && seconds > 0 {
			delay = max(delay, time.Duration(min(seconds, 86400))*time.Second)
		} else if at, parseErr := http.ParseTime(response.retryAfter); parseErr == nil && at.After(time.Now()) {
			delay = max(delay, min(time.Until(at), 24*time.Hour))
		}
	}
	return delay
}

func (c *PostHogClient) persistBatch(events []Event) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	batch := queuedAnalyticsBatch{Version: 1, ID: id.String(), CreatedAt: c.cfg.Now().UTC(), Items: c.batchItems(events)}
	return c.saveBatch(batch)
}

func (c *PostHogClient) saveBatch(batch queuedAnalyticsBatch) error {
	c.outboxMu.Lock()
	defer c.outboxMu.Unlock()
	if err := ensureAnalyticsDirectory(c.cfg.QueueDir); err != nil {
		return err
	}
	raw, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	if len(raw) > 8<<20 {
		return errors.New("analytics batch exceeds local storage limit")
	}
	entries, err := os.ReadDir(c.cfg.QueueDir)
	if err != nil {
		return err
	}
	var bytes int64
	for _, entry := range entries {
		if entry.Name() == batch.ID+".json" {
			continue
		}
		if entry.IsDir() {
			if entry.Name() == "failed" {
				failed, readErr := os.ReadDir(filepath.Join(c.cfg.QueueDir, "failed"))
				if readErr != nil {
					return readErr
				}
				for _, retained := range failed {
					if stat, err := retained.Info(); err == nil {
						bytes += stat.Size()
					}
				}
			}
			continue
		}
		if stat, err := entry.Info(); err == nil {
			bytes += stat.Size()
		}
	}
	if bytes+int64(len(raw)) > analyticsOutboxLimit {
		return errors.New("analytics outbox quota exhausted; batch remains buffered")
	}
	file, err := os.CreateTemp(c.cfg.QueueDir, ".batch-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(c.cfg.QueueDir, batch.ID+".json")); err != nil {
		return err
	}
	return syncAnalyticsDirectory(c.cfg.QueueDir)
}

func (c *PostHogClient) isolateBatch(name string) error {
	failed := filepath.Join(c.cfg.QueueDir, "failed")
	if err := ensureAnalyticsDirectory(failed); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(c.cfg.QueueDir, name), filepath.Join(failed, name)); err != nil {
		return err
	}
	return errors.Join(syncAnalyticsDirectory(c.cfg.QueueDir), syncAnalyticsDirectory(failed))
}

func (c *PostHogClient) replayBatches(ctx context.Context) (pending bool, delay time.Duration) {
	entries, err := os.ReadDir(c.cfg.QueueDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, 0
	}
	if err != nil {
		return true, 5 * time.Second
	}
	// UUIDv7 filenames preserve enqueue order while retries retain original event timestamps.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	delay = 5 * time.Minute
	for _, entry := range entries {
		if ctx.Err() != nil {
			return true, delay
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(c.cfg.QueueDir, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			pending = true
			delay = min(delay, 5*time.Second)
			continue
		}
		statErr := validateAnalyticsFile(file)
		var batch queuedAnalyticsBatch
		decodeErr := json.NewDecoder(io.LimitReader(file, (8<<20)+1)).Decode(&batch)
		file.Close()
		if statErr != nil || decodeErr != nil || batch.Version != 1 || batch.ID+".json" != entry.Name() || len(batch.Items) == 0 {
			if err := c.isolateBatch(entry.Name()); err != nil {
				return true, 5 * time.Second
			}
			c.failed.Add(1)
			slog.Warn("analytics: malformed batch retained in failed queue")
			continue
		}
		if c.cfg.Now().Before(batch.NextAttempt) {
			pending = true
			delay = min(delay, time.Until(batch.NextAttempt))
			continue
		}
		if err := c.sendItems(ctx, batch.Items); err != nil {
			if ctx.Err() != nil {
				return true, delay
			}
			c.failed.Add(uint64(len(batch.Items)))
			var response *batchDeliveryError
			if errors.As(err, &response) && response.status >= 400 && response.status < 500 && response.status != 408 && response.status != 429 {
				if err := c.isolateBatch(entry.Name()); err != nil {
					return true, 5 * time.Second
				}
				slog.Warn("analytics: rejected batch retained in failed queue", "status", response.status)
				continue
			}
			batch.Attempts++
			delay = analyticsRetryDelay(batch.Attempts, err)
			batch.NextAttempt = c.cfg.Now().Add(delay)
			if err := c.saveBatch(batch); err != nil {
				slog.Warn("analytics: could not persist retry deadline")
			}
			slog.Warn("analytics: upload deferred; original batch retained", "events", len(batch.Items))
			return true, delay
		}
		if err := os.Remove(path); err != nil {
			return true, 5 * time.Second
		}
		if err := syncAnalyticsDirectory(c.cfg.QueueDir); err != nil {
			return true, 5 * time.Second
		}
	}
	return pending, delay
}

func (c *PostHogClient) uploadLoop(ctx context.Context) {
	var owner *os.File
	defer func() {
		if owner != nil {
			owner.Close()
		}
	}()
	for owner == nil {
		var err error
		owner, err = lockAnalyticsOutbox(c.cfg.QueueDir)
		if err == nil && owner != nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	var timerCh <-chan time.Time = timer.C
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
			if timerCh != nil {
				continue
			}
			timer.Reset(0)
			timerCh = timer.C
		case <-timerCh:
			pending, delay := c.replayBatches(ctx)
			if !pending {
				timerCh = nil
				continue
			}
			timer.Reset(max(delay, time.Millisecond))
			timerCh = timer.C
		}
	}
}
