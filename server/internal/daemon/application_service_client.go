package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ErrApplicationServiceGrantUnsupported keeps old servers on the existing control-owned path.
var ErrApplicationServiceGrantUnsupported = errors.New("server does not support application service grants")

func applicationServiceGrantError(err error) error {
	var response *requestError
	if errors.As(err, &response) && (response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed) {
		return ErrApplicationServiceGrantUnsupported
	}
	return err
}

// ApplicationServiceGrantState reads public authority metadata after an uncertain issuance.
func (c *Client) ApplicationServiceGrantState(ctx context.Context, runtimeID string) (protocol.ApplicationServiceGrantState, error) {
	var state protocol.ApplicationServiceGrantState
	err := c.getJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/application-service-grants", runtimeID), &state)
	if err != nil {
		return state, applicationServiceGrantError(err)
	}
	if state.Capability != protocol.ApplicationServiceGrantCapability || state.RuntimeID != runtimeID {
		return state, ErrApplicationServiceGrantUnsupported
	}
	return state, nil
}

// IssueApplicationServiceGrant is called only by the parent control credential.
// Renewal deliberately advances generation and requires data/control reconnect;
// it must never cause an arbitrary in-flight application request to be replayed.
func (c *Client) IssueApplicationServiceGrant(ctx context.Context, runtimeID string, input protocol.ApplicationServiceGrantRequest) (protocol.ApplicationServiceGrantResponse, error) {
	instance, err := uuid.Parse(input.ServiceInstanceID)
	if err != nil {
		return protocol.ApplicationServiceGrantResponse{}, fmt.Errorf("parse application service instance: %w", err)
	}
	input.ServiceInstanceID = instance.String()
	var grant protocol.ApplicationServiceGrantResponse
	err = c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/application-service-grants", runtimeID), input, &grant)
	if err != nil {
		return grant, applicationServiceGrantError(err)
	}
	if !validApplicationServiceGrant(grant) || grant.RuntimeID != runtimeID || grant.ServiceInstanceID != input.ServiceInstanceID || grant.Generation != input.ExpectedGeneration+1 {
		return protocol.ApplicationServiceGrantResponse{}, errInvalidResponseBody
	}
	return grant, nil
}

// RevokeApplicationServiceGrant withdraws only the named current service generation.
func (c *Client) RevokeApplicationServiceGrant(ctx context.Context, runtimeID string, input protocol.RevokeApplicationServiceGrantRequest) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/application-service-grants/revoke", runtimeID), input, nil)
}

// ApplicationServiceClient holds no account or task credential and exposes only
// the application runtime operations of its immutable service grant.
type ApplicationServiceClient struct {
	client *Client
	grant  protocol.ApplicationServiceGrantResponse
}

func validApplicationServiceGrant(grant protocol.ApplicationServiceGrantResponse) bool {
	if !protocol.ValidApplicationDaemonID(grant.DaemonID) || !strings.HasPrefix(grant.Token, "mps_") || len(grant.Token) != 68 || grant.Revoked || grant.Generation < 1 || grant.Capability != protocol.ApplicationServiceGrantCapability || !grant.ExpiresAt.After(time.Now()) || len(grant.Operations) == 0 {
		return false
	}
	for _, id := range []string{grant.WorkspaceID, grant.RuntimeID, grant.ServiceInstanceID} {
		if uuid.Validate(id) != nil {
			return false
		}
	}
	for _, operation := range grant.Operations {
		if !protocol.ApplicationServiceOperationAllowed(operation) {
			return false
		}
	}
	return true
}

// NewApplicationServiceClient constructs the child transport from a scoped secret.
func NewApplicationServiceClient(baseURL, daemonID, serviceInstanceID string, grant protocol.ApplicationServiceGrantResponse) (*ApplicationServiceClient, error) {
	if grant.DaemonID != daemonID || !validApplicationServiceGrant(grant) {
		return nil, errors.New("invalid application service grant")
	}
	instance, err := uuid.Parse(serviceInstanceID)
	if err != nil {
		return nil, fmt.Errorf("parse application service instance: %w", err)
	}
	grantedInstance, err := uuid.Parse(grant.ServiceInstanceID)
	if err != nil || instance != grantedInstance {
		return nil, errors.New("application service instance does not match grant")
	}
	grant.ServiceInstanceID = grantedInstance.String()
	grant.Operations = slices.Clone(grant.Operations)
	client := NewClient(baseURL)
	client.SetToken(grant.Token)
	return &ApplicationServiceClient{client: client, grant: grant}, nil
}

func (c *ApplicationServiceClient) allowed(operation string) error {
	if !slices.Contains(c.grant.Operations, operation) {
		return errors.New("application service operation is not granted")
	}
	if !c.grant.ExpiresAt.After(time.Now()) {
		return errors.New("application service grant expired")
	}
	return nil
}

// Sync reads the desired registry for exactly this runtime.
func (c *ApplicationServiceClient) Sync(ctx context.Context) ([]protocol.ApplicationRuntimeInstance, error) {
	if err := c.allowed("sync"); err != nil {
		return nil, err
	}
	return c.client.syncApplications(ctx, c.grant.RuntimeID, c.grant.DaemonID)
}

// Claim obtains application operations using the existing per-step lease contract.
func (c *ApplicationServiceClient) Claim(ctx context.Context) ([]protocol.ApplicationClaim, error) {
	if err := c.allowed("claim"); err != nil {
		return nil, err
	}
	return c.client.claimApplications(ctx, c.grant.RuntimeID, c.grant.DaemonID)
}

// Observe reports an instance's existing generation and revision guards.
func (c *ApplicationServiceClient) Observe(ctx context.Context, observation protocol.ApplicationObservation) error {
	if err := c.allowed("observe"); err != nil {
		return err
	}
	return c.client.observeApplication(ctx, c.grant.RuntimeID, c.grant.DaemonID, observation)
}

// Complete retries only the existing idempotent operation-result protocol.
func (c *ApplicationServiceClient) Complete(ctx context.Context, claim protocol.ApplicationClaim, result protocol.ApplicationStepResult) error {
	if err := c.allowed("result"); err != nil {
		return err
	}
	return c.client.completeApplication(ctx, c.grant.RuntimeID, c.grant.DaemonID, claim, result)
}

// RenewLease extends the exact application operation claim, not grant authority.
func (c *ApplicationServiceClient) RenewLease(ctx context.Context, claim protocol.ApplicationClaim) error {
	if err := c.allowed("lease"); err != nil {
		return err
	}
	return c.client.renewApplicationLease(ctx, c.grant.RuntimeID, c.grant.DaemonID, claim)
}

// Tunnel opens a single authenticated connection without replay or reconnect.
// Data sockets must still send the separate one-use stream token handshake.
func (c *ApplicationServiceClient) Tunnel(ctx context.Context, kind string) (*websocket.Conn, error) {
	if kind != "control" && kind != "data" {
		return nil, errors.New("invalid application tunnel kind")
	}
	if err := c.allowed("tunnel_" + kind); err != nil {
		return nil, err
	}
	return c.client.connectApplicationTunnel(ctx, c.grant.RuntimeID, kind, c.grant.DaemonID)
}

// CloseIdleConnections releases HTTP transport resources when a grant is replaced.
func (c *ApplicationServiceClient) CloseIdleConnections() { c.client.CloseIdleConnections() }
