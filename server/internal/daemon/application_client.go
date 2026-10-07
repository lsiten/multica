package daemon

import (
	"context"
	"fmt"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (c *Client) claimApplications(ctx context.Context, runtimeID, daemonID string) ([]protocol.ApplicationClaim, error) {
	claims := []protocol.ApplicationClaim{}
	err := c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/applications/claim", runtimeID), map[string]string{"daemon_id": daemonID}, &claims)
	return claims, err
}

func (c *Client) syncApplications(ctx context.Context, runtimeID, daemonID string) ([]protocol.ApplicationRuntimeInstance, error) {
	instances := []protocol.ApplicationRuntimeInstance{}
	err := c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/applications/sync", runtimeID), map[string]string{"daemon_id": daemonID}, &instances)
	return instances, err
}

func (c *Client) observeApplication(ctx context.Context, runtimeID, daemonID string, observation protocol.ApplicationObservation) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/applications/observe", runtimeID), map[string]any{"daemon_id": daemonID, "observation": observation}, nil)
}

func (c *Client) completeApplication(ctx context.Context, runtimeID, daemonID string, claim protocol.ApplicationClaim, result protocol.ApplicationStepResult) error {
	return c.postJSONWithRetry(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/applications/steps/%s/result", runtimeID, claim.StepID), map[string]any{"daemon_id": daemonID, "result": result}, nil, defaultTerminalRetrySchedule)
}

func (c *Client) renewApplicationLease(ctx context.Context, runtimeID, daemonID string, claim protocol.ApplicationClaim) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/applications/steps/%s/lease", runtimeID, claim.StepID), map[string]string{"daemon_id": daemonID, "claim_token": claim.ClaimToken}, nil)
}
