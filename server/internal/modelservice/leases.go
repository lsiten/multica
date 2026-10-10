package modelservice

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func (s *Service) updateLease(operation string, input modelRequest) (json.RawMessage, *runtimeproc.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases[input.LeaseID]
	if !ok || lease.grant.Execution != input.Execution {
		return nil, &runtimeproc.Error{Code: "lease_lost", Message: "lease is absent or belongs to another execution"}
	}
	if operation == "model.release" {
		lease.revoked = true
		s.releaseIfIdleLocked(input.LeaseID, lease)
		return encode(struct{}{})
	}
	if lease.revoked || !lease.grant.Deadline.After(time.Now()) {
		return nil, &runtimeproc.Error{Code: "lease_lost", Message: "lease deadline expired"}
	}
	lease.grant.Deadline = time.Now().Add(s.leaseTTL)
	return encode(lease.grant)
}
func (s *Service) releaseIfIdleLocked(id string, lease *leaseState) {
	if lease.revoked && lease.inflight == 0 {
		lease.model.Release()
		delete(s.leases, id)
	}
}
func (s *Service) expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, job := range s.acquisitions {
		if time.Now().After(job.deadline) {
			job.cancel()
			delete(s.acquisitions, id)
		}
	}
	for id, lease := range s.leases {
		if !lease.grant.Deadline.After(time.Now()) {
			lease.revoked = true
		}
		s.releaseIfIdleLocked(id, lease)
	}
}
func (s *Service) infer(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/leases/")
	id, path, ok := strings.Cut(suffix, "/")
	if !strings.HasPrefix(r.URL.Path, "/leases/") || !ok || r.Method != "POST" || r.URL.RawQuery != "" {
		http.Error(w, "unknown inference route", 404)
		return
	}
	// The existing adapters use /v1/systemone and chat completion endpoints;
	// no health/admin/arbitrary upstream route is exposed through task grants.
	if path != "v1/chat/completions" && path != "chat/completions" && path != "v1/systemone" {
		http.Error(w, "unsupported inference route", 404)
		return
	}
	s.mu.Lock()
	lease, ok := s.leases[id]
	if !ok || s.closed || lease.revoked || !lease.grant.Deadline.After(time.Now()) || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+lease.grant.Token)) != 1 {
		s.mu.Unlock()
		http.Error(w, "lease unavailable", 401)
		return
	}
	if lease.inflight >= 4 {
		s.mu.Unlock()
		http.Error(w, "inference capacity exhausted", 429)
		return
	}
	lease.inflight++
	endpoint, token := lease.model.Endpoint, lease.model.Token
	s.mu.Unlock()
	defer func() { s.mu.Lock(); lease.inflight--; s.releaseIfIdleLocked(id, lease); s.mu.Unlock() }()
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "inference request exceeds limit", 413)
		return
	}
	origin, err := url.Parse(endpoint)
	if err != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" {
		http.Error(w, "invalid model worker origin", 502)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	upstream, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(endpoint, "/")+"/"+path, strings.NewReader(string(raw)))
	if err != nil {
		http.Error(w, "invalid inference request", 400)
		return
	}
	upstream.Header.Set("Authorization", "Bearer "+token)
	upstream.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(upstream)
	if err != nil {
		http.Error(w, "model inference unavailable", 502)
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		http.Error(w, "model redirect rejected", 502)
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		http.Error(w, "model response exceeds limit", 502)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}
