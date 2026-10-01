package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	identityActionsMCPName            = "multica-identity-actions"
	identityActionsMCPProtocolVersion = "2024-11-05"
	identityActionsMCPMaxRequestBytes = 80 << 10
	identityActionsMCPCallTimeout     = 60 * time.Second
	identitySendEmailToolName         = "multica_identity_send_email"
)

type identityEmailInvoker func(ctx context.Context, taskID, recipient, subject, body string) error

type identityActionsMCPServer struct {
	taskID    string
	path      string
	sendEmail identityEmailInvoker
	logger    *slog.Logger
}

type identityActionsMCPSet struct {
	server   *http.Server
	listener net.Listener
	once     sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
}

func (set *identityActionsMCPSet) Close() {
	if set == nil {
		return
	}
	set.once.Do(func() {
		if set.cancel != nil {
			set.cancel()
		}
		if set.done != nil {
			close(set.done)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if set.server != nil {
			_ = set.server.Shutdown(ctx)
		}
		if set.listener != nil {
			_ = set.listener.Close()
		}
	})
}

// startTaskIdentityActionsMCP exposes only the explicitly granted identity
// actions. The daemon-owned callback sends via host SMTP; tool arguments
// never accept credentials or a configurable From address.
func startTaskIdentityActionsMCP(lifetimeCtx context.Context, taskID string, allowEmail bool, sendEmail identityEmailInvoker, logger *slog.Logger) (json.RawMessage, *identityActionsMCPSet, error) {
	return startTaskIdentityActionsMCPAt(lifetimeCtx, taskID, allowEmail, sendEmail, logger, "127.0.0.1", "")
}

func startTaskIdentityActionsMCPAt(lifetimeCtx context.Context, taskID string, allowEmail bool, sendEmail identityEmailInvoker, logger *slog.Logger, listenHost, advertisedHost string) (json.RawMessage, *identityActionsMCPSet, error) {
	if !allowEmail || sendEmail == nil {
		return nil, nil, nil
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(listenHost, "0"))
	if err != nil {
		return nil, nil, fmt.Errorf("listen for identity actions MCP server: %w", err)
	}
	token, err := randomBrokerToken()
	if err != nil {
		_ = listener.Close()
		return nil, nil, fmt.Errorf("create identity actions MCP token: %w", err)
	}
	handler := &identityActionsMCPServer{taskID: taskID, path: "/" + token, sendEmail: sendEmail, logger: logger}
	serviceCtx, cancelService := context.WithCancel(lifetimeCtx)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return serviceCtx }}
	set := &identityActionsMCPSet{server: server, listener: listener, cancel: cancelService, done: make(chan struct{})}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && logger != nil {
			logger.Warn("identity actions MCP server stopped unexpectedly", "task_id", taskID, "error", serveErr)
		}
	}()
	go func() {
		select {
		case <-lifetimeCtx.Done():
			set.Close()
		case <-set.done:
		}
	}()
	host := advertisedHost
	if host == "" {
		host, _, _ = net.SplitHostPort(listener.Addr().String())
	}
	port := listener.Addr().(*net.TCPAddr).Port
	config, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		identityActionsMCPName: map[string]any{
			"type": "http",
			"url":  "http://" + net.JoinHostPort(host, fmt.Sprintf("%d", port)) + handler.path,
		},
	}})
	if err != nil {
		set.Close()
		return nil, nil, fmt.Errorf("marshal identity actions MCP config: %w", err)
	}
	return config, set, nil
}

func (s *identityActionsMCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.path || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, identityActionsMCPMaxRequestBytes+1))
	if err != nil {
		writePluginHookMCPError(w, nil, -32700, "could not read the request")
		return
	}
	if len(body) > identityActionsMCPMaxRequestBytes {
		writePluginHookMCPError(w, nil, -32600, "request is too large")
		return
	}
	var request pluginHookMCPRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writePluginHookMCPError(w, nil, -32700, "request is not valid JSON-RPC")
		return
	}
	switch request.Method {
	case "initialize":
		writePluginHookMCPResult(w, request.ID, map[string]any{
			"protocolVersion": identityActionsMCPProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": identityActionsMCPName, "version": "1"},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writePluginHookMCPResult(w, request.ID, map[string]any{"tools": []map[string]any{identitySendEmailToolDescriptor()}})
	case "tools/call":
		s.handleCall(w, r, request)
	default:
		writePluginHookMCPError(w, request.ID, -32601, "unsupported method "+request.Method)
	}
}

func identitySendEmailToolDescriptor() map[string]any {
	return map[string]any{
		"name":        identitySendEmailToolName,
		"description": "Send an email using the agent identity configured by the workspace.",
		"inputSchema": map[string]any{
			"type":     "object",
			"required": []string{"recipient", "subject", "body"},
			"properties": map[string]any{
				"recipient": map[string]any{"type": "string", "maxLength": 320},
				"subject":   map[string]any{"type": "string", "maxLength": 256},
				"body":      map[string]any{"type": "string", "maxLength": 65536},
			},
		},
	}
}

func (s *identityActionsMCPServer) handleCall(w http.ResponseWriter, r *http.Request, request pluginHookMCPRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil || params.Name != identitySendEmailToolName {
		writePluginHookMCPError(w, request.ID, -32602, "unknown or invalid identity action")
		return
	}
	var input struct {
		Recipient string `json:"recipient"`
		Subject   string `json:"subject"`
		Body      string `json:"body"`
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(params.Arguments), identityActionsMCPMaxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Recipient == "" || input.Subject == "" || input.Body == "" {
		writePluginHookMCPError(w, request.ID, -32602, "invalid email parameters")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writePluginHookMCPError(w, request.ID, -32602, "invalid email parameters")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), identityActionsMCPCallTimeout)
	defer cancel()
	if err := s.sendEmail(ctx, s.taskID, input.Recipient, input.Subject, input.Body); err != nil {
		if s.logger != nil {
			s.logger.Info("identity email tool call failed", "task_id", s.taskID, "error", err)
		}
		writePluginHookMCPResult(w, request.ID, map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
		})
		return
	}
	writePluginHookMCPResult(w, request.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "Email sent."}},
	})
}
