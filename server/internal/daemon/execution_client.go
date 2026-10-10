package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ErrExecutionUnsupported requires the caller to keep drain-before-restart on old servers.
var ErrExecutionUnsupported = errors.New("server does not support execution reconciliation")

// AcquireExecutionSupervisor negotiates execution authority without activating
// worker retention. Callers persist their instance and expected epoch for retry.
func (c *Client) AcquireExecutionSupervisor(ctx context.Context, runtimeID string, request protocol.SupervisorRequest) (*protocol.SupervisorResponse, error) {
	var response protocol.SupervisorResponse
	err := c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/execution-supervisor", runtimeID), request, &response)
	var requestErr *requestError
	if errors.As(err, &requestErr) && (requestErr.StatusCode == http.StatusNotFound || requestErr.StatusCode == http.StatusMethodNotAllowed) {
		return nil, ErrExecutionUnsupported
	}
	if err != nil {
		return nil, err
	}
	if response.Epoch < 1 || response.InstanceID != request.InstanceID || !slices.Contains(response.Capabilities, protocol.ExecutionCapabilityV1) {
		return nil, ErrExecutionUnsupported
	}
	return &response, nil
}

// BindTaskExecution reserves an exact worker and dispatch before provider launch.
func (c *Client) BindTaskExecution(ctx context.Context, runtimeID, taskID string, request protocol.BindExecutionRequest) (*protocol.ExecutionIdentity, error) {
	var response protocol.ExecutionIdentity
	err := c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/tasks/%s/execution", runtimeID, taskID), request, &response)
	if err != nil {
		return nil, err
	}
	if response.TaskID != taskID || response.RuntimeID != runtimeID || response.WorkerID != request.WorkerID || !response.DispatchedAt.Equal(request.DispatchedAt) || uuid.Validate(response.ExecutionID) != nil {
		return nil, errInvalidResponseBody
	}
	return &response, nil
}

// IssueExecutionGrant obtains or rotates the credential delivered to a worker.
func (c *Client) IssueExecutionGrant(ctx context.Context, runtimeID, taskID string, request protocol.ExecutionGrantRequest) (*protocol.ExecutionGrantResponse, error) {
	var response protocol.ExecutionGrantResponse
	err := c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/tasks/%s/execution-grants", runtimeID, taskID), request, &response)
	if err != nil {
		return nil, err
	}
	if response.Identity.TaskID != taskID || response.Identity.RuntimeID != runtimeID || response.Identity.ExecutionID != request.ExecutionID || !validExecutionGrant(response) {
		return nil, errInvalidResponseBody
	}
	return &response, nil
}

// RevokeExecutionGrant withdraws one credential without replacing task identity.
func (c *Client) RevokeExecutionGrant(ctx context.Context, runtimeID, taskID string, request protocol.ExecutionGrantRequest) error {
	request.Operations = nil
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/tasks/%s/execution-grants", runtimeID, taskID), request, nil)
}

// ReconcileExecutions uploads one bounded inventory page using control authority.
func (c *Client) ReconcileExecutions(ctx context.Context, runtimeID string, request protocol.ReconcileExecutionsRequest) (*protocol.ReconcileExecutionsResponse, error) {
	var response protocol.ReconcileExecutionsResponse
	err := c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/executions/reconcile", runtimeID), request, &response)
	if err != nil {
		return nil, err
	}
	return &response, nil
}

// ExecutionClient holds only a scoped credential and cannot perform runtime management.
// Legacy report outboxes continue using Client and acquire no invented identity.
type ExecutionClient struct {
	client   *Client
	identity protocol.ExecutionIdentity
}

// NewExecutionClient creates the transport handed to a single worker.
func NewExecutionClient(baseURL string, grant protocol.ExecutionGrantResponse) (*ExecutionClient, error) {
	if !validExecutionGrant(grant) {
		return nil, errors.New("invalid execution grant")
	}
	client := NewClient(baseURL)
	client.SetToken(grant.Token)
	return &ExecutionClient{client: client, identity: grant.Identity}, nil
}

