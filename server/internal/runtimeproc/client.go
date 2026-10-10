package runtimeproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Client authenticates every response against the expected immutable identity.
type Client struct {
	replayEpoch atomic.Uint64
	record      Record
	http        *http.Client
}

// NewClient validates a private record. It never follows redirects or environment proxies.
func NewClient(record Record) (*Client, error) {
	if err := record.Identity.Validate(); err != nil {
		return nil, err
	}
	if len(record.Token) != 64 {
		return nil, errors.New("invalid runtime credential")
	}
	if err := validateOrigin(record.Address); err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: 8192}
	client := &Client{record: record, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	client.replayEpoch.Store(record.ReplayEpoch)
	return client, nil
}

// Open reopens only the expected scoped identity and authenticates readiness.
func Open(ctx context.Context, root string, expected Identity) (*Client, error) {
	record, err := ReadRecord(root, expected)
	if err != nil {
		return nil, err
	}
	c, err := NewClient(record)
	if err != nil {
		return nil, err
	}
	_, err = c.Handshake(ctx)
	return c, err
}

// Call submits exactly the supplied request. Retrying must preserve its full contents.
func (c *Client) Call(ctx context.Context, request Request) (Response, error) {
	var out Response
	if request.Deadline.IsZero() {
		return out, errors.New("runtime deadline required")
	}
	callCtx, cancel := context.WithDeadline(ctx, request.Deadline)
	defer cancel()
	raw, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	if len(raw) > maxBody {
		return out, errors.New("runtime request exceeds limit")
	}
	req, err := http.NewRequestWithContext(callCtx, "POST", c.record.Address+"/rpc", bytes.NewReader(raw))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+c.record.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if callCtx.Err() != nil {
			return out, callCtx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return out, context.DeadlineExceeded
		}
		return out, errors.New("runtime control channel unavailable; owner is suspect")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+8193))
	if err != nil {
		return out, err
	}
	if len(body) > maxBody+8192 {
		return out, errors.New("runtime response exceeds limit")
	}
	if json.Unmarshal(body, &out) != nil {
		return out, errors.New("invalid runtime response")
	}
	if out.Status.Identity != c.record.Identity {
		return out, errors.New("runtime response identity mismatch")
	}
	if out.Status.ReplayEpoch == 0 {
		return out, errors.New("invalid runtime replay epoch")
	}
	for old := c.replayEpoch.Load(); out.Status.ReplayEpoch > old; old = c.replayEpoch.Load() {
		if c.replayEpoch.CompareAndSwap(old, out.Status.ReplayEpoch) {
			break
		}
	}
	if out.Error != nil {
		return out, out.Error
	}
	if res.StatusCode != 200 {
		return out, errors.New("runtime rejected request")
	}
	return out, nil
}

// Request creates a fresh bounded request; retain it unchanged for safe retries.
func (c *Client) Request(operation string, fence Fence, payload json.RawMessage) (Request, error) {
	id, err := randomHex(16)
	epoch := c.replayEpoch.Load()
	return Request{ReplayEpoch: epoch, Identity: c.record.Identity, RequestID: fmt.Sprintf("%d:%s", epoch, id), Operation: operation, Deadline: time.Now().Add(30 * time.Second), Fence: fence, Payload: payload}, err
}

// Handshake confirms build/protocol/instance and completed dependency readiness.
func (c *Client) Handshake(ctx context.Context) (Status, error) {
	r, err := c.Request("handshake", Fence{}, nil)
	if err != nil {
		return Status{}, err
	}
	response, err := c.Call(ctx, r)
	if err == nil && response.Status.State != "ready" {
		err = errors.New("runtime is not ready")
	}
	return response.Status, err
}

// Health reports authenticated state; errors mean suspect, never stopped.
func (c *Client) Health(ctx context.Context) (Status, error) {
	r, err := c.Request("health", Fence{}, nil)
	if err != nil {
		return Status{}, err
	}
	out, err := c.Call(ctx, r)
	return out.Status, err
}

// QueryOperation distinguishes absent, pending/uncertain and durable completion.
func (c *Client) QueryOperation(ctx context.Context, id string) (Receipt, error) {
	payload, _ := json.Marshal(map[string]string{"request_id": id})
	req, err := c.Request("query_operation", Fence{}, payload)
	if err != nil {
		return Receipt{}, err
	}
	out, err := c.Call(ctx, req)
	if err != nil {
		return Receipt{}, err
	}
	if out.Receipt == nil || out.Receipt.RequestID != id {
		return Receipt{}, errors.New("invalid runtime receipt")
	}
	return *out.Receipt, nil
}

// Read invokes a declared readonly capability without consuming an operation receipt.
func (c *Client) Read(ctx context.Context, operation string, payload json.RawMessage) (json.RawMessage, error) {
	request, err := c.Request(operation, Fence{}, payload)
	if err != nil {
		return nil, err
	}
	response, err := c.Call(ctx, request)
	if err != nil {
		return nil, err
	}
	if response.Receipt == nil || response.Receipt.State != "read" {
		return nil, errors.New("invalid readonly response")
	}
	return response.Receipt.Result, nil
}
