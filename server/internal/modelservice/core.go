package modelservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Execution identifies the exact server task a model lease is bound to. It is
// comparable: a grant or lease never crosses an execution boundary.
type Execution struct {
	WorkspaceID  string `json:"workspace_id"`
	RuntimeID    string `json:"runtime_id"`
	TaskID       string `json:"task_id"`
	DispatchedAt string `json:"dispatched_at"`
}

// validate rejects an identity that cannot be a real dispatched task. It never
// fabricates fields: an empty execution is incomplete, not a legacy fallback.
func (e Execution) validate() error {
	if e.WorkspaceID == "" || e.RuntimeID == "" || e.TaskID == "" || e.DispatchedAt == "" {
		return errors.New("model execution identity is incomplete")
	}
	return nil
}

// grant is a server-issued, lease-scoped inference credential. Every field is
// bound to the owning instance and execution so a client can verify it locally.
type grant struct {
	LeaseID    string    `json:"lease_id"`
	InstanceID string    `json:"instance_id"`
	Endpoint   string    `json:"endpoint"`
	Token      string    `json:"token"`
	Device     string    `json:"device"`
	Deadline   time.Time `json:"deadline"`
	Execution  Execution `json:"execution"`
	// ExecutionIdentity is the optional server-issued execution identity the
	// lease is bound to. It is never populated for a pre-existing run and is
	// never an invented epoch; the existing Execution remains the
	// workspace/runtime/task/dispatched identity, distinct from it.
	ExecutionIdentity *protocol.ExecutionIdentity `json:"execution_identity,omitempty"`
	Selection         jevmodels.Selection         `json:"selection"`
}

// modelRequest is the IPC payload for a single model operation.
type modelRequest struct {
	ModelID   string    `json:"model_id"`
	Revision  string    `json:"revision"`
	Device    string    `json:"device"`
	JobID     string    `json:"job_id"`
	LeaseID   string    `json:"lease_id"`
	Execution Execution `json:"execution"`
}

// ModelLease is a resolved jevmodels lease held by the service. Release is the
// only way to return it; it is idempotent and may be called at most meaningfully
// once by the service's shutdown path.
type ModelLease struct {
	Endpoint string
	Token    string
	Device   string
	Release  func()
}

// Lease is a client-side, task-scoped inference endpoint. Its release closure
// either performs the IPC release (Client) or the in-process release (Local).
type Lease struct {
	Endpoint string
	Token    string
	Device   string
	// ExecutionIdentity is the optional server-issued execution identity the
	// lease carries. It is nil for every pre-existing lease and is never an
	// invented epoch: the daemon never fabricates it, so it stays nil unless
	// the server issued a real one.
	ExecutionIdentity *protocol.ExecutionIdentity
	once              sync.Once
	release           func(context.Context) error
	result            error
}

// Release confirms the lease with the owning owner exactly once. A nil closure
// is a no-op so a half-constructed lease cannot panic a shutdown, and a second
// release (for example a shutdown racing an explicit release) returns the first
// result instead of re-sending a release the owner has already forgotten.
func (l *Lease) Release(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.release != nil {
			l.result = l.release(ctx)
		}
	})
	return l.result
}

// Manager adapts a jevmodels manager to the service's domain operations. The
// variadic signatures mirror jevmodels so realManager can embed *jevmodels.Manager.
type Manager interface {
	Catalog() []jevmodels.Model
	Status(id string, revisions ...string) (jevmodels.Status, error)
	Register(ctx context.Context, id, revision string, client *http.Client) (jevmodels.Model, error)
	Acquire(ctx context.Context, s jevmodels.Selection) (ModelLease, error)
	StartInstall(ctx context.Context, id string, revisions ...string) (<-chan error, error)
	CancelInstall(id string, revisions ...string) error
	Stop(id string, revisions ...string) error
	Remove(id string, revisions ...string) error
	Close() error
	CloseWithReceipt(receipt func() error) error
}

// realManager wraps *jevmodels.Manager, adapting Acquire to a value ModelLease
// so the service can hold the lease without a pointer into the manager's cache.
type realManager struct {
	*jevmodels.Manager
}

