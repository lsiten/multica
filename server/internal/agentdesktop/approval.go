package agentdesktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/service"
)

const (
	approvalPollInterval = 750 * time.Millisecond
	approvalExpiry       = 5 * time.Minute
)

var (
	errApprovalDenied      = errors.New("desktop action rejected by the member")
	errApprovalUnavailable = errors.New("desktop approval is unavailable")
)

type desktopApprover interface {
	Approve(context.Context, approvalRequirement, bool) error
}

type humanRequestApprover struct {
	client  *cli.APIClient
	taskID  string
	headed  bool
	poll    time.Duration
	expires time.Duration
}

type approvalRequestView struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
	Payload  struct {
		Key string `json:"key"`
	} `json:"payload"`
	Response json.RawMessage `json:"response"`
}

type approvalAnswer struct {
	Revision int64  `json:"revision"`
	Decision string `json:"decision"`
}

// newHumanRequestApprover uses the daemon-provided task identity. The bridge
// never falls back to a member token when that identity is incomplete.
func newHumanRequestApprover(taskID string, headed bool) desktopApprover {
	serverURL := strings.TrimSpace(os.Getenv("MULTICA_SERVER_URL"))
	workspaceID := strings.TrimSpace(os.Getenv("MULTICA_WORKSPACE_ID"))
	token := strings.TrimSpace(os.Getenv("MULTICA_TOKEN"))
	if serverURL == "" || workspaceID == "" || strings.TrimSpace(taskID) == "" || !strings.HasPrefix(token, cli.TaskTokenPrefix) {
		return unavailableApprover{}
	}
	client := cli.NewAPIClient(serverURL, workspaceID, token)
	client.AgentID = strings.TrimSpace(os.Getenv("MULTICA_AGENT_ID"))
	client.TaskID = taskID
	return &humanRequestApprover{
		client:  client,
		taskID:  taskID,
		headed:  headed,
		poll:    approvalPollInterval,
		expires: approvalExpiry,
	}
}

type unavailableApprover struct{}

func (unavailableApprover) Approve(context.Context, approvalRequirement, bool) error {
	return fmt.Errorf("%w: this run has no accessible human-request identity", errApprovalUnavailable)
}

func (a *humanRequestApprover) Approve(ctx context.Context, requirement approvalRequirement, headed bool) error {
	if len(requirement.Commands) == 0 {
		return nil
	}
	key := approvalRequestKey(a.taskID, requirement)
	input := service.HumanRequestInput{
		Key:              key,
		Kind:             "confirmation",
		Title:            approvalTitle(requirement),
		ActionLabel:      "Approve",
		Next:             "I will perform this desktop action once if approved.",
		Impact:           approvalImpact(requirement, headed),
		Details:          "Commands: " + strings.Join(requirement.Commands, ", "),
		ExpiresInSeconds: int64(a.expires / time.Second),
		ResponseMode:     "card_only",
		Purpose:          "desktop_approval",
	}
	var created approvalRequestView
	if err := a.client.PostJSON(ctx, "/api/human-requests/", input, &created); err != nil {
		return fmt.Errorf("%w: create request: %v", errApprovalUnavailable, err)
	}
	if created.ID == "" || created.Revision < 1 {
		return fmt.Errorf("%w: request response did not include id and revision", errApprovalUnavailable)
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("desktop approval cancelled: %w", ctx.Err())
		case <-timer.C:
		}
		var current approvalRequestView
		if err := a.client.GetJSON(ctx, "/api/human-requests/"+created.ID, &current); err != nil {
			return fmt.Errorf("%w: read request: %v", errApprovalUnavailable, err)
		}
		decided, err := approvalDecision(current, created.Revision)
		if err != nil || decided {
			return err
		}
		timer.Reset(a.poll)
	}
}

func approvalDecision(request approvalRequestView, revision int64) (bool, error) {
	switch request.Status {
	case "pending":
		return false, nil
	case "answered":
		var answer approvalAnswer
		if err := json.Unmarshal(request.Response, &answer); err != nil {
			return true, fmt.Errorf("%w: invalid response", errApprovalUnavailable)
		}
		if answer.Revision != revision || answer.Decision != "approve" {
			return true, errApprovalDenied
		}
		return true, nil
	case "declined", "cancelled", "expired":
		return true, errApprovalDenied
	default:
		return true, fmt.Errorf("%w: request state %q", errApprovalUnavailable, request.Status)
	}
}

func approvalRequestKey(taskID string, requirement approvalRequirement) string {
	sum := sha256.Sum256([]byte(taskID + "\x00" + requirement.CallID + "\x00" + requirement.Tool + "\x00" + strings.Join(requirement.Commands, "\x00")))
	return "agent-desktop-" + hex.EncodeToString(sum[:12])
}

func approvalTitle(requirement approvalRequirement) string {
	title := "Approve " + requirement.Tool
	if len(title) > 100 {
		return title[:100]
	}
	return title
}

func approvalImpact(requirement approvalRequirement, headed bool) string {
	input := "semantic accessibility actions"
	if headed {
		input = "semantic actions with physical pointer and keyboard input enabled"
	}
	return "Allows one " + requirement.Tool + " call using " + input + ": " + strings.Join(requirement.Commands, ", ") + "."
}
