package applicationhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client requires nonce authentication and validates the host's immutable scope on every reply.
type Client struct {
	record Record
	http   *http.Client
}

// NewClient connects only to the validated, machine-local host origin in a private record.
func NewClient(record Record) (*Client, error) {
	if err := record.Validate(); err != nil {
		return nil, err
	}
	if record.Address == "" {
		return nil, errors.New("application host has not announced its address")
	}
	return &Client{record: record, http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) request(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.record.Address+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.record.Token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return errors.New("application host control channel is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("application host rejected the request (%d)", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 128<<10)).Decode(out)
}

func (c *Client) validate(status Status) error {
	command := c.record.Command
	if status.HostID != c.record.HostID || status.InstanceID != command.InstanceID || status.WorkspaceID != command.WorkspaceID || status.RuntimeID != command.RuntimeID || status.Observation.InstanceID != command.InstanceID {
		return errors.New("application host ownership could not be confirmed")
	}
	return nil
}

// Status proves the identity of a service host without relying on its PID or port alone.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	if err := c.request(ctx, http.MethodGet, "/status", nil, &status); err != nil {
		return status, err
	}
	return status, c.validate(status)
}

// Stop requests a generation-fenced shutdown and waits for confirmed descendant cleanup.
func (c *Client) Stop(ctx context.Context, generation int64) (Status, error) {
	var status Status
	if err := c.request(ctx, http.MethodPost, "/stop", map[string]int64{"generation": generation}, &status); err != nil {
		return status, err
	}
	if err := c.validate(status); err != nil {
		return status, err
	}
	if status.Observation.Generation != generation || status.Observation.ProcessState != "stopped" {
		return status, errors.New("application host did not confirm service shutdown")
	}
	return status, nil
}

// Resume advances a proved running host after an unexecuted stop was cancelled.
func (c *Client) Resume(ctx context.Context, generation, revision int64) (Status, error) {
	var status Status
	if err := c.request(ctx, http.MethodPost, "/resume", map[string]int64{"expected_generation": c.record.Command.Generation, "generation": generation, "revision": revision}, &status); err != nil {
		return status, err
	}
	if err := c.validate(status); err != nil {
		return status, err
	}
	if status.Observation.Generation != generation || status.Observation.Revision != revision || status.Observation.ProcessState != "running" {
		return status, errors.New("application host did not confirm resuming the existing process")
	}
	return status, nil
}

// Logs returns bounded, redacted output from this authenticated host only.
func (c *Client) Logs(ctx context.Context, cursor string, limit int) (LogPage, error) {
	var page LogPage
	if limit < 1 || limit > 65536 {
		return page, errors.New("invalid application log limit")
	}
	if _, err := c.Status(ctx); err != nil {
		return page, err
	}
	query := url.Values{"cursor": {cursor}, "limit": {strconv.Itoa(limit)}}
	if err := c.request(ctx, http.MethodGet, "/logs?"+query.Encode(), nil, &page); err != nil {
		return page, err
	}
	return page, nil
}
