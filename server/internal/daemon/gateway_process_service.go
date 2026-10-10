package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// gatewayProcessService owns the built-in MCP listener and route table inside the
// gateway process. It holds no task business context: every registered route is
// a forwarder back to a control-owned callback, so the agent's MCP call still
// executes its handler in control while only the listener and route map live
// here. This is the F2 move-out: the process boundary is the listener, not the
// handler.
type gatewayProcessService struct {
	bootstrap runtimeproc.Bootstrap
	broker    *builtinMCPBroker

	mu         sync.RWMutex
	routes     int
	targets    map[string]string // public path -> control callback URL
	unregister map[string]func()
}

// RunGatewayService starts the gateway role and owns its exclusive listener until
// a durable stopped receipt. It runs only when a control parent opted in; the
// legacy in-process broker is the default and is never affected here.
func RunGatewayService(ctx context.Context, b runtimeproc.Bootstrap) error {
	if b.Identity.Scope.Service != "gateway" {
		return errors.New("unsupported gateway role")
	}
	if err := runtimeproc.PrepareRoot(b.Root); err != nil {
		return err
	}
	lock, err := lockMirrorProcessDomain(filepath.Join(b.Root, "gateway-manager.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	s := &gatewayProcessService{
		bootstrap:  b,
		targets:    make(map[string]string),
		unregister: make(map[string]func()),
	}
	service, err := runtimeproc.NewService(runtimeproc.Config{
		Bootstrap:        b,
		Capabilities:     []string{"gateway.register", "gateway.revoke"},
		ReadCapabilities: []string{"gateway.status"},
		Handler:          s.mutate,
		ReadHandler:      s.read,
		Ready:            s.ready,
		Shutdown:         s.shutdown,
	})
	if err != nil {
		return err
	}
	return service.Serve(ctx)
}

// ready starts the listener and reports the gateway live once it can accept a
// forward. A failed listen leaves the service suspect so the parent never routes
// a task to a gateway that is not actually listening.
func (s *gatewayProcessService) ready(ctx context.Context) error {
	broker, err := startBuiltinMCPBroker(ctx)
	if err != nil {
		return err
	}
	s.broker = broker
	s.mu.Lock()
	s.routes = 0
	s.mu.Unlock()
	slog.Default().Info("gateway listener ready", "gateway", s.bootstrap.Identity.InstanceID)
	return nil
}

type gatewayRegister struct {
	Path        string `json:"path"`
	CallbackURL string `json:"callback_url"`
}

type gatewayRegisterResult struct {
	Endpoint string `json:"endpoint"`
}

type gatewayRevoke struct {
	Path string `json:"path"`
}

// mutate registers and revokes forward routes. register parks a forwarder in the
// broker and returns the public endpoint the agent calls; revoke removes only the
// current registration so a stale revoke cannot delete a re-registered route.
func (s *gatewayProcessService) mutate(ctx context.Context, r runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	switch r.Operation {
	case "gateway.register":
		var in gatewayRegister
		if json.Unmarshal(r.Payload, &in) != nil {
			return nil, &runtimeproc.Error{Code: "malformed", Message: "invalid gateway register"}
		}
		if in.Path == "" || in.CallbackURL == "" {
			return nil, &runtimeproc.Error{Code: "malformed", Message: "missing gateway route target"}
		}
		if _, err := url.ParseRequestURI(in.CallbackURL); err != nil {
			return nil, &runtimeproc.Error{Code: "malformed", Message: "invalid gateway callback url"}
		}
		endpoint, unregister := s.broker.register(in.Path, gatewayForwarder{callbackURL: in.CallbackURL})
		if endpoint == "" {
			return nil, &runtimeproc.Error{Code: "gateway_register", Message: "gateway listener is not accepting routes"}
		}
		s.mu.Lock()
		s.targets[in.Path] = in.CallbackURL
		s.unregister[in.Path] = unregister
		s.routes++
		s.mu.Unlock()
		return marshalRaw(gatewayRegisterResult{Endpoint: endpoint}), nil
	case "gateway.revoke":
		var in gatewayRevoke
		if json.Unmarshal(r.Payload, &in) != nil || in.Path == "" {
			return nil, &runtimeproc.Error{Code: "malformed", Message: "invalid gateway revoke"}
		}
		s.mu.Lock()
		unregister, ok := s.unregister[in.Path]
		if ok {
			delete(s.unregister, in.Path)
			delete(s.targets, in.Path)
			s.routes--
		}
		s.mu.Unlock()
		if ok {
			unregister()
		}
	default:
		return nil, &runtimeproc.Error{Code: "unknown_operation", Message: "unknown gateway operation"}
	}
	return json.RawMessage(`{}`), nil
}

func (s *gatewayProcessService) read(ctx context.Context, r runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	s.mu.RLock()
	count := s.routes
	s.mu.RUnlock()
	state := "ready"
	if s.broker == nil || !s.broker.ready() {
		state = "suspect"
	}
	return marshalRaw(map[string]any{
		"instance_id": s.bootstrap.Identity.InstanceID,
		"pid":         os.Getpid(),
		"state":       state,
		"routes":      count,
	}), nil
}

func (s *gatewayProcessService) shutdown(ctx context.Context) error {
	if s.broker != nil {
		s.broker.close()
	}
	s.mu.Lock()
	s.routes = 0
	s.targets = map[string]string{}
	s.unregister = map[string]func(){}
	s.mu.Unlock()
	return nil
}

// gatewayForwarder forwards an agent request to the control-owned callback. The
// callback URL is the security boundary (loopback plus an unpredictable per-route
// token); the agent's public path is discarded so a route cannot be replayed to
// another callback path.
type gatewayForwarder struct {
	callbackURL string
}

func (f gatewayForwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(f.callbackURL)
	if err != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" {
		http.Error(w, "gateway route target invalid", http.StatusServiceUnavailable)
		return
	}
	upstream := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = target.Path
			req.URL.RawPath = target.RawPath
			req.URL.RawQuery = ""
			req.Host = target.Host
		},
		Transport:     gatewayForwardTransport,
		FlushInterval: 50 * time.Millisecond,
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(rw, "gateway forward failed", http.StatusBadGateway)
		},
	}
	upstream.ServeHTTP(w, r)
}

// gatewayForwardTransport is the loopback transport for the forward proxy. It
// disables proxies and keep-alives so a single agent request is one request.
var gatewayForwardTransport = &http.Transport{
	Proxy:                  nil,
	DisableKeepAlives:      true,
	MaxResponseHeaderBytes: 8192,
	DialContext:            (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
}

// validateGatewayForwardTarget rejects any callback that is not a plain loopback
// http origin. It is kept for callers that validate before forwarding.
func validateGatewayForwardTarget(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Fragment != "" {
		return errors.New("callback must be a loopback http origin")
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return errors.New("callback must include a port")
	}
	if strings.ContainsAny(port, "\x00\r\n ") {
		return errors.New("invalid callback port")
	}
	return nil
}
