package daemon

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type environmentFactOwnerKey struct{}

type environmentFact struct {
	RepoURL     string   `json:"repo_url,omitempty"`
	OwnerTaskID string   `json:"owner_task_id,omitempty"`
	Kind        string   `json:"kind"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
	RuntimeID   string   `json:"runtime_id,omitempty"`
	ID          string   `json:"id,omitempty"`
	IDs         []string `json:"ids,omitempty"`
	ClaimToken  string   `json:"claim_token,omitempty"`
}
type environmentFactEnvelope struct {
	InstanceID string          `json:"instance_id"`
	Deadline   time.Time       `json:"deadline"`
	Fact       environmentFact `json:"fact"`
}
type environmentFactReply struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}
type environmentFactCallback struct {
	daemon                     *Daemon
	instanceID, token, address string
	server                     *http.Server
	done                       chan struct{}
	admission                  chan struct{}
	cancel                     context.CancelFunc
	workers                    sync.WaitGroup
	mu                         sync.Mutex
	closed                     bool
}

func newEnvironmentFactCallback(d *Daemon, instanceID, token string) (*environmentFactCallback, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	callback := &environmentFactCallback{daemon: d, instanceID: instanceID, token: token, address: "http://" + listener.Addr().String(), admission: make(chan struct{}, 4), done: make(chan struct{}), cancel: cancel}
	callback.server = &http.Server{BaseContext: func(net.Listener) context.Context { return life }, Handler: http.HandlerFunc(callback.serve), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	go func() { defer close(callback.done); _ = callback.server.Serve(listener) }()
	return callback, nil
}
func (c *environmentFactCallback) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	_ = c.server.Close()
	<-c.done
	c.workers.Wait()
}
func (c *environmentFactCallback) serve(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		http.Error(w, "closed", http.StatusServiceUnavailable)
		return
	}
	c.workers.Add(1)
	c.mu.Unlock()
	defer c.workers.Done()
	if r.Method != http.MethodPost || r.URL.Path != "/facts" || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var envelope environmentFactEnvelope
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || decoder.Decode(&struct{}{}) != io.EOF || envelope.InstanceID != c.instanceID || !envelope.Deadline.After(time.Now()) || envelope.Deadline.After(time.Now().Add(35*time.Second)) {
		http.Error(w, "invalid fact request", http.StatusBadRequest)
		return
	}
	select {
	case c.admission <- struct{}{}:
		defer func() { <-c.admission }()
	default:
		http.Error(w, "fact capacity reached", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), envelope.Deadline)
	defer cancel()
	reply := c.fetch(ctx, envelope.Fact)
	if envelope.Fact.Kind == "git_auth" {
		defer clear(reply.Body)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(reply)
}
func (c *environmentFactCallback) fetch(ctx context.Context, fact environmentFact) environmentFactReply {
	reject := environmentFactReply{Status: http.StatusBadRequest, Body: json.RawMessage(`{"error":"invalid physical fact"}`)}
	if c.daemon.client == nil || len(fact.IDs) > 500 || len(fact.ID) > 256 || len(fact.WorkspaceID) > 256 || len(fact.RuntimeID) > 256 {
		return reject
	}
	for _, id := range append(append([]string{}, fact.IDs...), fact.ID, fact.WorkspaceID, fact.RuntimeID) {
		if strings.ContainsAny(id, "/\\\x00\r\n") {
			return reject
		}
	}
	var value any
	var err error
	if fact.Kind == "chat" || fact.Kind == "autopilot" {
		if fact.WorkspaceID == "" || fact.OwnerTaskID == "" {
			return reject
		}
		statuses, err := c.daemon.client.GetTaskGCChecks(ctx, fact.WorkspaceID, fact.RuntimeID, []string{fact.OwnerTaskID})
		if err != nil {
			return environmentFactReply{Status: 503, Body: json.RawMessage(`{"error":"physical owner facts unavailable"}`)}
		}
		owner := statuses[fact.OwnerTaskID]
		if owner == nil || owner.Missing || fact.Kind == "chat" && owner.ChatSessionID != fact.ID || fact.Kind == "autopilot" && owner.AutopilotRunID != fact.ID {
			return reject
		}
	}
	switch fact.Kind {
	case "git_auth":
		return c.gitAuth(ctx, fact.WorkspaceID, fact.RepoURL)
	case "task":
		if fact.ID == "" {
			return reject
		}
		value, err = c.daemon.client.GetTaskGCCheck(ctx, fact.ID)
		if err == nil {
			status := value.(*TaskGCStatus)
			if status.WorkspaceID == "" || fact.WorkspaceID != "" && status.WorkspaceID != fact.WorkspaceID || fact.RuntimeID != "" && status.RuntimeID != fact.RuntimeID {
				return reject
			}
		}
	case "tasks":
		if fact.WorkspaceID == "" || len(fact.IDs) == 0 {
			return reject
		}
		var values map[string]*TaskGCStatus
		values, err = c.daemon.client.GetTaskGCChecks(ctx, fact.WorkspaceID, fact.RuntimeID, fact.IDs)
		rows := make([]protocol.TaskGCStatus, 0, len(values))
		for _, value := range values {
			if value != nil {
				rows = append(rows, *value)
			}
		}
		value = protocol.TaskGCBatch{Tasks: rows}
	case "issue":
		if fact.ID == "" {
			return reject
		}

		if fact.WorkspaceID == "" {
			return reject
		}
		rows, lookupErr := c.daemon.client.GetIssueGCChecks(ctx, fact.WorkspaceID, []string{fact.ID})
		err = lookupErr
		if err == nil {
			row, ok := rows[fact.ID]
			if !ok || row.Err != nil {
				return environmentFactReply{Status: 503, Body: json.RawMessage(`{"error":"physical issue facts unavailable"}`)}
			}
			if !row.Found {
				return environmentFactReply{Status: 404, Body: json.RawMessage(`{"error":"issue absent from authorized workspace"}`)}
			}
			value = IssueGCStatus{Status: row.Status, Category: row.Category, UpdatedAt: row.UpdatedAt}
		}
	case "issues":
		if fact.WorkspaceID == "" || len(fact.IDs) == 0 {
			return reject
		}
		var values map[string]IssueGCCheckResult
		values, err = c.daemon.client.GetIssueGCChecks(ctx, fact.WorkspaceID, fact.IDs)
		rows := make([]IssueGCCheckResult, 0, len(values))
		for _, value := range values {
			rows = append(rows, value)
		}
		value = issueGCBatchResponse{Issues: rows}
	case "chat":
		if fact.ID == "" {
			return reject
		}
		value, err = c.daemon.client.GetChatSessionGCCheck(ctx, fact.ID)
	case "autopilot":
		if fact.ID == "" {
			return reject
		}
		value, err = c.daemon.client.GetAutopilotRunGCCheck(ctx, fact.ID)
	case "review_binding":
		if fact.ID == "" || fact.RuntimeID == "" {
			return reject
		}
		var binding protocol.LocalReviewRuntimeBinding
		err = c.daemon.client.getJSON(ctx, "/api/daemon/runtimes/"+url.PathEscape(fact.RuntimeID)+"/tasks/"+url.PathEscape(fact.ID)+"/review-binding", &binding)
		value = binding
	case "review_status":
		if fact.ID == "" || fact.RuntimeID == "" || fact.ClaimToken == "" {
			return reject
		}
		var status protocol.LocalReviewStatus
		err = c.daemon.client.postJSON(ctx, "/api/daemon/runtimes/"+url.PathEscape(fact.RuntimeID)+"/local-reviews/relay/"+url.PathEscape(fact.ID)+"/status", protocol.LocalReviewStatusRequest{ClaimToken: fact.ClaimToken}, &status)
		value = status
	default:
		return reject
	}
	if err != nil {
		var response *requestError
		status := http.StatusServiceUnavailable
		if errors.As(err, &response) && response.StatusCode >= 400 && response.StatusCode < 600 && response.StatusCode != http.StatusNotFound {
			status = response.StatusCode
		}
		return environmentFactReply{Status: status, Body: json.RawMessage(`{"error":"physical fact unavailable"}`)}
	}
	body, err := json.Marshal(value)
	if err != nil {
		return environmentFactReply{Status: 500, Body: json.RawMessage(`{"error":"invalid physical fact response"}`)}
	}
	return environmentFactReply{Status: 200, Body: body}
}

type environmentFactTransport struct {
	address, instanceID, token string
	http                       *http.Client
}

func (t *environmentFactTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	fact, err := environmentFactFromRequest(request)
	if err != nil {
		return nil, err
	}
	if scope, ok := request.Context().Value(environmentScopeContextKey{}).(environmentOperationScope); ok {
		if fact.WorkspaceID != "" && scope.WorkspaceID != "" && fact.WorkspaceID != scope.WorkspaceID || fact.RuntimeID != "" && scope.RuntimeID != "" && fact.RuntimeID != scope.RuntimeID {
			return nil, errors.New("physical fact scope changed")
		}
		if fact.WorkspaceID == "" {
			fact.WorkspaceID = scope.WorkspaceID
		}
		if fact.RuntimeID == "" {
			fact.RuntimeID = scope.RuntimeID
		}
	}
	if taskID, ok := request.Context().Value(environmentFactOwnerKey{}).(string); ok {
		fact.OwnerTaskID = taskID
	}
	reply, err := t.fetch(request.Context(), fact)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: reply.Status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(reply.Body)), Request: request}, nil
}

func (t *environmentFactTransport) fetch(parent context.Context, fact environmentFact) (environmentFactReply, error) {
	deadline := time.Now().Add(30 * time.Second)
	if earlier, ok := parent.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	body, err := json.Marshal(environmentFactEnvelope{InstanceID: t.instanceID, Deadline: deadline, Fact: fact})
	if err != nil {
		return environmentFactReply{}, err
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	outgoing, err := http.NewRequestWithContext(ctx, http.MethodPost, t.address+"/facts", bytes.NewReader(body))
	if err != nil {
		return environmentFactReply{}, err
	}
	outgoing.Header.Set("Authorization", "Bearer "+t.token)
	outgoing.Header.Set("Content-Type", "application/json")
	response, err := t.http.Do(outgoing)
	if err != nil {
		return environmentFactReply{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return environmentFactReply{}, errors.New("physical fact callback unavailable")
	}
	var reply environmentFactReply
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&reply); err != nil {
		return environmentFactReply{}, err
	}
	if reply.Status < 200 || reply.Status > 599 || !json.Valid(reply.Body) {
		return environmentFactReply{}, errors.New("invalid physical fact callback response")
	}
	return reply, nil
}

func environmentFactFromRequest(request *http.Request) (environmentFact, error) {
	invalid := errors.New("operation is outside physical fact scope")
	if request.URL.Host != "physical-facts.invalid" || request.URL.RawQuery != "" {
		return environmentFact{}, invalid
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "daemon" {
		return environmentFact{}, invalid
	}
	if request.Method == http.MethodGet && len(parts) == 5 && parts[4] == "gc-check" {
		kinds := map[string]string{"tasks": "task", "issues": "issue", "chat-sessions": "chat", "autopilot-runs": "autopilot"}
		if kind := kinds[parts[2]]; kind != "" {
			return environmentFact{Kind: kind, ID: parts[3]}, nil
		}
	}
	if request.Method == http.MethodGet && len(parts) == 7 && parts[2] == "runtimes" && parts[4] == "tasks" && parts[6] == "review-binding" {
		return environmentFact{Kind: "review_binding", RuntimeID: parts[3], ID: parts[5]}, nil
	}
	if request.Method == http.MethodPost && parts[2] == "workspaces" && parts[len(parts)-1] == "gc-check" {
		var body struct {
			TaskIDs  []string `json:"task_ids"`
			IssueIDs []string `json:"issue_ids"`
		}
		decoder := json.NewDecoder(io.LimitReader(request.Body, 256<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil {
			return environmentFact{}, invalid
		}
		if len(parts) == 6 && parts[4] == "issues" && len(body.TaskIDs) == 0 {
			return environmentFact{Kind: "issues", WorkspaceID: parts[3], IDs: body.IssueIDs}, nil
		}
		if len(parts) == 6 && parts[4] == "tasks" && len(body.IssueIDs) == 0 {
			return environmentFact{Kind: "tasks", WorkspaceID: parts[3], IDs: body.TaskIDs}, nil
		}
		if len(parts) == 8 && parts[4] == "runtimes" && parts[6] == "tasks" && len(body.IssueIDs) == 0 {
			return environmentFact{Kind: "tasks", WorkspaceID: parts[3], RuntimeID: parts[5], IDs: body.TaskIDs}, nil
		}
	}
	if request.Method == http.MethodPost && len(parts) == 8 && parts[2] == "runtimes" && parts[4] == "local-reviews" && parts[5] == "relay" && parts[7] == "status" {
		var body protocol.LocalReviewStatusRequest
		decoder := json.NewDecoder(io.LimitReader(request.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) == nil {
			return environmentFact{Kind: "review_status", RuntimeID: parts[3], ID: parts[6], ClaimToken: body.ClaimToken}, nil
		}
	}
	return environmentFact{}, invalid
}
