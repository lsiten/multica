package runtimeproc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Service) mutateHTTP(ctx context.Context, w http.ResponseWriter, req Request) {
	var err error
	release, admissionErr := s.admit(ctx, req.Operation)
	if admissionErr != nil {
		s.reject(w, 408, "deadline", "operation was not admitted")
		return
	}
	defer release()
	if ctx.Err() != nil {
		s.reject(w, 408, "deadline", "operation was not admitted")
		return
	}
	// Hash the full typed request, including authorization fences and deadline.
	canonical, _ := json.Marshal(req)
	digest := sha256.Sum256(canonical)
	fingerprint := hex.EncodeToString(digest[:])
	s.mu.Lock()
	if ctx.Err() != nil || !req.Deadline.After(time.Now()) {
		s.mu.Unlock()
		s.reject(w, 408, "deadline", "operation was not admitted")
		return
	}
	if prior, ok := s.record.Operations[req.RequestID]; ok {
		s.mu.Unlock()
		if prior.Digest != fingerprint {
			s.reject(w, 409, "request_conflict", "request id belongs to different content")
			return
		}
		s.respond(w, 200, &prior, nil)
		return
	}
	if !strings.HasPrefix(req.RequestID, fmt.Sprintf("%d:", s.record.ReplayEpoch)) {
		s.mu.Unlock()
		s.reject(w, 409, "retired_request", "request id belongs to another replay epoch")
		return
	}
	if req.ReplayEpoch != s.record.ReplayEpoch {
		s.mu.Unlock()
		s.reject(w, 409, "retired_request", "replay epoch differs")
		return
	}
	if req.Fence != s.record.Fence {
		s.mu.Unlock()
		s.reject(w, 409, "stale_fence", "supervisor resource epoch or revision differs")
		return
	}
	if s.record.State != "ready" && req.Operation != "resume" && req.Operation != "stop" && req.Operation != "acknowledge" {
		s.mu.Unlock()
		s.reject(w, 409, "draining", "service is not admitting mutations")
		return
	}
	if s.record.State == "stopped" {
		s.mu.Unlock()
		s.reject(w, 409, "stopped", "service has stopped")
		return
	}
	var retired []string
	if req.Operation == "acknowledge" {
		var problem *Error
		retired, problem = s.acknowledgementTargetsLocked(req.Payload)
		if problem != nil {
			s.mu.Unlock()
			s.reject(w, 409, problem.Code, problem.Message)
			return
		}
	}
	limit := maxOperations
	if isBuiltin(req.Operation) {
		limit += 16
	}
	if req.Operation == "stop" {
		limit++
	}
	if req.Operation == "acknowledge" {
		limit += 2
	}
	if len(s.record.Operations) >= limit {
		message := s.receiptCapacityMessageLocked()
		s.mu.Unlock()
		s.reject(w, 507, "resource_exhausted", message)
		return
	}
	if !isBuiltin(req.Operation) {
		current, _ := json.Marshal(s.record)
		reserved := maxBody
		for _, pending := range s.record.Operations {
			if pending.State == "pending" {
				reserved += maxBody
			}
		}
		if len(current)+reserved > maxJournal-(1<<20) {
			s.mu.Unlock()
			s.reject(w, 507, "resource_exhausted", "journal quota reached; acknowledge completed receipts")
			return
		}
	}
	receipt := Receipt{RequestID: req.RequestID, Operation: req.Operation, AcceptedAt: time.Now().UTC(), Digest: fingerprint, State: "pending", Fence: s.record.Fence}
	s.record.Operations[req.RequestID] = receipt
	if err = writeRecord(s.path, s.record); err != nil {
		delete(s.record.Operations, req.RequestID)
		s.mu.Unlock()
		s.reject(w, 507, "storage", "operation intent was not persisted")
		return
	}
	rootContext := s.rootContext
	s.mu.Unlock()
	ctx, executionCancel := s.executionContext(ctx, rootContext, req)
	defer executionCancel()
	if ctx.Err() != nil || !req.Deadline.After(time.Now()) {
		s.respond(w, 200, &receipt, nil)
		return
	}
	var result json.RawMessage
	var problem *Error
	switch req.Operation {
	case "drain", "resume", "acknowledge":
	case "stop":
		if s.config.Shutdown != nil {
			if err = s.config.Shutdown(ctx); err != nil {
				problem = &Error{Code: "shutdown_failed", Message: "domain shutdown remains unconfirmed"}
			}
		}
	default:
		result, problem = s.config.Handler(ctx, req)
	}
	// Canceled effects stay uncertain unless the domain proves a terminal outcome.
	if ctx.Err() != nil {
		outcome, confirmed := s.reconcileCanceledMutation(rootContext, req, Outcome{Result: result, Error: problem})
		if !confirmed {
			s.respond(w, 200, &receipt, nil)
			return
		}
		result, problem = outcome.Result, outcome.Error
	}
	if len(result) > maxBody || len(result) > 0 && !json.Valid(result) {
		result = nil
		problem = &Error{Code: "invalid_result", Message: "domain result is invalid or exceeds limit"}
	}
	if problem != nil && (len(problem.Code) > 128 || len(problem.Message) > 1024) {
		problem = &Error{Code: "domain_failure", Message: "domain failure exceeds transport limit"}
	}
	s.mu.Lock()
	receipt.Result = bytes.Clone(result)
	receipt.Error = problem
	receipt.State = "completed"
	if err = s.completeLocked(req, &receipt, retired); err != nil {
		s.mu.Unlock()
		s.reject(w, 507, "storage", "operation outcome could not be persisted")
		return
	}
	s.mu.Unlock()
	s.respond(w, 200, &receipt, nil)
	if req.Operation == "stop" && problem == nil {
		s.stopOnce.Do(func() { close(s.stop) })
	}
}
