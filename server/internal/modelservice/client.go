package modelservice

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Client serializes control mutations, consumes receipts and explicitly acknowledges
// them before admitting the next mutation. Unknown outcomes never auto-replay.
type Client struct {
	process    *runtimeproc.Process
	transport  *runtimeproc.Client
	gate       chan struct{}
	instanceID string
	leaseMu    sync.Mutex
	active     map[*Lease]struct{}
	closed     bool
	closing    bool
	closeOnce  sync.Once
	closeErr   error
	uncertain  *UncertainOperation
}

func newClient(p *runtimeproc.Process) (*Client, error) {
	status, err := p.Client.Health(context.Background())
	if err != nil {
		return nil, errors.Join(err, p.Close())
	}
	return &Client{process: p, transport: p.Client, active: map[*Lease]struct{}{}, gate: make(chan struct{}, 1), instanceID: status.Identity.InstanceID}, nil
}
func (c *Client) read(ctx context.Context, op string, input any, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	body, err := c.transport.Read(ctx, op, raw)
	if err != nil {
		var p *runtimeproc.Error
		if errors.As(err, &p) {
			return decodeProblem(p)
		}
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}
func (c *Client) mutate(ctx context.Context, op string, input any, out any) error {
	if err := c.admit(ctx); err != nil {
		return err
	}
	defer func() { <-c.gate }()
	if c.closed {
		return jevmodels.ErrClosed
	}
	if c.uncertain != nil {
		return c.uncertain
	}
	status, err := c.transport.Health(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := c.transport.Request(op, status.Fence, payload)
	if err != nil {
		return err
	}
	req.Deadline = time.Now().Add(4 * time.Minute)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(req.Deadline) {
		req.Deadline = deadline
	}
	response, err := c.transport.Call(ctx, req)
	if err != nil {
		var rejection *runtimeproc.Error
		if errors.As(err, &rejection) {
			switch rejection.Code {
			case "unauthorized", "identity_mismatch", "stale_fence", "malformed", "unknown_operation", "retired_request", "draining", "stopped", "resource_exhausted", "deadline":
				return err
			}
		}
		return c.markUncertain(req, err)
	}
	if response.Receipt == nil || response.Receipt.State != "completed" {
		return c.markUncertain(req, errors.New("model operation outcome is pending"))
	}
	domainErr := decodeProblem(response.Receipt.Error)
	if domainErr == nil && out != nil {
		if err = json.Unmarshal(response.Receipt.Result, out); err != nil {
			return c.markUncertain(req, err)
		}
	}
	ack, err := c.transport.Request("acknowledge", response.Status.Fence, nil)
	if err != nil {
		return err
	}
	acknowledged, err := c.transport.Call(ctx, ack)
	if err != nil {
		return c.markAckUncertain(ack, response.Receipt, err)
	}
	if acknowledged.Receipt == nil || acknowledged.Receipt.State != "completed" || acknowledged.Receipt.Error != nil {
		return c.markAckUncertain(ack, response.Receipt, errors.New("receipt acknowledgement is pending"))
	}
	return domainErr
}
func (c *Client) Catalog(ctx context.Context) ([]jevmodels.Model, error) {
	var models []jevmodels.Model
	err := c.read(ctx, "model.catalog", struct{}{}, &models)
	return models, err
}
func (c *Client) Status(ctx context.Context, id, revision string) (jevmodels.Status, error) {
	var status jevmodels.Status
	err := c.read(ctx, "model.status", modelRequest{ModelID: id, Revision: revision}, &status)
	return status, err
}
func (c *Client) Register(ctx context.Context, id, revision string) (jevmodels.Model, error) {
	var model jevmodels.Model
	err := c.mutate(ctx, "model.register", modelRequest{ModelID: id, Revision: revision}, &model)
	return model, err
}
func (c *Client) StartInstall(ctx context.Context, id, revision string) error {
	return c.mutate(ctx, "model.install", modelRequest{ModelID: id, Revision: revision}, nil)
}
func (c *Client) CancelInstall(ctx context.Context, id, revision string) error {
	if revision == "" {
		status, err := c.Status(ctx, id, revision)
		if err != nil {
			return err
		}
		revision = status.Revision
	}
	var job InstallJob
	if err := c.read(ctx, "model.job", modelRequest{ModelID: id, Revision: revision}, &job); err != nil {
		return err
	}
	return c.mutate(ctx, "model.cancel", modelRequest{ModelID: id, Revision: revision, JobID: job.ID}, nil)
}
func (c *Client) Stop(ctx context.Context, id, revision string) error {
	return c.mutate(ctx, "model.stop", modelRequest{ModelID: id, Revision: revision}, nil)
}
func (c *Client) Remove(ctx context.Context, id, revision string) error {
	return c.mutate(ctx, "model.remove", modelRequest{ModelID: id, Revision: revision}, nil)
}
func (c *Client) Acquire(ctx context.Context, selection jevmodels.Selection, execution Execution) (*Lease, error) {
	c.leaseMu.Lock()
	closing := c.closing
	c.leaseMu.Unlock()
	if closing {
		return nil, jevmodels.ErrClosed
	}
	if err := execution.validate(); err != nil {
		return nil, err
	}
	var accepted acquireResult
	if err := c.mutate(ctx, "model.acquire", modelRequest{ModelID: selection.ModelID, Revision: selection.Revision, Device: selection.Device, Execution: execution}, &accepted); err != nil {
		return nil, err
	}
	var outcome acquireResult
	poll := time.NewTicker(40 * time.Millisecond)
	defer poll.Stop()
	for {
		if err := c.read(ctx, "model.acquire_status", modelRequest{JobID: accepted.ID, Execution: execution}, &outcome); err != nil {
			return nil, errors.Join(err, c.cancelAcquire(accepted.ID, execution))
		}
		if outcome.State != "pending" {
			break
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), c.cancelAcquire(accepted.ID, execution))
		case <-poll.C:
		}
	}
	if outcome.Error != nil {
		return nil, errors.Join(decodeProblem(outcome.Error), c.cancelAcquire(accepted.ID, execution))
	}
	if outcome.Grant == nil {
		return nil, errors.Join(errors.New("missing model lease grant"), c.cancelAcquire(accepted.ID, execution))
	}
	grant := *outcome.Grant
	if err := c.validateGrant(grant, selection, execution); err != nil {
		return nil, errors.Join(err, c.cancelAcquire(accepted.ID, execution))
	}
	if grant.LeaseID == "" || grant.Execution != execution || !grant.Deadline.After(time.Now()) {
		return nil, errors.New("invalid model lease grant")
	}
	if err := c.mutate(ctx, "model.acquire_ack", modelRequest{JobID: accepted.ID, LeaseID: grant.LeaseID, Execution: execution}, nil); err != nil {
		return nil, errors.Join(err, c.cancelAcquire(accepted.ID, execution))
	}
	c.leaseMu.Lock()
	if c.closing {
		c.leaseMu.Unlock()
		releaseCtx, finish := context.WithTimeout(context.Background(), 10*time.Second)
		defer finish()
		return nil, errors.Join(jevmodels.ErrClosed, c.mutate(releaseCtx, "model.release", modelRequest{LeaseID: grant.LeaseID, Execution: execution}, nil))
	}
	renewCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var renewalErr error
	lease := &Lease{Endpoint: grant.Endpoint, Token: grant.Token, Device: grant.Device, ExecutionIdentity: grant.ExecutionIdentity}
	go func() {
		defer close(done)
		renewEvery := time.Until(grant.Deadline) / 3
		if renewEvery < 10*time.Millisecond {
			renewEvery = 10 * time.Millisecond
		}
		ticker := time.NewTicker(renewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				// Finish an admitted renewal before release, avoiding a lost-ACK race
				// caused solely by our own cancellation. Each call is still bounded.
				if renewCtx.Err() != nil {
					return
				}
				renewal, finish := context.WithTimeout(context.Background(), 10*time.Second)
				renewalErr = c.mutate(renewal, "model.keepalive", modelRequest{LeaseID: grant.LeaseID, Execution: execution}, nil)
				finish()
				if renewalErr != nil {
					return
				}
			}
		}
	}()
	lease.release = func(ctx context.Context) error {
		cancel()
		<-done
		err := c.mutate(ctx, "model.release", modelRequest{LeaseID: grant.LeaseID, Execution: execution}, nil)
		c.leaseMu.Lock()
		delete(c.active, lease)
		c.leaseMu.Unlock()
		return errors.Join(renewalErr, err)
	}
	c.active[lease] = struct{}{}
	c.leaseMu.Unlock()
	return lease, nil
}
func (c *Client) Close() error { c.closeOnce.Do(func() { c.closeErr = c.close() }); return c.closeErr }
func (c *Client) close() error {
	c.leaseMu.Lock()
	c.closing = true
	leases := make([]*Lease, 0, len(c.active))
	for lease := range c.active {
		leases = append(leases, lease)
	}
	c.leaseMu.Unlock()
	var result error
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, lease := range leases {
		result = errors.Join(result, lease.Release(ctx))
	}
	if err := c.admit(ctx); err != nil {
		return errors.Join(result, err, c.process.Close())
	}
	defer func() { <-c.gate }()
	c.closed = true
	if err := c.process.Stop(ctx); err != nil {
		return errors.Join(result, err, c.process.Close())
	}
	return result
}