func (r realManager) Acquire(ctx context.Context, s jevmodels.Selection) (ModelLease, error) {
	lease, err := r.Manager.Acquire(ctx, s)
	if err != nil {
		return ModelLease{}, err
	}
	return ModelLease{
		Endpoint: lease.Endpoint,
		Token:    lease.Token,
		Device:   lease.Device,
		Release:  lease.Release,
	}, nil
}

// Backend is the daemon-facing model control surface. *Client (IPC) and *Local
// (in-process) both implement it.
type Backend interface {
	Catalog(ctx context.Context) ([]jevmodels.Model, error)
	Status(ctx context.Context, id, revision string) (jevmodels.Status, error)
	Register(ctx context.Context, id, revision string) (jevmodels.Model, error)
	StartInstall(ctx context.Context, id, revision string) error
	CancelInstall(ctx context.Context, id, revision string) error
	Acquire(ctx context.Context, s jevmodels.Selection, e Execution) (*Lease, error)
	Close() error
}

// Local serves the jevmodels manager in-process, without IPC. It is used only
// when the ai process service is not enabled; the daemon still sees a Backend.
type Local struct {
	Manager *jevmodels.Manager
}

func (l *Local) Catalog(ctx context.Context) ([]jevmodels.Model, error) {
	return l.Manager.Catalog(), nil
}

func (l *Local) Status(ctx context.Context, id, revision string) (jevmodels.Status, error) {
	return l.Manager.Status(id, revision)
}

func (l *Local) Register(ctx context.Context, id, revision string) (jevmodels.Model, error) {
	return l.Manager.Register(ctx, id, revision, nil)
}

func (l *Local) StartInstall(ctx context.Context, id, revision string) error {
	result, err := l.Manager.StartInstall(ctx, id, revision)
	if err != nil {
		return err
	}
	go func() { <-result }()
	return nil
}

func (l *Local) CancelInstall(ctx context.Context, id, revision string) error {
	return l.Manager.CancelInstall(id, revision)
}

func (l *Local) Stop(ctx context.Context, id, revision string) error {
	return l.Manager.Stop(id, revision)
}

func (l *Local) Remove(ctx context.Context, id, revision string) error {
	return l.Manager.Remove(id, revision)
}

func (l *Local) Acquire(ctx context.Context, s jevmodels.Selection, e Execution) (*Lease, error) {
	if err := e.validate(); err != nil {
		return nil, err
	}
	lease, err := l.Manager.Acquire(ctx, s)
	if err != nil {
		return nil, err
	}
	wrapped := &Lease{Endpoint: lease.Endpoint, Token: lease.Token, Device: lease.Device}
	wrapped.release = func(context.Context) error {
		lease.Release()
		return nil
	}
	return wrapped, nil
}

func (l *Local) Close() error {
	return l.Manager.Close()
}

// encode marshals a domain result for a runtimeproc receipt.
func encode(v any) (json.RawMessage, *runtimeproc.Error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, problem(err)
	}
	return raw, nil
}

// problem maps a domain error to a safe, machine-readable transport error. The
// code is stable so decodeProblem can reconstruct the exact domain error and
// let callers match it with errors.Is.
func problem(err error) *runtimeproc.Error {
	if err == nil {
		return nil
	}
	return &runtimeproc.Error{Code: domainCode(err), Message: err.Error()}
}

// decodeProblem reconstructs a domain error from a transport error. Unknown
// codes become a fresh *runtimeproc.Error so a caller can still inspect it.
func decodeProblem(p *runtimeproc.Error) error {
	if p == nil {
		return nil
	}
	switch p.Code {
	case "model_busy":
		return jevmodels.ErrBusy
	case "model_closed":
		return jevmodels.ErrClosed
	case "model_unknown":
		return jevmodels.ErrUnknownModel
	case "model_not_installed":
		return jevmodels.ErrNotInstalled
	case "canceled":
		return context.Canceled
	case "deadline_exceeded":
		return context.DeadlineExceeded
	default:
		return &runtimeproc.Error{Code: p.Code, Message: p.Message}
	}
}

func domainCode(err error) string {
	switch {
	case errors.Is(err, jevmodels.ErrBusy):
		return "model_busy"
	case errors.Is(err, jevmodels.ErrClosed):
		return "model_closed"
	case errors.Is(err, jevmodels.ErrUnknownModel):
		return "model_unknown"
	case errors.Is(err, jevmodels.ErrNotInstalled):
		return "model_not_installed"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "domain_error"
	}
}
