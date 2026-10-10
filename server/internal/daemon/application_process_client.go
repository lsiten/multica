package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type applicationUncertainOperation struct {
	RequestID, Operation string
	Cause                error
}

func (e *applicationUncertainOperation) Error() string {
	return fmt.Sprintf("application %s outcome uncertain (request %s): %v", e.Operation, e.RequestID, e.Cause)
}
func (e *applicationUncertainOperation) Unwrap() error { return e.Cause }

type applicationProcessClient struct {
	daemon       *Daemon
	process      *runtimeproc.Process
	bootstrap    runtimeproc.Bootstrap
	source       *applicationSourceCallback
	gate         chan struct{}
	control      chan struct{}
	uncertain    error
	mu           sync.Mutex
	grants       map[string]protocol.ApplicationServiceGrantResponse
	syncFailed   bool
	bindingReady bool
	closeOnce    sync.Once
	closeErr     error
}

func (d *Daemon) applicationProcessMode() bool {
	return slices.Contains(d.cfg.ProcessServices, "application")
}
func (d *Daemon) ensureApplicationProcess(ctx context.Context) (*applicationProcessClient, error) {
	d.applicationProcessMu.Lock()
	defer d.applicationProcessMu.Unlock()
	if d.applicationProcess != nil {
		return d.applicationProcess, nil
	}
	var account struct {
		ID string `json:"id"`
	}
	if err := d.client.getJSON(ctx, "/api/me", &account); err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, errors.New("application owner identity missing")
	}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: d.cfg.ServerBaseURL, Account: account.ID, Profile: d.cfg.Profile, DaemonID: d.cfg.DaemonID, Service: "application"}, d.cfg.NativeHostBuild)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(filepath.Dir(d.cfg.NativeVscreenPreferencesPath), "application-service")
	if !filepath.IsAbs(root) {
		return nil, errors.New("application profile root must be absolute")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil {
		return nil, err
	}
	root = filepath.Join(parent, filepath.Base(root))
	if err = runtimeproc.PrepareRoot(root); err != nil {
		return nil, err
	}
	if err = d.reconcileApplicationManager(ctx, root, identity.Scope); err != nil {
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
	for _, name := range []string{"PATH", "HOME", "USER", "USERPROFILE", "SYSTEMROOT", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			environment[name] = value
		}
	}
	process, err := runtimeproc.Start(context.Background(), runtimeproc.LaunchConfig{Executable: executable, SHA256: hex.EncodeToString(hash.Sum(nil)), Environment: environment, Bootstrap: b, StartupTimeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	source, err := newApplicationSourceCallback(d, b.Identity.InstanceID, b.Token)
	if err != nil {
		process.Close()
		return nil, err
	}
	client := &applicationProcessClient{daemon: d, process: process, bootstrap: b, source: source, gate: make(chan struct{}, 1), control: make(chan struct{}, 1), grants: map[string]protocol.ApplicationServiceGrantResponse{}}
	d.applicationProcess = client
	return client, nil
}
func (c *applicationProcessClient) mutation(ctx context.Context, operation string, input any) error {
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
	request, err := c.process.Client.Request(operation, status.Fence, marshalRaw(input))
	if err != nil {
		return err
	}
	request.Deadline = time.Now().Add(3 * time.Minute)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(request.Deadline) {
		request.Deadline = deadline
	}
	response, err := c.process.Client.Call(ctx, request)
	if err != nil {
		var reject *runtimeproc.Error
		if errors.As(err, &reject) && slices.Contains([]string{"unauthorized", "identity_mismatch", "stale_fence", "malformed", "unknown_operation", "retired_request", "draining", "stopped", "resource_exhausted", "deadline"}, reject.Code) {
			return err
		}
		c.uncertain = &applicationUncertainOperation{RequestID: request.RequestID, Operation: operation, Cause: err}
		return c.uncertain
	}
	if response.Receipt == nil || response.Receipt.State != "completed" {
		c.uncertain = &applicationUncertainOperation{RequestID: request.RequestID, Operation: operation, Cause: errors.New("pending receipt")}
		return c.uncertain
	}
	domainErr := response.Receipt.Error
	ack, err := c.process.Client.Request("acknowledge", response.Status.Fence, nil)
	if err == nil {
		result, callErr := c.process.Client.Call(ctx, ack)
		if callErr != nil || result.Receipt == nil || result.Receipt.State != "completed" {
			c.uncertain = &applicationUncertainOperation{RequestID: ack.RequestID, Operation: "acknowledge", Cause: callErr}
		}
	} else {
		c.uncertain = err
	}
	// A lost housekeeping ACK cannot turn a consumed durable result into failure.
	if domainErr != nil {
		return domainErr
	}
	return nil
}
func (c *applicationProcessClient) grantSnapshot() map[string]protocol.ApplicationServiceGrantResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := map[string]protocol.ApplicationServiceGrantResponse{}
	for id, g := range c.grants {
		result[id] = g
	}
	return result
}
func (c *applicationProcessClient) storeGrants(grants map[string]protocol.ApplicationServiceGrantResponse) {
	c.mu.Lock()
	c.grants = grants
	c.mu.Unlock()
	c.source.mu.Lock()
	c.source.grants = map[string]protocol.ApplicationServiceGrantResponse{}
	for id, g := range grants {
		c.source.grants[id] = g
	}
	c.source.mu.Unlock()
}
func (c *applicationProcessClient) sync(ctx context.Context) (resultErr error) {
	defer func() { c.mu.Lock(); c.syncFailed = resultErr != nil; c.mu.Unlock() }()
	select {
	case c.control <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.control }()
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	uncertain := c.uncertain
	<-c.gate
	if uncertain != nil {
		return uncertain
	}
	grants := c.grantSnapshot()
	roster := map[string]bool{}
	for _, id := range c.daemon.allRuntimeIDs() {
		if _, ok := c.daemon.applicationServerCapabilities.Load(id); ok {
			roster[id] = true
		}
	}
	changed := false
	for id, grant := range grants {
		if roster[id] {
			continue
		}
		if err := c.mutation(ctx, "application.remove", map[string]string{"runtime_id": id}); err != nil {
			return err
		}
		if err := c.daemon.client.RevokeApplicationServiceGrant(ctx, id, protocol.RevokeApplicationServiceGrantRequest{ServiceInstanceID: grant.ServiceInstanceID, Generation: grant.Generation}); err != nil {
			return err
		}
		delete(grants, id)
		changed = true
	}
	for id := range roster {
		old, exists := grants[id]
		if exists && time.Until(old.ExpiresAt) > 5*time.Minute {
			continue
		}
		state, err := c.daemon.client.ApplicationServiceGrantState(ctx, id)
		if err != nil {
			return err
		}
		if exists && state.ServiceInstanceID != old.ServiceInstanceID {
			return errors.New("application service authority replaced")
		}
		grant, err := c.daemon.client.IssueApplicationServiceGrant(ctx, id, protocol.ApplicationServiceGrantRequest{ServiceInstanceID: c.bootstrap.Identity.InstanceID, ExpectedGeneration: state.Generation, Operations: []string{"sync", "claim", "observe", "result", "lease", "tunnel_control", "tunnel_data"}})
		if err != nil {
			return err
		}
		grants[id] = grant
		changed = true
	}
	c.storeGrants(grants)
	if !changed && c.bindingReady && len(roster) > 0 {
		return c.mutation(ctx, "application.wake", nil)
	}
	list := make([]protocol.ApplicationServiceGrantResponse, 0, len(grants))
	for _, grant := range grants {
		list = append(list, grant)
	}
	canonical, err := filepath.EvalSymlinks(c.daemon.cfg.WorkspacesRoot)
	if err != nil {
		return err
	}
	c.bindingReady = false
	err = c.mutation(ctx, "application.bind", applicationProcessBinding{WorkspacesRoot: canonical, SourceAddress: c.source.address, Grants: list})
	if err == nil {
		c.bindingReady = true
	}
	return err
}
func (c *applicationProcessClient) remove(ctx context.Context, id string) error {
	select {
	case c.control <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.control }()
	grants := c.grantSnapshot()
	grant, ok := grants[id]
	if !ok {
		return nil
	}
	if err := c.mutation(ctx, "application.remove", map[string]string{"runtime_id": id}); err != nil {
		return err
	}
	if err := c.daemon.client.RevokeApplicationServiceGrant(ctx, id, protocol.RevokeApplicationServiceGrantRequest{ServiceInstanceID: grant.ServiceInstanceID, Generation: grant.Generation}); err != nil {
		return err
	}
	delete(grants, id)
	c.storeGrants(grants)
	return nil
}
func (c *applicationProcessClient) inventory(ctx context.Context) (applicationProcessInventory, error) {
	raw, err := c.process.Client.Read(ctx, "application.inventory", nil)
	if err != nil {
		return applicationProcessInventory{Unknown: true}, err
	}
	var inventory applicationProcessInventory
	if json.Unmarshal(raw, &inventory) != nil || inventory.InstanceID != c.bootstrap.Identity.InstanceID {
		return applicationProcessInventory{Unknown: true}, errors.New("invalid application inventory")
	}
	return inventory, nil
}
func (c *applicationProcessClient) close() error {
	c.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		select {
		case c.control <- struct{}{}:
			defer func() { <-c.control }()
		case <-ctx.Done():
			c.closeErr = ctx.Err()
			return
		}
		select {
		case c.gate <- struct{}{}:
			defer func() { <-c.gate }()
		case <-ctx.Done():
			c.closeErr = ctx.Err()
			return
		}

		status, err := c.process.Client.Health(ctx)
		if err == nil {
			request, requestErr := c.process.Client.Request("stop", status.Fence, nil)
			err = requestErr
			if err == nil {
				request.Deadline = time.Now().Add(55 * time.Second)
				response, callErr := c.process.Client.Call(ctx, request)
				err = callErr
				if err == nil && (response.Receipt == nil || response.Receipt.State != "completed" || response.Receipt.Error != nil || response.Status.State != "stopped") {
					err = errors.New("application shutdown remains unconfirmed")
				}
			}
		}
		c.closeErr = err
		if err == nil {
			c.closeErr = c.process.Wait(ctx)
		}
		c.source.close()
		for id, grant := range c.grantSnapshot() {
			c.closeErr = errors.Join(c.closeErr, c.daemon.client.RevokeApplicationServiceGrant(ctx, id, protocol.RevokeApplicationServiceGrantRequest{ServiceInstanceID: grant.ServiceInstanceID, Generation: grant.Generation}))
		}
	})
	return c.closeErr
}
func (d *Daemon) applicationProcessLoop(ctx context.Context, client *applicationProcessClient) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		update, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := client.sync(update)
		cancel()
		if err != nil && ctx.Err() == nil {
			d.logger.Warn("application service reconciliation deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-d.applicationWake:
		}
	}
}