func (c *Client) admit(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.gate <- struct{}{}:
		return nil
	}
}
func (c *Client) validateGrant(grant grant, selection jevmodels.Selection, execution Execution) error {
	if grant.Device != "cpu" && grant.Device != "mps" && grant.Device != "cuda" {
		return errors.New("invalid model lease device")
	}
	if grant.InstanceID != c.instanceID || grant.Selection != selection || grant.Execution != execution || len(grant.LeaseID) != 64 || len(grant.Token) != 64 || !grant.Deadline.After(time.Now()) || grant.Deadline.After(time.Now().Add(leaseLifetime+time.Second)) {
		return errors.New("invalid model lease identity")
	}
	if _, err := hex.DecodeString(grant.Token + grant.LeaseID); err != nil {
		return errors.New("invalid model lease credential")
	}
	endpoint, err := url.Parse(grant.Endpoint)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "/leases/"+grant.LeaseID || strings.Contains(endpoint.Host, "@") {
		return errors.New("invalid model lease endpoint")
	}
	if err := validateExecutionIdentity(grant.ExecutionIdentity, execution); err != nil {
		return err
	}
	return nil
}

// validateExecutionIdentity is the fail-closed guard for the optional
// server-issued execution identity a lease may carry. nil means "not
// negotiated" and is valid. A non-nil identity must be a real, server-issued
// one: it carries the server-issued ExecutionID plus the workspace/runtime
// task identity and is never an invented/empty epoch. It must also match the
// execution the lease was acquired for, so an invented or mismatched identity
// fails closed rather than being accepted or silently dropped.
func validateExecutionIdentity(ei *protocol.ExecutionIdentity, execution Execution) error {
	if ei == nil {
		return nil
	}
	if ei.ExecutionID == "" || ei.TaskID == "" || ei.RuntimeID == "" || ei.DispatchedAt.IsZero() {
		return errors.New("invalid model lease execution identity")
	}
	if ei.TaskID != execution.TaskID || ei.RuntimeID != execution.RuntimeID {
		return errors.New("model lease execution identity conflicts with execution")
	}
	return nil
}

