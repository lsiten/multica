package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/agent"
)

const (
	providerApprovalPollInterval = 750 * time.Millisecond
	providerApprovalExpiry       = 5 * time.Minute
)

var (
	errProviderApprovalDenied      = errors.New("provider action rejected by the member")
	errProviderApprovalUnavailable = errors.New("provider approval is unavailable")
)

type providerApprovalRequest struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
	Response struct {
		Revision int64  `json:"revision"`
		Decision string `json:"decision"`
	} `json:"response"`
}

// requestTaskApproval routes provider-native high-risk operations through the
// task-bound human-request API. The task token is the only authority used.
func (d *Daemon) requestTaskApproval(task Task) func(context.Context, agent.ApprovalRequest) (bool, error) {
	return func(ctx context.Context, request agent.ApprovalRequest) (bool, error) {
		if task.InitiatorType != "member" || task.InitiatorID == "" {
			return false, errors.New("daemon: no human approval initiator")
		}
		if !strings.HasPrefix(task.AuthToken, cli.TaskTokenPrefix) || d.cfg.ServerBaseURL == "" {
			return false, fmt.Errorf("%w: task identity is incomplete", errProviderApprovalUnavailable)
		}
		client := cli.NewAPIClient(d.cfg.ServerBaseURL, task.WorkspaceID, task.AuthToken)
		client.AgentID = task.AgentID
		client.TaskID = task.ID
		return waitForProviderApproval(ctx, client, task.ID, request)
	}
}

func waitForProviderApproval(
	ctx context.Context,
	client *cli.APIClient,
	taskID string,
	request agent.ApprovalRequest,
) (bool, error) {
	input := service.HumanRequestInput{
		Key:              providerApprovalKey(taskID, request),
		Kind:             "confirmation",
		Title:            providerApprovalTitle(request),
		ActionLabel:      "Approve",
		Next:             "I will perform this provider operation once if approved.",
		Impact:           "Allows one provider operation: " + request.Method + ".",
		Details:          providerApprovalDetails(request),
		ExpiresInSeconds: int64(providerApprovalExpiry / time.Second),
		ResponseMode:     "card_only",
	}
	var created providerApprovalRequest
	if err := client.PostJSON(ctx, "/api/human-requests/", input, &created); err != nil {
		return false, fmt.Errorf("%w: create request: %v", errProviderApprovalUnavailable, err)
	}
	if created.ID == "" || created.Revision < 1 {
		return false, fmt.Errorf("%w: request response did not include id and revision", errProviderApprovalUnavailable)
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("provider approval cancelled: %w", ctx.Err())
		case <-timer.C:
		}
		var current providerApprovalRequest
		if err := client.GetJSON(ctx, "/api/human-requests/"+created.ID, &current); err != nil {
			return false, fmt.Errorf("%w: read request: %v", errProviderApprovalUnavailable, err)
		}
		switch current.Status {
		case "pending":
			timer.Reset(providerApprovalPollInterval)
		case "answered":
			if current.Response.Revision != created.Revision || current.Response.Decision != "approve" {
				return false, errProviderApprovalDenied
			}
			return true, nil
		case "declined", "cancelled", "expired":
			return false, errProviderApprovalDenied
		default:
			return false, fmt.Errorf("%w: request state %q", errProviderApprovalUnavailable, current.Status)
		}
	}
}

func providerApprovalKey(taskID string, request agent.ApprovalRequest) string {
	sum := sha256.Sum256([]byte(taskID + "\x00" + request.Method + "\x00" + string(request.Params)))
	return "provider-" + hex.EncodeToString(sum[:12])
}

func providerApprovalTitle(request agent.ApprovalRequest) string {
	title := "Approve " + request.Method
	switch {
	case strings.Contains(request.Method, "command"), strings.Contains(request.Method, "exec"):
		title = "Approve command"
	case strings.Contains(request.Method, "file"), strings.Contains(request.Method, "patch"):
		title = "Approve file changes"
	case strings.Contains(request.Method, "permissions"):
		title = "Approve permissions"
	case strings.Contains(request.Method, "mcp"):
		title = "Approve MCP tool"
	}
	if len(title) > 100 {
		return title[:100]
	}
	return title
}

func providerApprovalDetails(request agent.ApprovalRequest) string {
	details := string(request.Params)
	if len(request.FileChanges) == 0 {
		return details
	}
	paths := make([]string, 0, len(request.FileChanges))
	for _, change := range request.FileChanges {
		path := change.Path
		if change.MovePath != "" {
			path += " -> " + change.MovePath
		}
		if change.Kind != "" {
			path += " (" + change.Kind + ")"
		}
		paths = append(paths, path)
	}
	if details == "" {
		details = strings.Join(paths, "\n")
	} else {
		details += "\nFiles:\n" + strings.Join(paths, "\n")
	}
	const maxDetailsBytes = 8000
	if len(details) <= maxDetailsBytes {
		return details
	}
	return strings.ToValidUTF8(details[:maxDetailsBytes], "")
}
