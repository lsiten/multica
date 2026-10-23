package runtimeproc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

func isBuiltin(op string) bool {
	return slices.Contains([]string{"handshake", "health", "query_operation", "drain", "resume", "stop", "acknowledge"}, op)
}
func (s *Service) statusLocked() Status {
	return Status{Receipts: s.receiptUsageLocked(), ReplayEpoch: s.record.ReplayEpoch, Identity: s.record.Identity, State: s.record.State, Fence: s.record.Fence, Capabilities: append([]string{"handshake", "health", "query_operation", "drain", "resume", "stop", "acknowledge"}, append(slices.Clone(s.config.Capabilities), s.config.ReadCapabilities...)...)}
}
func (s *Service) respond(w http.ResponseWriter, code int, receipt *Receipt, problem *Error) {
	s.mu.Lock()
	status := s.statusLocked()
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// Broken connections do not undo durable operation receipts.
	_ = json.NewEncoder(w).Encode(Response{Status: status, Receipt: receipt, Error: problem})
}
func (s *Service) reject(w http.ResponseWriter, code int, kind, message string) {
	s.respond(w, code, nil, &Error{Code: kind, Message: message})
}
func (s *Service) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.config.Bootstrap.Token)) != 1 {
		s.reject(w, 401, "unauthorized", "private credential required")
		return
	}
	if r.Method != "POST" || r.URL.Path != "/rpc" || r.URL.RawQuery != "" {
		s.reject(w, 404, "not_found", "unknown endpoint")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		s.reject(w, 413, "payload_too_large", "request exceeds limit")
		return
	}
	var req Request
	if json.Unmarshal(raw, &req) != nil {
		s.reject(w, 400, "malformed", "invalid request body")
		return
	}
	if req.Identity != s.config.Bootstrap.Identity {
		s.reject(w, 409, "identity_mismatch", "scope instance build or protocol differs")
		return
	}
	if len(req.RequestID) == 0 || len(req.RequestID) > 128 || len(req.Operation) > 128 {
		s.reject(w, 400, "malformed", "request identity required")
		return
	}
	if req.Deadline.IsZero() || !req.Deadline.After(time.Now()) || req.Deadline.After(time.Now().Add(5*time.Minute)) {
		s.reject(w, 408, "deadline", "deadline expired or exceeds five minutes")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), req.Deadline)
	defer cancel()
	if req.Operation == "handshake" || req.Operation == "health" {
		s.respond(w, 200, nil, nil)
		return
	}
	if req.Operation == "query_operation" {
		var query struct {
			RequestID string `json:"request_id"`
		}
		if json.Unmarshal(req.Payload, &query) != nil || query.RequestID == "" {
			s.reject(w, 400, "malformed", "query request id required")
			return
		}
		s.mu.Lock()
		receipt, ok := s.record.Operations[query.RequestID]
		replayEpoch := s.record.ReplayEpoch
		s.mu.Unlock()
		if !ok && !strings.HasPrefix(query.RequestID, fmt.Sprintf("%d:", replayEpoch)) {
			s.reject(w, 410, "retired_request", "operation belongs to a retired replay epoch")
			return
		}
		if !ok {
			s.reject(w, 404, "not_found", "operation has not been accepted")
			return
		}
		s.respond(w, 200, &receipt, nil)
		return
	}
	if slices.Contains(s.config.ReadCapabilities, req.Operation) {
		s.mu.Lock()
		ready := s.record.State == "ready" || s.record.State == "draining"
		s.mu.Unlock()
		if !ready {
			s.reject(w, 409, "not_ready", "service is not available")
			return
		}
		result, problem := s.config.ReadHandler(ctx, req)
		if ctx.Err() != nil {
			s.reject(w, 408, "deadline", "read deadline exceeded")
			return
		}
		if len(result) > maxBody || len(result) > 0 && !json.Valid(result) {
			s.reject(w, 500, "invalid_result", "invalid readonly result")
			return
		}
		s.respond(w, 200, &Receipt{RequestID: req.RequestID, State: "read", Result: result, Error: problem}, problem)
		return
	}
	if !isBuiltin(req.Operation) && !slices.Contains(s.config.Capabilities, req.Operation) {
		s.reject(w, 400, "unknown_operation", "unsupported operation")
		return
	}
	s.mutateHTTP(ctx, w, req)
}
