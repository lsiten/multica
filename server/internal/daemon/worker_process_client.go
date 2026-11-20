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
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// workerProcessClient is the control-side owner of ONE per-execution task
// worker. Unlike the long-lived service owners (gateway/application/
// environment), a worker owns a single server-claimed execution: control
// launches the child, binds the exact execution identity + scoped grant + the
// execution context, drives one genuine provider run, then stops the child.
// The worker is the only production execution owner; the control parent owns
// orchestration and reconciliation, never a second provider runner.
//
// This is the control-parent half of the F3 move-out. It owns only the private
// runtime channel and the launch/bind/run lifecycle. The worker itself owns the
// provider run, the completion gate, the report, and the launch-authorization
// decision (DecideWorkerLaunch), so a control-parent bind can never fabricate a
// launch or a result.
type workerProcessClient struct {
	transport         workerRuntimeTransport
	bootstrap         runtimeproc.Bootstrap
	executionIdentity protocol.ExecutionIdentity
	wait              func(ctx context.Context) error
	// instanceID is this worker's canonical 32-char hex instance id; the
	// control parent binds it server-side before the bind so the worker
	// receives the exact execution identity it is bound to.
	instanceID string
	pid        int

	gate      chan struct{}
	uncertain error
	closeOnce sync.Once
	closeErr  error
	// reclaim, when set, runs once after the worker is closed so the daemon can
	// drop this worker from its live registry. It is set by registerWorkerProcess.
	reclaim func()
}

// workerRuntimeTransport is the private runtime channel the control parent uses
// to drive a worker. It is the subset of *runtimeproc.Client the worker client
// needs; a test can supply a fake without launching a real child, while the
// real launch path hands over a live *runtimeproc.Client.
type workerRuntimeTransport interface {
	Health(ctx context.Context) (runtimeproc.Status, error)
	Request(operation string, fence runtimeproc.Fence, payload json.RawMessage) (runtimeproc.Request, error)
	Call(ctx context.Context, request runtimeproc.Request) (runtimeproc.Response, error)
}

// workerRunInput is the exact facts control delivers at bind. It is the closed
// set the worker applies its launch-authorization contract to; it carries no
// secret beyond the scoped grant (the worker's own credential) and no *Daemon
// reference.
// workerRunInput is the control parent's projection of the worker bind request.
// Its JSON tags match workerBindRequest exactly so the marshaled payload unmarshals
// into the worker's closed bind contract without any key drift.
type workerRunInput struct {
	Grant               protocol.ExecutionGrantResponse `json:"grant"`
	CallbackURL         string                          `json:"callback_url"`
	WorkerIDMatchesBind bool                            `json:"worker_id_matches_bind"`
	LaunchAuthorized    bool                            `json:"launch_authorized"`
	Uncertain           bool                            `json:"uncertain"`
	Context             *workerContext                  `json:"context,omitempty"`
}

// ensureWorkerProcess starts a per-execution worker child and returns the
// control-side client. It mirrors the sibling owners (a scoped identity, a
// private per-execution root, executable pinning, an explicit environment
// allowlist, and a readiness handshake) but for a single execution rather than a
// long-lived service. A missing executable fails closed: control never runs the
// task in-process to recover an uncertain launch.
func (d *Daemon) ensureWorkerProcess(ctx context.Context, execID string) (*workerProcessClient, error) {
	if d.cfg.NativeHostExecutable == "" {
		return nil, errors.New("worker executable is not configured; worker launch disabled")
	}
	accountID, accountErr := d.verifiedAccount(ctx)
	if accountErr != nil {
		return nil, accountErr
	}
	account := struct{ ID string }{ID: accountID}
	if account.ID == "" {
		return nil, errors.New("worker owner identity missing")
	}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{
		Backend:  d.cfg.ServerBaseURL,
		Account:  account.ID,
		Profile:  d.cfg.Profile,
		DaemonID: d.cfg.DaemonID,
		Service:  "worker",
	}, d.cfg.NativeHostBuild)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(d.cfg.NativeVscreenPreferencesPath))
	if err != nil {
		return nil, err
	}
	root := filepath.Join(parent, "worker-service", execID)
	if !filepath.IsAbs(root) {
		return nil, errors.New("worker profile root must be absolute")
	}
	if err = runtimeproc.PrepareRoot(root); err != nil {
		return nil, err
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
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
		Bootstrap:      bootstrap,
		StartupTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &workerProcessClient{
		bootstrap:  bootstrap,
		transport:  process.Client,
		wait:       process.Wait,
		instanceID: identity.InstanceID,
		pid:        process.PID(),
		gate:       make(chan struct{}, 1),
	}, nil
}

