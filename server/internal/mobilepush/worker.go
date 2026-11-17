package mobilepush

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/inbox"
	"github.com/multica-ai/multica/server/internal/projectaccess"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type Sender interface {
	Send(context.Context, Notification) error
}

type Worker struct {
	queries *db.Queries
	senders map[string]Sender
	topics  map[string]bool
	done    chan struct{}
}

type Configuration struct {
	Enabled      bool     `json:"enabled"`
	Topics       []string `json:"topics"`
	Environments []string `json:"environments"`
}

func NewWorker(queries *db.Queries, senders map[string]Sender, topics []string) *Worker {
	worker := &Worker{queries: queries, senders: make(map[string]Sender), topics: make(map[string]bool), done: make(chan struct{})}
	for environment, sender := range senders {
		if sender != nil && (environment == "production" || environment == "sandbox") {
			worker.senders[environment] = sender
		}
	}
	for _, topic := range topics {
		if topic = strings.TrimSpace(topic); topic != "" {
			worker.topics[topic] = true
		}
	}
	return worker
}

func (w *Worker) Configuration() Configuration {
	config := Configuration{Topics: []string{}, Environments: []string{}}
	if w == nil {
		return config
	}
	for topic := range w.topics {
		config.Topics = append(config.Topics, topic)
	}
	for environment := range w.senders {
		config.Environments = append(config.Environments, environment)
	}
	sort.Strings(config.Topics)
	sort.Strings(config.Environments)
	config.Enabled = len(config.Topics) > 0 && len(config.Environments) > 0
	return config
}

func (w *Worker) Allows(topic, environment string) bool {
	return w != nil && w.topics[topic] && w.senders[environment] != nil
}

func (w *Worker) Run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("mobile push worker failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) Wait(ctx context.Context) error {
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) Tick(ctx context.Context) error {
	if err := w.queries.PruneMobilePushDeliveries(ctx); err != nil {
		return err
	}
	if err := w.queries.EnqueueMobilePushDeliveries(ctx); err != nil {
		return err
	}
	if err := w.queries.EnqueueMobileChatPushDeliveries(ctx); err != nil {
		return err
	}
	for range 20 {
		delivery, err := w.queries.ClaimMobilePushDelivery(ctx)
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

func (w *Worker) target(ctx context.Context, delivery db.MobilePushDelivery) (db.GetMobilePushDeliveryTargetRow, error) {
	if delivery.ChatMessageID.Valid {
		target, err := w.queries.GetMobileChatPushDeliveryTarget(ctx, db.GetMobileChatPushDeliveryTargetParams{DeviceID: delivery.DeviceID, ChatMessageID: delivery.ChatMessageID})
		return db.GetMobilePushDeliveryTargetRow{
			ID: target.ID, UserID: target.UserID, Token: target.Token, Topic: target.Topic,
			Environment: target.Environment, ActiveSince: target.ActiveSince, CreatedAt: target.CreatedAt, UpdatedAt: target.UpdatedAt,
			InboxItemID: target.InboxItemID, Title: target.Title, Body: pgtype.Text{String: target.Body, Valid: true},
			InboxType: target.InboxType, IssueID: target.IssueID, Details: target.Details,
			WorkspaceID: target.WorkspaceID, Slug: target.Slug, Preferences: target.Preferences,
			IssueProjectID: target.IssueProjectID, Role: target.Role, ChatSessionID: target.ChatSessionID,
			ChatMessageID: target.ChatMessageID, TaskID: target.TaskID,
		}, err
	}
	return w.queries.GetMobilePushDeliveryTarget(ctx, db.GetMobilePushDeliveryTargetParams{DeviceID: delivery.DeviceID, InboxID: delivery.InboxID})
}

func (w *Worker) deliver(ctx context.Context, delivery db.MobilePushDelivery) error {
	target, err := w.target(ctx, delivery)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	finished := errors.Is(err, pgx.ErrNoRows)
	if !finished {
		var preferences map[string]string
		if err := json.Unmarshal(target.Preferences, &preferences); err != nil {
			return err
		}
		finished = !w.Allows(target.Topic, target.Environment) || preferences["system_notifications"] == "muted" || inbox.NotificationMuted(preferences, target.InboxType)
		if target.ChatMessageID.Valid {
			origin, err := w.queries.GetTaskChannelOrigin(ctx, target.TaskID)
			if err != nil {
				return err
			}
			finished = finished || origin.ChannelIngested || preferences["chat_replies"] == "muted"
		}
		if !finished && target.IssueProjectID.Valid {
			hidden, err := projectaccess.ForMember(ctx, w.queries, target.WorkspaceID, target.UserID, target.Role)
			if err != nil {
				return err
			}
			finished = !hidden.CanSeeProject(target.IssueProjectID)
		}
	}
	if !finished {
		preview := []rune(strings.Join(strings.Fields(target.Body.String), " "))
		body := string(preview[:min(len(preview), 200)])
		title := []rune(target.Title)
		notification := Notification{
			DeviceToken: target.Token, Topic: target.Topic, Environment: target.Environment,
			ID: util.UUIDToString(delivery.ID), InboxID: util.UUIDToString(target.InboxItemID),
			WorkspaceSlug: target.Slug, RecipientID: util.UUIDToString(target.UserID),
			IssueID: util.UUIDToString(target.IssueID), ChatSessionID: util.UUIDToString(target.ChatSessionID), MessageID: util.UUIDToString(target.ChatMessageID),
			Type: target.InboxType, Title: string(title[:min(len(title), 200)]), Body: body,
		}
		err := w.senders[target.Environment].Send(ctx, notification)
		finished = err == nil || errors.Is(err, ErrNotification)
		var failure *ResponseError
		if errors.As(err, &failure) {
			finished = !failure.Retryable()
			if failure.InvalidDevice() {
				if err := w.queries.InvalidateMobilePushDevice(ctx, db.InvalidateMobilePushDeviceParams{ID: target.ID, UpdatedAt: target.UpdatedAt}); err != nil {
					return err
				}
			}
		}
		if err != nil {
			slog.Warn("mobile push delivery failed", "delivery_id", util.UUIDToString(delivery.ID), "attempt", delivery.Attempts, "error", err)
		}
	}
	return w.queries.FinishMobilePushDelivery(ctx, db.FinishMobilePushDeliveryParams{
		ID: delivery.ID, Attempts: delivery.Attempts, Finished: finished || delivery.Attempts >= 6, RetrySeconds: 60 * delivery.Attempts,
	})
}
