package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

type workerResultStatus struct {
	ExecutionID string                               `json:"execution_id"`
	Accepted    bool                                 `json:"accepted"`
	Pending     bool                                 `json:"pending"`
	Result      *workerRunResult                     `json:"result,omitempty"`
	Artifact    *runtimeproc.ReviewArtifactReference `json:"artifact,omitempty"`
}

func (s *workerProcessService) startProviderRun(client *ExecutionClient, run *workerContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workDone != nil {
		return
	}
	lifetime := s.lifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	ctx, cancel := context.WithCancel(lifetime)
	s.workCancel, s.workDone = cancel, make(chan struct{})
	s.state = "running"
	execute := s.executeProvider
	if execute == nil {
		execute = s.executeProviderRun
	}
	go func() {
		defer close(s.workDone)
		defer cancel()
		result := execute(ctx, client, run)
		raw, err := json.Marshal(result)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.workError = err
			return
		}
		s.workPendingResult, s.workPendingRaw = &result, raw
		s.persistWorkerResultLocked()
	}()
}

func (s *workerProcessService) persistWorkerResultLocked() {
	requestID := "worker:" + s.identity.ExecutionID
	raw := s.workPendingRaw
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	err := runtimeproc.PrepareReviewArtifactNamespace(s.bootstrap.Root)
	namespace, name := runtimeproc.ReviewArtifactNamespace(s.bootstrap.Root), runtimeproc.ReviewArtifactName(requestID)
	if err == nil {
		err = runtimeproc.PublishReviewArtifact(namespace, name, raw)
	}
	if err != nil {
		// A prior rename may have succeeded before its directory sync failed.
		if _, readErr := runtimeproc.ReadReviewArtifact(namespace, name, digest, int64(len(raw))); readErr == nil {
			err = runtimeproc.SyncPrivateArtifactDirectory(namespace)
		}
	}
	if err != nil {
		_ = runtimeproc.PersistReviewArtifactPending(s.bootstrap.Root, requestID, raw)
		s.workError = err
		s.workPersistFailures++
		s.workPersistRetryAt = time.Now().Add(reportRetryDelay(s.workPersistFailures, err))
		return
	}
	s.state, s.workResult, s.workError = "finished", s.workPendingResult, nil
	if len(raw) > 256<<10 {
		s.workArtifact = &runtimeproc.ReviewArtifactReference{RequestID: requestID, Hash: digest, Length: int64(len(raw))}
	}
	_ = runtimeproc.RemoveReviewArtifactPending(s.bootstrap.Root, requestID)
	s.workPendingRaw, s.workPendingResult = nil, nil
}

func (s *workerProcessService) stopProviderRun(ctx context.Context) error {
	s.mu.Lock()
	cancel, done := s.workCancel, s.workDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (c *workerProcessClient) readResult(ctx context.Context) (workerResultStatus, error) {
	var result workerResultStatus
	request, err := c.transport.Request("worker.result", runtimeproc.Fence{}, nil)
	if err != nil {
		return result, err
	}
	response, err := c.transport.Call(ctx, request)
	if err != nil {
		return result, err
	}
	if response.Receipt == nil || response.Receipt.State != "read" {
		return result, errors.New("worker result receipt is invalid")
	}
	if err := json.Unmarshal(response.Receipt.Result, &result); err != nil {
		return result, err
	}
	if result.ExecutionID != c.executionIdentity.ExecutionID {
		return result, errors.New("worker result belongs to another execution")
	}
	if result.Artifact != nil {
		expected := "worker:" + c.executionIdentity.ExecutionID
		if result.Artifact.RequestID != expected {
			return result, errors.New("worker result artifact identity changed")
		}
		raw, err := runtimeproc.ReadReviewArtifact(runtimeproc.ReviewArtifactNamespace(c.bootstrap.Root), runtimeproc.ReviewArtifactName(expected), result.Artifact.Hash, result.Artifact.Length)
		if err != nil {
			return result, err
		}
		if err := json.Unmarshal(raw, &result.Result); err != nil {
			return result, err
		}
	}
	return result, nil
}

var errWorkerOutcomeUncertain = errors.New("worker execution outcome remains uncertain")

func waitWorkerResult(ctx context.Context, read func(context.Context) (workerResultStatus, error)) (*workerRunResult, error) {
	for {
		status, err := read(ctx)
		if err == nil && status.Result != nil {
			return status.Result, nil
		}
		if err != nil && workerResultFatalError(err) {
			return nil, errors.Join(errWorkerOutcomeUncertain, err)
		}
		if ctx.Err() != nil {
			return nil, errors.Join(errWorkerOutcomeUncertain, ctx.Err(), err)
		}
		if err := sleepWithContext(ctx, 250*time.Millisecond); err != nil {
			return nil, errors.Join(errWorkerOutcomeUncertain, err)
		}
	}
}

func workerResultFatalError(err error) bool {
	var runtimeErr *runtimeproc.Error
	if errors.As(err, &runtimeErr) {
		return slices.Contains([]string{"result_unavailable", "malformed", "identity_mismatch", "invalid"}, runtimeErr.Code)
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "artifact") || strings.Contains(message, "belongs to another execution") || strings.Contains(message, "invalid worker result") || strings.Contains(message, "length mismatch")
}
