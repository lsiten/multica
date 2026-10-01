package daemon

import (
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

	"github.com/multica-ai/multica/server/internal/communications"
)

const (
	phoneActionsMCPName            = "multica-identity-phone"
	phoneActionsMCPProtocolVersion = "2024-11-05"
	phoneActionsMCPMaxRequestBytes = 80 << 10
	phoneActionsMCPCallTimeout     = 90 * time.Second
	phoneStartCallToolName         = "multica_identity_start_call"
	phoneGetCallToolName           = "multica_identity_get_call"
	phoneCancelCallToolName        = "multica_identity_cancel_call"
	phoneTranscriptsToolName       = "multica_identity_list_transcriptions"
)

type phoneActionsMCPSet struct {
	server   *http.Server
	listener net.Listener
	once     sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
	cleanup  func()
}

func (set *phoneActionsMCPSet) Close() {
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
		if set.cleanup != nil {
			set.cleanup()
		}
	})
}

type phoneActionsMCPServer struct {
	taskID   string
	path     string
	provider communications.CallProvider
	logger   *slog.Logger
	mu       sync.Mutex
	callSIDs map[string]struct{}
}

func startTaskPhoneActionsMCP(ctx context.Context, task Task, allowPhone bool, logger *slog.Logger) (json.RawMessage, *phoneActionsMCPSet, error) {
	return startTaskPhoneActionsMCPAt(ctx, task, allowPhone, logger, "127.0.0.1", "", "")
}

func startTaskPhoneActionsMCPAt(lifetimeCtx context.Context, task Task, allowPhone bool, logger *slog.Logger, listenHost, advertisedHost, idempotencyPath string) (json.RawMessage, *phoneActionsMCPSet, error) {
	if !allowPhone || task.Agent == nil || task.Agent.Identity == nil || task.Agent.Identity.Phone == "" {
		return nil, nil, nil
	}
	cfg := communications.ConfigFromEnvValues(task.Agent.CustomEnv)
	if task.Agent.Identity.Phone != cfg.FromNumber {
		if logger != nil {
			logger.Warn("identity phone action disabled because provider caller id does not match the agent identity", "task_id", task.ID)
		}
		return nil, nil, nil
	}
	var store communications.IdempotencyStore
	var err error
	if idempotencyPath != "" {
		store, err = communications.NewFileIdempotencyStore(idempotencyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("create phone idempotency store: %w", err)
		}
	}
	var provider communications.CallProvider
	if cfg.Provider == "http" || cfg.Provider == "domestic" {
		provider, err = communications.NewHTTPPhoneClient(cfg, nil, store)
	} else {
		provider, err = communications.NewTwilioClient(cfg, nil, store)
	}
	if err != nil {
		if logger != nil {
			logger.Warn("identity phone action unavailable: configured host phone provider is not ready", "task_id", task.ID, "provider", cfg.Provider)
		}
		return nil, nil, nil
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(listenHost, "0"))
	if err != nil {
		return nil, nil, fmt.Errorf("listen for identity phone MCP server: %w", err)
	}
	token, err := randomBrokerToken()
	if err != nil {
		_ = listener.Close()
		return nil, nil, fmt.Errorf("create identity phone MCP token: %w", err)
	}
	handler := &phoneActionsMCPServer{taskID: task.ID, path: "/" + token, provider: provider, logger: logger, callSIDs: make(map[string]struct{})}
	serviceCtx, cancelService := context.WithCancel(lifetimeCtx)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return serviceCtx }}
	set := &phoneActionsMCPSet{server: server, listener: listener, cancel: cancelService, done: make(chan struct{}), cleanup: handler.cancelOwnedCalls}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && logger != nil {
			logger.Warn("identity phone MCP server stopped unexpectedly", "task_id", task.ID, "error", serveErr)
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
		phoneActionsMCPName: map[string]any{
			"type": "http",
			"url":  "http://" + net.JoinHostPort(host, fmt.Sprintf("%d", port)) + handler.path,
		},
	}})
	if err != nil {
		set.Close()
		return nil, nil, fmt.Errorf("marshal identity phone MCP config: %w", err)
	}
	return config, set, nil
}

func (s *phoneActionsMCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.path || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, phoneActionsMCPMaxRequestBytes+1))
	if err != nil {
		writePluginHookMCPError(w, nil, -32700, "could not read the request")
		return
	}
	if len(body) > phoneActionsMCPMaxRequestBytes {
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
			"protocolVersion": phoneActionsMCPProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": phoneActionsMCPName, "version": "1"},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writePluginHookMCPResult(w, request.ID, map[string]any{"tools": phoneActionToolDescriptors()})
	case "tools/call":
		s.handleCall(w, r, request)
	default:
		writePluginHookMCPError(w, request.ID, -32601, "unsupported method "+request.Method)
	}
}
