package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// gatewayProcessClient is the control-side owner of the built-in MCP broker
// seam when a gateway process is opted in. It keeps task handlers in control
// (through gatewayProcessCallback) and moves only the listener and route table
// into the gateway process, so the hot path register/ready/close calls travel
// over the private runtime channel instead of an in-process function call.
type gatewayProcessClient struct {
	daemon    *Daemon
	process   *runtimeproc.Process
	bootstrap runtimeproc.Bootstrap
	callback  *gatewayProcessCallback

	gate      chan struct{}
	uncertain error
	mu        sync.Mutex
	closeOnce sync.Once
	closeErr  error
	// reAdoptClient is the opt-in re-adopted control-side client for the gateway
	// child. It is nil on the default path; when set it overrides process.Client so
	// a child that survived a control restart is supervised over the private
	// runtime channel (the G "retain and re-adopt" cut) instead of being killed.
	reAdoptClient *runtimeproc.Client
}

func (d *Daemon) gatewayProcessMode() bool {
	return slices.Contains(d.cfg.ProcessServices, "gateway")
}

// ensureGatewayProcess starts the gateway child exactly once and returns the
// control-side client. It mirrors the other process owners: a scoped identity,
// a private root, executable pinning, an explicit environment allowlist, and a
// readiness handshake before the client is usable.
func (d *Daemon) ensureGatewayProcess(ctx context.Context) (*gatewayProcessClient, error) {
	d.gatewayProcessMu.Lock()
	defer d.gatewayProcessMu.Unlock()
	if d.gatewayProcess != nil {
		return d.gatewayProcess, nil
	}
	var account struct {
		ID string `json:"id"`
	}
	if err := d.client.getJSON(ctx, "/api/me", &account); err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, errors.New("gateway owner identity missing")
	}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{
		Backend:  d.cfg.ServerBaseURL,
		Account:  account.ID,
		Profile:  d.cfg.Profile,
		DaemonID: d.cfg.DaemonID,
		Service:  "gateway",
	}, d.cfg.NativeHostBuild)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(filepath.Dir(d.cfg.NativeVscreenPreferencesPath), "gateway-service")
	if !filepath.IsAbs(root) {
		return nil, errors.New("gateway profile root must be absolute")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil {
		return nil, err
	}
	root = filepath.Join(parent, filepath.Base(root))
	if err = runtimeproc.PrepareRoot(root); err != nil {
		return nil, err
	}
	b, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		return nil, err
	}
	executable, err := filepath.EvalSymlinks(d.cfg.NativeHostExecutable)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(executable)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		return nil, err
	}
	environment := map[string]string{}
	for _, name := range []string{"PATH", "HOME", "USER", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TMP", "TEMP", "TMPDIR", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			environment[name] = value
		}
	}
	process, err := runtimeproc.Start(context.Background(), runtimeproc.LaunchConfig{
		Executable:     executable,
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		Environment:    environment,
		Bootstrap:      b,
		StartupTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	callback, err := newGatewayProcessCallback()
	if err != nil {
		process.Close()
		return nil, err
	}
	client := &gatewayProcessClient{
		daemon:    d,
		process:   process,
		bootstrap: b,
		callback:  callback,
		gate:      make(chan struct{}, 1),
	}
	d.gatewayProcess = client
	return client, nil
}

// mutation submits one bounded gateway operation and returns the domain result.
// It is serialized so register/revoke keep an exact route-generation order, and
// a transport failure becomes an uncertain operation rather than a false close.
func (c *gatewayProcessClient) mutation(ctx context.Context, operation string, input, out any) error {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	if c.uncertain != nil {
		return c.uncertain
	}
	status, err := c.process.Client.Health(ctx)
	if err != nil {
		return err
	}
	var payload json.RawMessage
	if input != nil {
		payload = marshalRaw(input)
	}
	request, err := c.process.Client.Request(operation, status.Fence, payload)
	if err != nil {
		return err
	}
	request.Deadline = time.Now().Add(30 * time.Second)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(request.Deadline) {
		request.Deadline = deadline
	}
	response, err := c.process.Client.Call(ctx, request)
	if err != nil {
		var reject *runtimeproc.Error
		if errors.As(err, &reject) && slices.Contains([]string{
			"unauthorized", "identity_mismatch", "stale_fence", "malformed",
			"unknown_operation", "retired_request", "draining", "stopped",
			"resource_exhausted", "deadline",
		}, reject.Code) {
			return err
		}
		c.uncertain = fmt.Errorf("gateway %s outcome uncertain (request %s): %v", operation, request.RequestID, err)
		return c.uncertain
	}
	if len(response.Receipt.Result) > 0 {
		if out != nil {
			if err = json.Unmarshal(response.Receipt.Result, out); err != nil {
				return err
			}
		}
	}
	ack, err := c.process.Client.Request("acknowledge", response.Status.Fence, nil)
	if err != nil {
		return err
	}
	if _, err = c.process.Client.Call(ctx, ack); err != nil {
		// The domain mutation already completed durably; a lost acknowledgement
		// is an uncertainty, not a reason to re-run a register/revoke.
		c.uncertain = fmt.Errorf("gateway %s acknowledgement uncertain (request %s): %v", operation, request.RequestID, err)
		return c.uncertain
	}
	return nil
}

// register moves a task route into the gateway process. The control-owned
// handler is parked in the callback server first, then the gateway is told to
// forward its public path to that callback. The returned endpoint is the
// gateway listener URL the agent calls; the unregister closure is exact.
func (c *gatewayProcessClient) register(path string, handler http.Handler) (string, func()) {
	if handler == nil {
		return "", func() {}
	}
	callbackURL, unregisterCallback := c.callback.register(handler)
	if callbackURL == "" {
		return "", func() {}
	}
	type gatewayRegister struct {
		Path        string `json:"path"`
		CallbackURL string `json:"callback_url"`
	}
	type gatewayRegisterResult struct {
		Endpoint string `json:"endpoint"`
	}
	var result gatewayRegisterResult
	if err := c.mutation(context.Background(), "gateway.register", gatewayRegister{Path: path, CallbackURL: callbackURL}, &result); err != nil {
		unregisterCallback()
		return "", func() {}
	}
	var once sync.Once
	unregister := func() {
		once.Do(func() {
			type gatewayRevoke struct {
				Path string `json:"path"`
			}
			_ = c.mutation(context.Background(), "gateway.revoke", gatewayRevoke{Path: path}, nil)
			unregisterCallback()
		})
	}
	return result.Endpoint, unregister
}

// ready reports whether the gateway listener is live. A health failure means
// the owner is suspect, not stopped, so readiness fails closed.
func (c *gatewayProcessClient) ready() bool {
	if c == nil || c.process == nil {
		return false
	}
	status, err := c.process.Client.Health(context.Background())
	if err != nil {
		return false
	}
	return status.State == "ready" || status.State == "running"
}

func (c *gatewayProcessClient) close() {
	c.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := c.process.Client.Health(ctx)
		if err == nil {
			request, requestErr := c.process.Client.Request("stop", status.Fence, nil)
			err = requestErr
			if err == nil {
				request.Deadline = time.Now().Add(55 * time.Second)
				if _, callErr := c.process.Client.Call(ctx, request); callErr != nil {
					err = callErr
				}
			}
		}
		if err != nil {
			c.closeErr = err
			return
		}
		c.closeErr = c.process.Wait(ctx)
		c.callback.close()
	})
}

// reAdopt re-adopts the gateway child after a control restart by reopening the
// child's runtime record (it persists on disk, owned by the child, not the
// control) and authenticating readiness. It is the G "retain and re-adopt"
// substrate: the control does not kill the child, it reconnects to it over the
// private runtime channel. Open accepts a live (ready/starting/draining) child's
// record, so a child that outlived the control is re-adopted, not replaced.
// The re-adopted client supervises the child (register/revoke/stop) in place of
// the process handle, which no longer exists after the control exited. It never
// replaces a child that is not ready (Open fails closed), so a half-dead child
// is never adopted as alive.
func (c *gatewayProcessClient) reAdopt(ctx context.Context) (*runtimeproc.Client, error) {
	client, err := runtimeproc.Open(ctx, c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		return nil, err
	}
	c.reAdoptClient = client
	return client, nil
}
