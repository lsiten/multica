package modelservice

import (
	"context"
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

type acquireResult struct {
	ID    string             `json:"id"`
	State string             `json:"state"`
	Grant *grant             `json:"grant,omitempty"`
	Error *runtimeproc.Error `json:"error,omitempty"`
}
type acquisition struct {
	result    acquireResult
	execution Execution
	cancel    context.CancelFunc
	deadline  time.Time
}

func (s *Service) startAcquire(req runtimeproc.Request, input modelRequest) (json.RawMessage, *runtimeproc.Error) {
	if err := input.Execution.validate(); err != nil {
		return nil, problem(err)
	}
	s.mu.Lock()
	full := len(s.leases)+len(s.acquisitions) >= maxLeases
	s.mu.Unlock()
	if full {
		return nil, problem(jevmodels.ErrBusy)
	}
	if err := s.markDescendants(); err != nil {
		return nil, problem(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), req.Deadline)
	job := &acquisition{result: acquireResult{ID: req.RequestID, State: "pending"}, execution: input.Execution, cancel: cancel, deadline: req.Deadline}
	s.mu.Lock()
	s.acquisitions[req.RequestID] = job
	s.mu.Unlock()
	s.acquiring.Add(1)
	go func() {
		defer s.acquiring.Done()
		defer cancel()
		selection := jevmodels.Selection{ModelID: input.ModelID, Revision: input.Revision, Device: input.Device}
		lease, err := s.manager.Acquire(ctx, selection)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err == nil && (ctx.Err() != nil || s.closed || s.acquisitions[req.RequestID] != job) {
			lease.Release()
			err = context.Canceled
		}
		if err != nil {
			job.result.State = "failed"
			job.result.Error = problem(err)
			return
		}
		id, err := randomToken()
		if err != nil {
			lease.Release()
			job.result.State = "failed"
			job.result.Error = problem(err)
			return
		}
		token, err := randomToken()
		if err != nil {
			lease.Release()
			job.result.State = "failed"
			job.result.Error = problem(err)
			return
		}
		grant := grant{LeaseID: id, InstanceID: s.bootstrap.Identity.InstanceID, Endpoint: s.proxyAddress + "/leases/" + id, Token: token, Device: lease.Device, Deadline: time.Now().Add(s.leaseTTL), Execution: input.Execution, Selection: selection}
		s.leases[id] = &leaseState{grant: grant, model: lease}
		job.result.State = "ready"
		job.result.Grant = &grant
	}()
	return encode(acquireResult{ID: req.RequestID, State: "pending"})
}
func (s *Service) acquisitionStatus(input modelRequest) (json.RawMessage, *runtimeproc.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.acquisitions[input.JobID]
	if !ok || job.execution != input.Execution {
		return nil, &runtimeproc.Error{Code: "acquire_lost", Message: "acquire job unavailable"}
	}
	return encode(job.result)
}
func (s *Service) finishAcquire(operation string, input modelRequest) (json.RawMessage, *runtimeproc.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.acquisitions[input.JobID]
	if !ok || job.execution != input.Execution {
		return nil, &runtimeproc.Error{Code: "acquire_lost", Message: "acquire job unavailable"}
	}
	if operation == "model.acquire_ack" {
		if job.result.State != "ready" || job.result.Grant.LeaseID != input.LeaseID {
			return nil, &runtimeproc.Error{Code: "stale_lease", Message: "acquire grant differs"}
		}
	} else {
		job.cancel()
		if job.result.Grant != nil {
			if lease := s.leases[job.result.Grant.LeaseID]; lease != nil {
				lease.revoked = true
				s.releaseIfIdleLocked(job.result.Grant.LeaseID, lease)
			}
		}
	}
	delete(s.acquisitions, input.JobID)
	return encode(struct{}{})
}
