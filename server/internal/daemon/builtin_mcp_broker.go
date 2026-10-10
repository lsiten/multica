package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// builtinMCPBroker is the daemon-owned transport for built-in MCP services.
// The listener is long-lived, while routes are short-lived task contexts. A
// task route therefore cannot outlive its task or be reused by another task.
type builtinMCPBroker struct {
	listener net.Listener
	server   *http.Server
	baseURL  string

	mu     sync.RWMutex
	routes map[string]http.Handler
	// genByPath is the current registration generation per path. A route is
	// re-registered with a fresh generation, and a revoke is exact: it removes a
	// route only if it is still the current generation for that path. This is
	// the "exact registration-generation revoke" the F2 gateway listener
	// contract needs, so a stale revoke (an older control's closure) cannot
	// delete a route a newer control re-registered on the same path.
	genByPath map[string]uint64
	// generation is the monotonic source of per-path generations.
	generation uint64
	closed     bool
}

func startBuiltinMCPBroker(ctx context.Context) (*builtinMCPBroker, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for built-in MCP broker: %w", err)
	}
	broker := &builtinMCPBroker{
		listener:  listener,
		baseURL:   "http://" + listener.Addr().String(),
		routes:    make(map[string]http.Handler),
		genByPath: make(map[string]uint64),
	}
	broker.server = &http.Server{
		Handler:           broker,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() {
		if serveErr := broker.server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			// The owning daemon reports the loss through health once its broker is
			// closed; individual task handlers must not panic on Serve errors.
		}
	}()
	return broker, nil
}

func (b *builtinMCPBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.RLock()
	handler := b.routes[r.URL.Path]
	b.mu.RUnlock()
	if handler == nil {
		http.NotFound(w, r)
		return
	}
	handler.ServeHTTP(w, r)
}

func (b *builtinMCPBroker) register(path string, handler http.Handler) (string, func()) {
	path = "/" + strings.TrimPrefix(path, "/")
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return "", func() {}
	}
	// A fresh generation per registration: re-registering the same path bumps
	// the generation, so a revoke from an older registration becomes stale.
	b.generation++
	generation := b.generation
	b.routes[path] = handler
	b.genByPath[path] = generation
	b.mu.Unlock()
	var once sync.Once
	return b.baseURL + path, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			// Exact revoke: only remove the route if this registration is still
			// the current generation for the path. A stale revoke (from a
			// superseded registration) leaves the newer route in place.
			if b.genByPath[path] == generation {
				delete(b.routes, path)
			}
		})
	}
}

func (b *builtinMCPBroker) close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.routes = map[string]http.Handler{}
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if b.server != nil {
		_ = b.server.Shutdown(ctx)
	}
	if b.listener != nil {
		_ = b.listener.Close()
	}
}

func (b *builtinMCPBroker) ready() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return !b.closed && b.listener != nil && b.server != nil
}

// mcpBroker is the seam the daemon hot path uses to reach the built-in MCP
// listener. The legacy owner is the in-process *builtinMCPBroker; an opted-in
// daemon routes the same seam through a gateway process (gatewayProcessClient)
// whose listener and route table live in a separate process. Both owners keep
// the exact register/ready/close contract so task consumers are unchanged.
type mcpBroker interface {
	register(path string, handler http.Handler) (string, func())
	ready() bool
	close()
}
