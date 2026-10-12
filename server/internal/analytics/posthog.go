package analytics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	defaultQueueSize    = 1024
	defaultBatchSize    = 64
	defaultFlushEvery   = 10 * time.Second
	defaultFlushTimeout = 5 * time.Second
)

// PostHogConfig configures the live PostHog client.
type PostHogConfig struct {
	APIKey      string
	Host        string
	Environment string

	// Optional overrides. Zero values fall back to sensible defaults.
	QueueSize  int
	BatchSize  int
	FlushEvery time.Duration
	HTTPClient *http.Client
	QueueDir   string
	Now        func() time.Time
}

// PostHogClient ships events to PostHog's /batch/ endpoint. It enqueues events
// into a bounded buffer (non-blocking Capture) and flushes them from a
// background worker.
type PostHogClient struct {
	cfg          PostHogConfig
	ch           chan Event
	done         chan struct{}
	wg           sync.WaitGroup
	uploadCancel context.CancelFunc
	uploadDone   chan struct{}
	wake         chan struct{}
	closed       atomic.Bool
	closeOnce    sync.Once
	captureMu    sync.Mutex
	outboxMu     sync.Mutex

	dropped atomic.Uint64 // events dropped because the queue was full
	sent    atomic.Uint64
	failed  atomic.Uint64
}

// NewPostHogClient starts the background flush worker. Caller must call Close
// on shutdown to drain pending events.
func NewPostHogClient(cfg PostHogConfig) *PostHogClient {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = defaultFlushEvery
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultFlushTimeout}
	}
	if cfg.Environment == "" {
		cfg.Environment = EnvironmentFromEnv()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.QueueDir == "" {
		cfg.QueueDir = filepath.Join("data", "analytics-outbox")
	}
	scope := sha256.Sum256([]byte(strings.TrimRight(cfg.Host, "/") + "\x00" + cfg.APIKey))
	cfg.QueueDir = filepath.Join(cfg.QueueDir, hex.EncodeToString(scope[:16]))
	uploadCtx, uploadCancel := context.WithCancel(context.Background())
	c := &PostHogClient{
		cfg:          cfg,
		ch:           make(chan Event, cfg.QueueSize),
		done:         make(chan struct{}),
		wake:         make(chan struct{}, 1),
		uploadCancel: uploadCancel,
		uploadDone:   make(chan struct{}),
	}
	c.wg.Add(1)
	go c.run()
	go func() { defer close(c.uploadDone); c.uploadLoop(uploadCtx) }()
	return c
}

// Capture enqueues an event. Returns immediately; on a full queue the event
// is dropped and counted. Analytics must never block a request handler.
func (c *PostHogClient) Capture(e Event) {
	c.captureMu.Lock()
	defer c.captureMu.Unlock()
	if c.closed.Load() {
		c.dropped.Add(1)
		return
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	select {
	case c.ch <- e:
	default:
		n := c.dropped.Add(1)
		// Log periodically — every 100 drops — so a broken pipe is visible but
		// doesn't spam logs under sustained load.
		if n%100 == 1 {
			slog.Warn("analytics: queue full, dropping event", "event", e.Name, "total_dropped", n)
		}
	}
}

// Close stops accepting events and drains whatever is already queued.
func (c *PostHogClient) Close() {
	c.closeOnce.Do(func() {
		c.captureMu.Lock()
		c.closed.Store(true)
		close(c.done)
		c.captureMu.Unlock()
		c.wg.Wait()
		c.uploadCancel()
		<-c.uploadDone
	})
	slog.Info("analytics: posthog client closed",
		"sent", c.sent.Load(),
		"dropped", c.dropped.Load(),
		"failed", c.failed.Load(),
	)
}

func (c *PostHogClient) run() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.cfg.FlushEvery)
	defer ticker.Stop()

	batch := make([]Event, 0, c.cfg.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := c.persistBatch(batch); err != nil {
			c.failed.Add(uint64(len(batch)))
			slog.Warn("analytics: local outbox unavailable; retaining buffered batch", "events", len(batch))
			return
		}
		batch = batch[:0]
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}

	for {
		select {
		case e := <-c.ch:
			if len(batch) >= c.cfg.QueueSize+c.cfg.BatchSize {
				c.dropped.Add(1)
				continue
			}
			batch = append(batch, e)
			if len(batch) >= c.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-c.done:
			// Drain remaining events. The channel is not closed by Close() to
			// avoid racing with Capture, so we loop until it's empty.
			for {
				select {
				case e := <-c.ch:
					batch = append(batch, e)
					if len(batch) >= c.cfg.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// capturePayload mirrors the PostHog /batch/ JSON shape.
type capturePayload struct {
	APIKey string        `json:"api_key"`
	Batch  []captureItem `json:"batch"`
}

type captureItem struct {
	UUID       string         `json:"uuid"`
	Event      string         `json:"event"`
	DistinctID string         `json:"distinct_id"`
	Properties map[string]any `json:"properties"`
	Timestamp  string         `json:"timestamp"`
}

func (c *PostHogClient) batchItems(batch []Event) []captureItem {
	items := make([]captureItem, 0, len(batch))
	for _, e := range batch {
		props := make(map[string]any, len(e.Properties)+2)
		for k, v := range e.Properties {
			props[k] = v
		}
		if e.WorkspaceID != "" {
			props["workspace_id"] = e.WorkspaceID
		}
		props["event_schema_version"] = EventSchemaVersion
		props["environment"] = c.cfg.Environment
		if _, ok := props["is_demo"]; !ok {
			props["is_demo"] = false
		}
		if _, ok := props["user_id"]; !ok && e.DistinctID != "" && !strings.Contains(e.DistinctID, ":") {
			props["user_id"] = e.DistinctID
		}
		if len(e.SetOnce) > 0 {
			props["$set_once"] = e.SetOnce
		}
		if len(e.Set) > 0 {
			props["$set"] = e.Set
		}
		items = append(items, captureItem{
			UUID:       e.ID,
			Event:      e.Name,
			DistinctID: e.DistinctID,
			Properties: props,
			Timestamp:  e.Timestamp.UTC().Format(time.RFC3339Nano),
		})
	}
	return items
}

func (c *PostHogClient) sendItems(parent context.Context, items []captureItem) error {
	body, err := json.Marshal(capturePayload{APIKey: c.cfg.APIKey, Batch: items})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(parent, defaultFlushTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Host+"/batch/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &batchDeliveryError{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After")}
	}
	c.sent.Add(uint64(len(items)))
	return nil
}
