package applicationgateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

const revocationChannel = "multica:applications:revocations"

type revocation struct {
	Mode string `json:"mode"`
	ID   string `json:"id"`
}

// Start subscribes this replica to revocations under the owning server's lifecycle.
func (h *Hub) Start(ctx context.Context) error {
	if h.cluster == nil {
		return nil
	}
	h.cluster.startOnce.Do(func() {
		subscription := h.cluster.redis.Subscribe(ctx, revocationChannel)
		if _, err := subscription.Receive(ctx); err != nil {
			subscription.Close()
			h.cluster.startErr = err
			close(h.cluster.done)
			return
		}
		go func() {
			defer close(h.cluster.done)
			defer subscription.Close()
			messages := subscription.Channel()
			for {
				select {
				case <-ctx.Done():
					return
				case message, ok := <-messages:
					if !ok {
						return
					}
					var request revocation
					if json.Unmarshal([]byte(message.Payload), &request) != nil || request.ID == "" {
						continue
					}
					switch request.Mode {
					case "endpoint":
						h.revokeStreams(request.ID, "")
					case "runtime":
						h.revokeStreams("", request.ID)
					}
				}
			}
		}()
	})
	return h.cluster.startErr
}

// Wait joins the cluster observer after its lifecycle context has been cancelled.
func (h *Hub) Wait(ctx context.Context) error {
	if h.cluster == nil {
		return nil
	}
	select {
	case <-h.cluster.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hub) publishRevocation(mode, id string) {
	if h.cluster == nil {
		return
	}
	raw, err := json.Marshal(revocation{Mode: mode, ID: id})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.cluster.redis.Publish(ctx, revocationChannel, raw).Err(); err != nil {
		slog.Error("application gateway revocation relay unavailable", "mode", mode, "resource_id", id)
	}
}
