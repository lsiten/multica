package notificationbot

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Worker recovers from the durable inbox, so a process crash between insertion
// and event publication cannot lose notifications. Delivery is at-least-once:
// a provider acceptance followed by a lost response can result in a duplicate.
type Worker struct {
	Queries *db.Queries
	Box     *secretbox.Box
	Sender  Sender
	AppURL  string
	done    chan struct{}
}

func NewWorker(queries *db.Queries, box *secretbox.Box, appURL string) *Worker {
	return &Worker{Queries: queries, Box: box, AppURL: strings.TrimRight(appURL, "/"), done: make(chan struct{})}
}

func (w *Worker) Run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("notification bot worker failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) Wait(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.done:
		return true
	case <-timer.C:
		return false
	}
}

// Tick is bounded; leases prevent two replicas from claiming the same job.
func (w *Worker) Tick(ctx context.Context) error {
	if err := w.Queries.PruneNotificationBotDeliveries(ctx); err != nil {
		return err
	}
	if err := w.Queries.EnqueueNotificationBotDeliveries(ctx); err != nil {
		return err
	}
	for range 20 {
		delivery, err := w.Queries.ClaimNotificationBotDelivery(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := w.deliver(ctx, delivery); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) deliver(ctx context.Context, delivery db.NotificationBotDelivery) error {
	target, err := w.Queries.GetNotificationBotDeliveryTarget(ctx, db.GetNotificationBotDeliveryTargetParams{BotID: delivery.BotID, InboxID: delivery.InboxID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	finished := errors.Is(err, pgx.ErrNoRows)
	if !finished {
		var preferences map[string]string
		if err := json.Unmarshal(target.Preferences, &preferences); err != nil {
			return err
		}
		finished = muted(preferences, target.InboxType)
	}
	if !finished {
		config, configErr := w.Open(target.Platform, target.Credentials)
		sendErr := configErr
		if configErr == nil {
			message := "Multica\n" + target.Title
			if target.Body.Valid && target.Body.String != "" {
				message += "\n" + target.Body.String
			}
			link := ""
			if w.AppURL != "" {
				link = w.AppURL + "/" + url.PathEscape(target.Slug) + "/inbox\n"
				if target.IssueID.Valid {
					link = strings.TrimSuffix(link, "\n") + "?issue=" + util.UUIDToString(target.IssueID) + "\n"
				}
			}
			// Keep the navigation link first so long notification bodies cannot
			// push it past the provider's text limit.
			sendErr = w.Sender.Send(ctx, config, link+message)
		}
		lastError := ""
		if sendErr != nil {
			lastError = sendErr.Error()
		}
		if err := w.Queries.RecordNotificationBotResult(ctx, db.RecordNotificationBotResultParams{ID: delivery.BotID, LastError: lastError}); err != nil {
			return err
		}
		finished = sendErr == nil || delivery.Attempts >= 6
	}
	return w.Queries.FinishNotificationBotDelivery(ctx, db.FinishNotificationBotDeliveryParams{
		ID: delivery.ID, Attempts: delivery.Attempts, Finished: finished, RetrySeconds: 60 * delivery.Attempts,
	})
}

// Open keeps encryption and malformed persisted credentials behind one safe
// error boundary; ciphertext or decrypted values never escape in errors.
func (w *Worker) Open(platform string, ciphertext []byte) (Config, error) {
	raw, err := w.Box.Open(ciphertext)
	if err != nil {
		return Config{}, ErrConfig
	}
	var credentials Credentials
	if json.Unmarshal(raw, &credentials) != nil {
		return Config{}, ErrConfig
	}
	return ParseConfig(platform, credentials)
}

func muted(preferences map[string]string, kind string) bool {
	group := ""
	switch kind {
	case "issue_assigned", "unassigned", "assignee_changed":
		group = "assignments"
	case "status_changed":
		group = "status_changes"
	case "new_comment":
		group = "comments"
	case "mentioned":
		group = "mentions"
	case "priority_changed", "start_date_changed", "due_date_changed":
		group = "updates"
	case "task_completed", "task_failed", "agent_blocked", "agent_completed":
		group = "agent_activity"
	default:
		return false
	}
	return preferences[group] == "muted"
}