// Callback reports only this execution's allowed operations. Server-side grant
// validation remains authoritative, including expiry and membership revocation.
func (c *ExecutionClient) Callback(ctx context.Context, operation string, request, response any) error {
	return c.callback(ctx, operation, "", request, response)
}

func (c *ExecutionClient) callback(ctx context.Context, operation, commentID string, request, response any) error {
	path, ok := protocol.ExecutionCallbackPath("POST", operation, c.identity.TaskID, commentID)
	if !ok {
		return errors.New("unsupported worker operation or resource")
	}
	return c.client.postJSON(ctx, path, request, response)
}

// ClaimSupplement makes one claim attempt; a lost response never triggers another claim.
func (c *ExecutionClient) ClaimSupplement(ctx context.Context) (*TaskSupplement, error) {
	var result TaskSupplement
	if err := c.callback(ctx, "supplements/claim", "", map[string]any{}, &result); err != nil {
		return nil, err
	}
	if result.CommentID == "" {
		return nil, nil
	}
	return &result, nil
}

// AckSupplement acknowledges the exact comment without reinjecting or claiming input.
func (c *ExecutionClient) AckSupplement(ctx context.Context, commentID string, delivered bool, reason string) error {
	return c.callback(ctx, "supplements/ack", commentID, map[string]any{"delivered": delivered, "error": reason}, nil)
}

// RenewPrepareLease uses the task-scoped route; runtime management is not granted.
func (c *ExecutionClient) RenewPrepareLease(ctx context.Context) error {
	return c.Callback(ctx, "prepare-lease", map[string]any{}, nil)
}

// Status queries the current task state through the same scoped transport.
func (c *ExecutionClient) Status(ctx context.Context, response any) error {
	path, ok := protocol.ExecutionCallbackPath("GET", "status", c.identity.TaskID, "")
	if !ok {
		return errors.New("invalid execution task identity")
	}
	return c.client.getJSON(ctx, path, response)
}

// ReportTaskMessages posts this execution's transcript rows through the scoped
// transport so a task worker can drive the provider-run drain loop without a
// *Daemon. The task is fixed by the grant identity; the server re-validates the
// token scope, expiry, and membership before persisting the rows.
func (c *ExecutionClient) ReportTaskMessages(ctx context.Context, messages []TaskMessageData) error {
	return c.callback(ctx, "messages", "", map[string]any{"messages": messages}, nil)
}

// PinTaskSession persists this execution's resume pointer mid-flight through the
// scoped transport. A pin with no session id and no work dir is a no-op, matching
// the legacy in-process client so an empty probe never posts.
func (c *ExecutionClient) PinTaskSession(ctx context.Context, sessionID, workDir string) error {
	if sessionID == "" && workDir == "" {
		return nil
	}
	body := map[string]any{}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	if workDir != "" {
		body["work_dir"] = workDir
	}
	return c.callback(ctx, "session", "", body, nil)
}

// ListRuntimeExecutions reads a bounded authoritative page before reconciliation.
func (c *Client) ListRuntimeExecutions(ctx context.Context, runtimeID, after string) (*protocol.ExecutionInventoryResponse, error) {
	var response protocol.ExecutionInventoryResponse
	path := fmt.Sprintf("/api/daemon/runtimes/%s/executions?limit=100", runtimeID)
	if after != "" {
		path += "&after=" + url.QueryEscape(after)
	}
	if err := c.getJSON(ctx, path, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func validExecutionGrant(grant protocol.ExecutionGrantResponse) bool {
	return strings.HasPrefix(grant.Token, "mwt_") && len(grant.Token) == 68 && grant.ExpiresAt.After(time.Now()) && uuid.Validate(grant.Identity.TaskID) == nil && uuid.Validate(grant.Identity.RuntimeID) == nil && uuid.Validate(grant.Identity.WorkerID) == nil && uuid.Validate(grant.Identity.ExecutionID) == nil && !grant.Identity.DispatchedAt.IsZero()
}
