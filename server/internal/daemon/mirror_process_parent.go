package daemon

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) bindMirrorProcess(ctx context.Context, generation mirrorControlGeneration, enqueue func([]byte) (*wsOutbound, error)) error {
	if !d.mirrorProcessMode() {
		return nil
	}
	client, err := d.ensureMirrorProcess(ctx)
	if err != nil {
		return err
	}
	resources, err := d.mirrorRoster()
	if err != nil {
		return err
	}
	d.mu.Lock()
	serverGeneration := d.vscreenServerGeneration
	d.mu.Unlock()
	return client.bind(ctx, mirrorControlBinding{Generation: uint64(generation), ServerGeneration: serverGeneration, Resources: resources}, enqueue)
}
func (d *Daemon) currentMirrorProcess() (*mirrorProcessClient, error) {
	d.mirrorProcessMu.Lock()
	defer d.mirrorProcessMu.Unlock()
	if d.mirrorProcess == nil || d.mirrorProcessClosed {
		return nil, errors.New("mirror service unavailable")
	}
	return d.mirrorProcess, nil
}
func (d *Daemon) mirrorCall(ctx context.Context, input mirrorProcessRequest, out any) error {
	client, err := d.currentMirrorProcess()
	if err != nil {
		return err
	}
	return client.call(ctx, input, out)
}
func (d *Daemon) forwardMirrorControl(ctx context.Context, kind string, msg mirrorOfferMessage) bool {
	if !d.mirrorProcessMode() {
		return false
	}
	client, err := d.currentMirrorProcess()
	if err == nil {
		client.mu.Lock()
		current := client.generation
		client.mu.Unlock()
		if current != uint64(msg.controlGeneration) {
			err = errors.New("stale control connection")
		} else {
			err = client.call(ctx, mirrorProcessRequest{Operation: "control", Payload: marshalRaw(protocol.Message{Type: kind, Payload: msg.raw})}, nil)
		}
	}
	if err != nil {
		if d.logger != nil {
			d.logger.Warn("mirror process control unavailable", "event", kind, "error", err)
		}
		var uncertain *mirrorUncertainOperation
		if msg.enqueue != nil && ctx.Err() == nil && !errors.As(err, &uncertain) {
			switch kind {
			case protocol.EventMirrorOffer:
				var offer protocol.MirrorOfferPayload
				if json.Unmarshal(msg.raw, &offer) == nil && offer.Validate() == nil {
					_ = d.sendMirrorAnswerFailure(msg.enqueue, offer, protocol.MirrorAnswerFailureCaptureUnavailable)
				}
			case protocol.EventVscreenQuery:
				var query protocol.VscreenQuery
				if json.Unmarshal(msg.raw, &query) == nil {
					_ = d.sendVscreen(msg.enqueue, msg.controlGeneration, protocol.EventVscreenQueryResult, protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope, Reason: protocol.VscreenNativeUnavailable})
				}
			}
		}
	}
	return true
}
func (d *Daemon) closeMirrorProcess() {
	d.mirrorProcessMu.Lock()
	d.mirrorProcessClosed = true
	client := d.mirrorProcess
	d.mirrorProcessMu.Unlock()
	if client != nil {
		if err := client.close(); err != nil && d.logger != nil {
			d.logger.Warn("mirror process shutdown unconfirmed", "error", err)
		}
	}
}
func (d *Daemon) refreshMirrorRoster() {
	client, err := d.currentMirrorProcess()
	if err != nil {
		return
	}
	client.mu.Lock()
	generation, enqueue := client.generation, client.enqueue
	client.mu.Unlock()
	if generation == 0 || enqueue == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	d.mu.Lock()
	removed := d.mirrorPendingRemoval
	d.mirrorPendingRemoval = map[string]bool{}
	d.mu.Unlock()
	for runtimeID := range removed {
		if err = client.call(ctx, mirrorProcessRequest{Operation: "remove", RuntimeID: runtimeID}, nil); err != nil {
			client.unbind(generation)
			if d.logger != nil {
				d.logger.Warn("mirror removed runtime cleanup unconfirmed", "error", err)
			}
			return
		}
	}
	if err = d.bindMirrorProcess(ctx, mirrorControlGeneration(generation), enqueue); err != nil && d.logger != nil {
		d.logger.Warn("mirror roster refresh failed", "error", err)
	}
}
func (d *Daemon) startProcessVscreen(ctx context.Context, task Task, provider string, stop func(error)) (json.RawMessage, *vscreenMCP, *vscreenExecution, error) {
	client, err := d.currentMirrorProcess()
	if err != nil {
		if task.MirrorSource == nil && task.VscreenContinuation == nil {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, err
	}
	claim := mirrorTaskClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, RuntimeID: task.RuntimeID, AgentID: task.AgentID, DispatchedAt: task.DispatchedAt, PriorSessionID: task.PriorSessionID, Source: task.MirrorSource, Continuation: task.VscreenContinuation, Model: mirrorModelForTask(task)}
	var grant mirrorExecutionGrant
	if err = client.call(ctx, mirrorProcessRequest{Operation: "acquire", Provider: provider, Claim: &claim}, &grant); err != nil {
		if task.MirrorSource == nil && task.VscreenContinuation == nil {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, err
	}
	if err = client.validateExecutionGrant(grant, claim); err != nil {
		client.close()
		return nil, nil, nil, err
	}
	if !grant.Active {
		return nil, nil, nil, nil
	}
	if grant.ExecutionID == "" || len(grant.MCP) == 0 {
		return nil, nil, nil, errors.New("invalid GUI execution grant")
	}
	claim.Model = nil
	remote := &mirrorRemoteExecution{client: client, id: grant.ExecutionID, claim: claim, stopProvider: stop, dependent: task.MirrorSource != nil || task.VscreenContinuation != nil, config: bytes.Clone(grant.MCP)}
	client.mu.Lock()
	client.executions[remote.id] = remote
	client.mu.Unlock()
	// Task context cancellation freezes child tools; only provider teardown later
	// supplies the stopped proof. This hook does not fabricate ProviderStopped.
	remote.cancelWatch = context.AfterFunc(ctx, func() { remote.cancelTools() })
	return grant.MCP, &vscreenMCP{closeRemote: remote.close}, &vscreenExecution{remote: remote}, nil
}
func (r *mirrorRemoteExecution) cancelTools() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_ = r.client.call(ctx, mirrorProcessRequest{Operation: "cancel_execution", ExecutionID: r.id, Claim: &r.claim}, nil)
}
func (r *mirrorRemoteExecution) close() {
	r.once.Do(func() {
		if r.cancelWatch != nil {
			r.cancelWatch()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		var released mirrorReleaseResult
		r.err = r.client.call(ctx, mirrorProcessRequest{Operation: "release", ExecutionID: r.id, Claim: &r.claim}, &released)
		if r.err == nil && !released.InterventionPending {
			r.client.mu.Lock()
			delete(r.client.executions, r.id)
			r.client.mu.Unlock()
		}
		if r.err != nil && r.client.daemon.logger != nil {
			r.client.daemon.logger.Warn("GUI resource release unconfirmed", "error", r.err)
		}
	})
}
func (r *mirrorRemoteExecution) invoke(ctx context.Context, name string, input json.RawMessage) ([]map[string]any, error) {
	var config struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(r.config, &config) != nil {
		return nil, errors.New("invalid GUI tool endpoint")
	}
	endpoint := config.Servers[vscreenMCPName]
	raw := marshalRaw(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": input}})
	request, err := http.NewRequestWithContext(ctx, "POST", endpoint.URL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	for key, value := range endpoint.Headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("GUI tool request rejected")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var result struct {
		Result struct {
			Content []map[string]any `json:"content"`
			IsError bool             `json:"isError"`
		} `json:"result"`
	}
	if json.Unmarshal(body, &result) != nil {
		return nil, &vscreen.Error{Reason: protocol.VscreenActionUncertainReason}
	}
	if result.Result.IsError {
		reason := protocol.VscreenActionUncertainReason
		if len(result.Result.Content) > 0 {
			if text, ok := result.Result.Content[0]["text"].(string); ok {
				switch protocol.VscreenRejectionReason(text) {
				case protocol.VscreenPermissionDenied, protocol.VscreenSourceGone, protocol.VscreenLeaseExpired, protocol.VscreenAppInUse, protocol.VscreenNativeUnavailable:
					reason = protocol.VscreenRejectionReason(text)
				}
			}
		}
		return nil, &vscreen.Error{Reason: reason}
	}
	return result.Result.Content, nil
}
func mirrorModelForTask(task Task) *mirrorGUIModel {
	if task.Agent == nil {
		return nil
	}
	env := task.Agent.CustomEnv
	endpoint := env["OPENAI_BASE_URL"]
	if endpoint == "" {
		endpoint = env["OPENAI_API_BASE"]
	}
	style, key := "openai", env["OPENAI_API_KEY"]
	if endpoint == "" && env["ANTHROPIC_API_KEY"] != "" {
		endpoint = env["ANTHROPIC_BASE_URL"]
		if endpoint == "" {
			endpoint = "https://api.anthropic.com/v1/messages"
		}
		style, key = "anthropic", env["ANTHROPIC_API_KEY"]
	}
	if endpoint == "" || task.Agent.Model == "" {
		return nil
	}
	if style == "openai" && !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint = strings.TrimRight(endpoint, "/") + "/chat/completions"
	}
	return &mirrorGUIModel{Endpoint: endpoint, Model: task.Agent.Model, APIKey: key, Style: style}
}
func (d *Daemon) processProviderStopped(ctx context.Context, claim mirrorTaskClaim) error {
	client, err := d.currentMirrorProcess()
	if err != nil {
		return err
	}
	client.mu.Lock()
	var found *mirrorRemoteExecution
	for _, execution := range client.executions {
		if mirrorClaimsEqual(execution.claim, claim) {
			found = execution
			break
		}
	}
	client.mu.Unlock()
	if found == nil {
		return nil
	}
	if err = client.call(ctx, mirrorProcessRequest{Operation: "provider_stopped", ExecutionID: found.id, Claim: &found.claim}, nil); err != nil {
		return err
	}
	client.mu.Lock()
	delete(client.executions, found.id)
	client.mu.Unlock()
	return nil
}
func (d *Daemon) verifiedMirrorLocal(ctx context.Context, capability string, action mirrorLocalAction) (mirrorLocalResult, error) {
	d.vscreenMu.Lock()
	verify := d.vscreenLocalOwner
	d.vscreenMu.Unlock()
	if verify == nil || !verify(ctx, capability) {
		return mirrorLocalResult{}, errors.New("local owner authentication required")
	}
	var result mirrorLocalResult
	err := d.mirrorCall(ctx, mirrorProcessRequest{Operation: "local", Local: &action}, &result)
	return result, err
}
func (c *mirrorProcessClient) validateExecutionGrant(grant mirrorExecutionGrant, claim mirrorTaskClaim) error {
	if !grant.Active {
		if grant.ExecutionID != "" || len(grant.MCP) > 0 {
			return errors.New("inactive GUI grant contains authority")
		}
		return nil
	}
	if grant.InstanceID != c.identity.InstanceID || len(grant.ExecutionID) != 48 || !mirrorClaimsEqual(grant.Claim, claim) {
		return errors.New("GUI execution grant identity differs")
	}
	if _, err := hex.DecodeString(grant.ExecutionID); err != nil {
		return err
	}
	var document map[string]json.RawMessage
	if len(grant.MCP) > 8192 || json.Unmarshal(grant.MCP, &document) != nil || len(document) != 1 {
		return errors.New("invalid GUI MCP grant")
	}
	var servers map[string]json.RawMessage
	if json.Unmarshal(document["mcpServers"], &servers) != nil || len(servers) != 1 {
		return errors.New("invalid GUI MCP server set")
	}
	var entry struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	decoder := json.NewDecoder(bytes.NewReader(servers[vscreenMCPName]))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&entry) != nil || entry.Type != "http" || len(entry.Headers) != 1 {
		return errors.New("invalid GUI MCP server")
	}
	u, err := url.Parse(entry.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || len(u.Path) != 49 {
		return errors.New("GUI MCP endpoint must be private loopback")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return errors.New("invalid GUI MCP port")
	}
	if _, err = hex.DecodeString(strings.TrimPrefix(u.Path, "/")); err != nil {
		return err
	}
	bearer := entry.Headers["Authorization"]
	if !strings.HasPrefix(bearer, "Bearer ") || len(bearer) != 55 {
		return errors.New("invalid GUI MCP credential")
	}
	_, err = hex.DecodeString(strings.TrimPrefix(bearer, "Bearer "))
	return err
}
