package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/multica-ai/multica/server/internal/communications"
	"strings"
)

// The server stores identity and task grants; SMTP executes on the selected
// Agent's daemon using that Agent's private configuration.
func hostIdentityEmailInvoker(task Task, receiptPath string) identityEmailInvoker {
	return func(ctx context.Context, taskID, recipient, subject, body string) error {
		if taskID != task.ID || task.Agent == nil || task.Agent.Identity == nil || task.Agent.Identity.Email == "" {
			return errors.New("agent identity email is not configured")
		}
		cfg := communications.SMTPConfigFromEnvValues(task.Agent.CustomEnv)
		if !strings.EqualFold(cfg.From, task.Agent.Identity.Email) {
			return errors.New("host SMTP sender must match the agent identity email")
		}
		sender, err := communications.NewSMTPSender(cfg)
		if err != nil {
			return err
		}
		store, err := communications.NewFileIdempotencyStore(receiptPath)
		if err != nil {
			return err
		}
		data, _ := json.Marshal([]string{task.ID, cfg.From, recipient, subject, body})
		digest := sha256.Sum256(data)
		key := hex.EncodeToString(digest[:])
		reserved, err := store.Reserve(key)
		if err != nil {
			return err
		}
		if !reserved {
			return nil
		}
		if err := sender.Send(ctx, communications.Mail{Recipient: recipient, Subject: subject, Body: body}); err != nil {
			if !errors.Is(err, communications.ErrMailDeliveryUnknown) {
				return errors.Join(err, store.Forget(key))
			}
			return err
		}
		return store.Complete(key, communications.Call{SID: "email-accepted", Status: "accepted"})
	}
}