// bind delivers the exact execution facts the worker applies its closed
// launch-authorization contract to and returns the worker's recorded decision.
// A transport failure is uncertain (the bind may have completed); a domain-level
// result (launchable or a refusal reason) is the worker's recorded decision and
// is returned directly. The control parent must not relaunch on an uncertain
// bind or a refused bind.
func (c *workerProcessClient) bind(ctx context.Context, in workerRunInput) (*workerStatus, error) {
	var status workerStatus
	if err := c.mutation(ctx, "worker.bind", in, &status); err != nil {
		return nil, err
	}
	c.executionIdentity = in.Grant.Identity
	return &status, nil
}

// run drives the single genuine provider run and returns the exact result the
// worker produced (never a fabricated success). A domain-level refusal (not
// launchable, no context, launch failed) is the worker's recorded decision and
// is returned directly; only a transport failure is uncertain. The terminal
// result the server settles the execution from is owned by the worker's own
// report (client.Callback), so the control parent never relaunches to recover a
// lost report.
func (c *workerProcessClient) run(ctx context.Context) (*workerRunResult, error) {
	var accepted struct {
		Accepted bool `json:"accepted"`
	}
	if err := c.mutation(ctx, "worker.run", json.RawMessage(`{}`), &accepted); err != nil {
		var domain *runtimeproc.Error
		var consumed *consumedRuntimeResultError
		if errors.As(err, &domain) || errors.As(err, &consumed) || runtimeproc.RejectedBeforeAdmission(err) {
			return nil, err
		}
	}
	return waitWorkerResult(ctx, c.readResult)
}

// mutation submits one bounded worker operation and returns the domain result.
// It is serialized so bind/run keep an exact order, and a transport failure
// becomes an uncertain operation rather than a false close.
func (c *workerProcessClient) mutation(ctx context.Context, operation string, input, out any) error {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	if c.uncertain != nil {
		return c.uncertain
	}
	status, err := c.transport.Health(ctx)
	if err != nil {
		return err
	}
	var payload json.RawMessage
	if input != nil {
		payload = marshalRaw(input)
	}
	request, err := c.transport.Request(operation, status.Fence, payload)
	if err != nil {
		return err
	}
	request.Deadline = time.Now().Add(30 * time.Second)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(request.Deadline) {
		request.Deadline = deadline
	}
	response, err := c.transport.Call(ctx, request)
	if err != nil {
		if runtimeproc.RejectedBeforeAdmission(err) {
			return err
		}
		c.uncertain = fmt.Errorf("worker %s outcome uncertain (request %s): %v", operation, request.RequestID, err)
		return c.uncertain
	}
	if response.Receipt == nil || response.Receipt.State != "completed" {
		c.uncertain = fmt.Errorf("worker %s outcome uncertain (request %s): pending receipt", operation, request.RequestID)
		return c.uncertain
	}
	resultErr := decodeCompletedRuntimeResult(response.Receipt, out)
	ack, ackErr := c.transport.Request("acknowledge", response.Status.Fence, nil)
	if ackErr == nil {
		var acknowledged runtimeproc.Response
		acknowledged, ackErr = c.transport.Call(ctx, ack)
		if ackErr == nil {
			if acknowledged.Receipt == nil || acknowledged.Receipt.State != "completed" {
				ackErr = errors.New("pending acknowledgement receipt")
			} else if acknowledged.Receipt.Error != nil {
				ackErr = acknowledged.Receipt.Error
			}
		}
	}
	if ackErr != nil {
		c.uncertain = fmt.Errorf("worker %s acknowledgement uncertain (request %s): %v", operation, request.RequestID, ackErr)
		return errors.Join(resultErr, c.uncertain)
	}
	return resultErr
}

// ready reports whether the worker is live. A health failure means the owner is
// suspect, not stopped, so readiness fails closed.
func (c *workerProcessClient) ready() bool {
	if c == nil || c.transport == nil {
		return false
	}
	status, err := c.transport.Health(context.Background())
	if err != nil {
		return false
	}
	return status.State == "ready" || status.State == "running"
}

// close stops the per-execution worker and joins it. A lost stop is an
// uncertainty, never a claim that domain descendants stopped.
func (c *workerProcessClient) close() {
	c.closeOnce.Do(func() {
		if c.reclaim != nil {
			defer c.reclaim()
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if c.wait == nil {
			c.closeErr = errors.New("worker has no runtime wait")
			return
		}
		status, err := c.transport.Health(ctx)
		if err == nil {
			request, requestErr := c.transport.Request("stop", status.Fence, nil)
			err = requestErr
			if err == nil {
				request.Deadline = time.Now().Add(55 * time.Second)
				if _, callErr := c.transport.Call(ctx, request); callErr != nil {
					err = callErr
				}
			}
		}
		if err != nil {
			c.closeErr = err
			return
		}
		c.closeErr = c.wait(ctx)
	})
}