func (c *Client) cancelAcquire(id string, execution Execution) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return c.mutate(ctx, "model.acquire_cancel", modelRequest{JobID: id, Execution: execution}, nil)
}

// UncertainOperation identifies the exact receipt that must be resolved before
// further mutations. Reads remain available; the client never silently ACKs it.
type UncertainOperation struct {
	RequestID string
	Operation string
	Cause     error
	Completed *runtimeproc.Receipt
}

func (e *UncertainOperation) Error() string {
	if e.Completed != nil {
		return fmt.Sprintf("model operation %s completed; receipt acknowledgement %s requires reconciliation: %v", e.Completed.RequestID, e.RequestID, e.Cause)
	}
	return fmt.Sprintf("model operation %s (%s) requires reconciliation: %v", e.Operation, e.RequestID, e.Cause)
}
func (e *UncertainOperation) Unwrap() error { return e.Cause }
func (c *Client) markUncertain(req runtimeproc.Request, cause error) error {
	c.uncertain = &UncertainOperation{RequestID: req.RequestID, Operation: req.Operation, Cause: cause}
	return c.uncertain
}

// QueryOperation reads the preserved transport receipt without authorizing a replay.
func (c *Client) QueryOperation(ctx context.Context, id string) (runtimeproc.Receipt, error) {
	return c.transport.QueryOperation(ctx, id)
}

func (c *Client) markAckUncertain(req runtimeproc.Request, completed *runtimeproc.Receipt, cause error) error {
	c.uncertain = &UncertainOperation{RequestID: req.RequestID, Operation: req.Operation, Cause: cause, Completed: completed}
	return c.uncertain
}
