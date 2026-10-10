package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// gatewayProcessCallback is the control-owned reverse target for the gateway
// process. The gateway owns the built-in MCP listener; control owns the actual
// task handlers. A registered route in the gateway forwards every request back
// to a loopback callback path keyed by an unpredictable token, so a handler
// that reaches the gateway through the hot path is still executed by control.
// Only the listener and route table leave the process; the business context
// (task gate, budget, credentials) stays authoritative here.
type gatewayProcessCallback struct {
	listener net.Listener
	server   *http.Server
	baseURL  string

	mu     sync.Mutex
	routes map[string]http.Handler
}

func newGatewayProcessCallback() (*gatewayProcessCallback, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	c := &gatewayProcessCallback{
		listener: listener,
		baseURL:  "http://" + listener.Addr().String(),
		routes:   make(map[string]http.Handler),
	}
	c.server = &http.Server{
		Handler:           c,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return context.Background() },
	}
	go func() {
		if err := c.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The owning daemon observes the callback loss through gateway health;
			// the callback must never panic a task handler on a serve error.
		}
	}()
	return c, nil
}

// register stores a control-owned handler behind a fresh private path and
// returns the callback URL the gateway should forward to, plus an exact
// unregister that only removes this registration.
func (c *gatewayProcessCallback) register(handler http.Handler) (string, func()) {
	pathToken, err := randomBrokerToken()
	if err != nil {
		return "", func() {}
	}
	c.mu.Lock()
	c.routes[pathToken] = handler
	c.mu.Unlock()
	path := "/cb/" + pathToken
	var once sync.Once
	return c.baseURL + path, func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.routes, pathToken)
			c.mu.Unlock()
		})
	}
}

// ServeHTTP forwards a gateway request to the exact control-owned handler.
// The callback path is the security boundary: each route gets an unpredictable
// token and the server is loopback-only, so a missing or revoked path is refused
// rather than executed.
func (c *gatewayProcessCallback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pathToken := r.URL.Path
	if len(pathToken) < 5 || pathToken[:4] != "/cb/" {
		http.NotFound(w, r)
		return
	}
	pathToken = pathToken[4:]
	c.mu.Lock()
	handler := c.routes[pathToken]
	c.mu.Unlock()
	if handler == nil {
		http.NotFound(w, r)
		return
	}
	handler.ServeHTTP(w, r)
}

func (c *gatewayProcessCallback) close() {
	if c == nil || c.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.server.Shutdown(ctx)
	_ = c.listener.Close()
	c.mu.Lock()
	c.routes = map[string]http.Handler{}
	c.mu.Unlock()
}
