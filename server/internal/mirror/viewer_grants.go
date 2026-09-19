package mirror

import (
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ViewerGrantRecord retains no credentials or media; it is independent of SDP expiry.
type ViewerGrantRecord struct {
	Grant            protocol.MirrorViewerGrant
	DaemonID         string
	DaemonGeneration string
	CredentialHash   string
	CredentialKind   string
	CredentialExpiry time.Time
	Legacy           bool
	Active           bool
}

// ViewerGrantStore keeps a bounded set of server-issued, connection-bound viewer leases.
type ViewerGrantStore struct {
	mu    sync.Mutex
	items map[string]ViewerGrantRecord
}

func NewViewerGrantStore() *ViewerGrantStore {
	return &ViewerGrantStore{items: make(map[string]ViewerGrantRecord)}
}

func (s *ViewerGrantStore) Add(record ViewerGrantRecord, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, item := range s.items {
		if !item.Grant.ExpiresAt.After(now) {
			delete(s.items, id)
		}
	}
	if len(s.items) >= 4096 || record.Grant.SessionID == "" || !record.Grant.ExpiresAt.After(now) {
		return ErrInvalidSessionInput
	}
	if _, exists := s.items[record.Grant.SessionID]; exists {
		return ErrSessionReplay
	}
	s.items[record.Grant.SessionID] = record
	return nil
}

func (s *ViewerGrantStore) Lookup(sessionID, userID, runtimeID string, now time.Time) (ViewerGrantRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[sessionID]
	if !ok || item.Grant.UserID != userID || item.Grant.RuntimeID != runtimeID {
		return ViewerGrantRecord{}, ErrSessionNotFound
	}
	if !item.Grant.ExpiresAt.After(now) {
		return ViewerGrantRecord{}, ErrSessionExpired
	}
	return item, nil
}

// Renew accepts an overlapping renewal of the same lease without resurrecting removal.

// LookupViewer returns the live grant for one authenticated runtime viewer.
func (s *ViewerGrantStore) LookupViewer(runtimeID, viewerID, userID string, now time.Time) (ViewerGrantRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.Grant.RuntimeID == runtimeID && item.Grant.ViewerID == viewerID &&
			item.Grant.UserID == userID && item.Grant.ExpiresAt.After(now) {
			return item, true
		}
	}
	return ViewerGrantRecord{}, false
}

func (s *ViewerGrantStore) Renew(previous ViewerGrantRecord, expiresAt, now time.Time) (ViewerGrantRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.items[previous.Grant.SessionID]
	if !ok || !current.Grant.ExpiresAt.After(now) || !expiresAt.After(now) {
		return ViewerGrantRecord{}, ErrSessionExpired
	}
	grant := current.Grant
	grant.ExpiresAt = previous.Grant.ExpiresAt
	if grant != previous.Grant || current.DaemonGeneration != previous.DaemonGeneration || current.CredentialHash != previous.CredentialHash {
		return ViewerGrantRecord{}, ErrSessionIdentityMismatch
	}
	if current.Grant != previous.Grant && !current.Grant.ExpiresAt.After(expiresAt) {
		return current, nil
	}
	current.Grant.ExpiresAt = expiresAt
	current.CredentialExpiry = previous.CredentialExpiry
	s.items[current.Grant.SessionID] = current
	return current, nil
}

func (s *ViewerGrantStore) Remove(sessionID string) (ViewerGrantRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[sessionID]
	delete(s.items, sessionID)
	return item, ok
}

func (s *ViewerGrantStore) Records() []ViewerGrantRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]ViewerGrantRecord, 0, len(s.items))
	for _, item := range s.items {
		records = append(records, item)
	}
	return records
}

func (s *ViewerGrantStore) SetActive(daemonID, runtimeID, viewerID string, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, item := range s.items {
		if item.DaemonID == daemonID && item.Grant.RuntimeID == runtimeID && item.Grant.ViewerID == viewerID {
			item.Active = active
			s.items[id] = item
		}
	}
}

// RemoveIfCurrent prevents a stale validation failure from removing a newer lease.
func (s *ViewerGrantStore) RemoveIfCurrent(previous ViewerGrantRecord) (ViewerGrantRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.items[previous.Grant.SessionID]
	if !ok || current.Grant != previous.Grant {
		return ViewerGrantRecord{}, false
	}
	delete(s.items, previous.Grant.SessionID)
	return current, true
}
