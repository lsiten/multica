package communications

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

var ErrIdempotencyConflict = errors.New("idempotency key is bound to a different request")

type idempotencyRecord struct {
	Call        Call   `json:"call"`
	Pending     bool   `json:"pending"`
	Fingerprint string `json:"fingerprint"`
}

type IdempotencyStore interface {
	Reserve(string) (bool, error)
	Complete(string, Call) error
	Forget(string) error
	Get(string) (Call, bool, bool)
}
type FingerprintedIdempotencyStore interface {
	IdempotencyStore
	ReserveFingerprint(string, string) (bool, error)
}
type MemoryIdempotencyStore struct {
	mu sync.Mutex
	m  map[string]idempotencyRecord
}

func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{m: make(map[string]idempotencyRecord)}
}
func (s *MemoryIdempotencyStore) Reserve(key string) (bool, error) {
	return s.ReserveFingerprint(key, "")
}
func (s *MemoryIdempotencyStore) ReserveFingerprint(key, fingerprint string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.m[key]; ok {
		if r.Fingerprint != fingerprint {
			return false, ErrIdempotencyConflict
		}
		if r.Pending {
			return false, ErrAmbiguousOperation
		}
		return false, nil
	}
	s.m[key] = idempotencyRecord{Pending: true, Fingerprint: fingerprint}
	return true, nil
}
func (s *MemoryIdempotencyStore) Complete(key string, call Call) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[key]
	if !ok {
		return ErrInvalidRequest
	}
	r.Call = call
	r.Pending = false
	s.m[key] = r
	return nil
}
func (s *MemoryIdempotencyStore) Forget(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}
func (s *MemoryIdempotencyStore) Get(key string) (Call, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[key]
	return r.Call, ok, r.Pending
}

type ProviderError struct{ StatusCode int }

func (e *ProviderError) Error() string {
	return fmt.Sprintf("phone provider returned HTTP %d", e.StatusCode)
}

func phoneRequestFingerprint(req CallRequest, cfg Config) string {
	// Include every provider-visible field that can change the call. Keep
	// credentials out of the digest so secret rotation does not turn a retry
	// into a second outbound call.
	data, _ := json.Marshal(struct {
		To             string
		From           string
		Message        string
		Twiml          string
		StatusCallback string
		DefaultStatus  string
		RecordCallback string
		Record         bool
		Provider       string
		AccountSID     string
		BaseURL        string
		HTTPBaseURL    string
	}{
		To:             req.To,
		From:           cfg.FromNumber,
		Message:        req.Message,
		Twiml:          req.Twiml,
		StatusCallback: req.StatusCallback,
		DefaultStatus:  cfg.StatusCallbackURL,
		RecordCallback: cfg.RecordingStatusCallbackURL,
		Record:         req.Record,
		Provider:       cfg.Provider,
		AccountSID:     cfg.AccountSID,
		BaseURL:        cfg.BaseURL,
		HTTPBaseURL:    cfg.HTTPBaseURL,
	})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
