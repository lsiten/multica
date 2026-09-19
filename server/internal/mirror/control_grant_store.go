package mirror

import (
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ControlGrantRecord retains one server-issued human input capability. It
// stores no input payloads, only identity/credential metadata.
type ControlGrantRecord struct {
	Grant            protocol.MirrorControlGrant
	DaemonID         string
	DaemonGeneration string
	CredentialHash   string
	CredentialKind   string
	CredentialExpiry time.Time
	Active           bool
}

// ControlGrantStore keeps bounded, expiring control grants. One viewer may
// hold at most one control grant per runtime; re-entering interaction renews
// or replaces that single grant.
type ControlGrantStore struct {
	mu    sync.Mutex
	items map[string]ControlGrantRecord // keyed by runtimeID + viewerID
}

func controlGrantKey(runtimeID, viewerID string) string {
	return runtimeID + "\x00" + viewerID
}

func NewControlGrantStore() *ControlGrantStore {
	return &ControlGrantStore{items: make(map[string]ControlGrantRecord)}
}

// Put inserts or replaces the single grant for a runtime+viewer.
func (s *ControlGrantStore) Put(record ControlGrantRecord, now time.Time) error {
	if !record.Grant.ExpiresAt.After(now) {
		return ErrInvalidSessionInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	if len(s.items) >= 4096 {
		return ErrInvalidSessionInput
	}
	s.items[controlGrantKey(record.Grant.RuntimeID, record.Grant.ViewerID)] = record
	return nil
}

// Lookup returns the live grant for one runtime+viewer.
func (s *ControlGrantStore) Lookup(runtimeID, viewerID string, now time.Time) (ControlGrantRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.items[controlGrantKey(runtimeID, viewerID)]
	if !ok || !record.Grant.ExpiresAt.After(now) {
		return ControlGrantRecord{}, false
	}
	return record, true
}

// Renew replaces the stored record after a successful downstream renewal.
func (s *ControlGrantStore) Renew(record ControlGrantRecord, now time.Time) (ControlGrantRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := controlGrantKey(record.Grant.RuntimeID, record.Grant.ViewerID)
	current, ok := s.items[key]
	if !ok || !current.Grant.ExpiresAt.After(now) {
		return ControlGrantRecord{}, false
	}
	s.items[key] = record
	return record, true
}

// Remove deletes the grant for a runtime+viewer and returns it.
func (s *ControlGrantStore) Remove(runtimeID, viewerID string) (ControlGrantRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := controlGrantKey(runtimeID, viewerID)
	record, ok := s.items[key]
	delete(s.items, key)
	return record, ok
}

// RemoveRuntime removes every control grant for one runtime and returns it
// for daemon-side revocation.
func (s *ControlGrantStore) RemoveRuntime(daemonID, runtimeID string) []ControlGrantRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var removed []ControlGrantRecord
	for key, record := range s.items {
		if record.Grant.RuntimeID == runtimeID && (daemonID == "" || record.DaemonID == daemonID) {
			removed = append(removed, record)
			delete(s.items, key)
		}
	}
	return removed
}

// RemoveDaemon removes grants bound to a disconnecting daemon.
func (s *ControlGrantStore) RemoveDaemon(daemonID string, runtimeIDs []string) []ControlGrantRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := make(map[string]bool, len(runtimeIDs))
	for _, id := range runtimeIDs {
		wanted[id] = true
	}
	var removed []ControlGrantRecord
	for key, record := range s.items {
		if record.DaemonID == daemonID && (len(wanted) == 0 || wanted[record.Grant.RuntimeID]) {
			removed = append(removed, record)
			delete(s.items, key)
		}
	}
	return removed
}

// RemoveIfCurrent removes the record only while it still has the supplied
// grant identity, preventing a stale revocation from removing a replacement.
func (s *ControlGrantStore) RemoveIfCurrent(record ControlGrantRecord) (ControlGrantRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := controlGrantKey(record.Grant.RuntimeID, record.Grant.ViewerID)
	current, ok := s.items[key]
	if !ok || current.Grant.GrantID != record.Grant.GrantID {
		return ControlGrantRecord{}, false
	}
	delete(s.items, key)
	return current, true
}

// SetActive marks whether the viewer's input channel is currently attached.
func (s *ControlGrantStore) SetActive(daemonID, runtimeID, viewerID string, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := controlGrantKey(runtimeID, viewerID)
	if record, ok := s.items[key]; ok && record.DaemonID == daemonID {
		record.Active = active
		s.items[key] = record
	}
}

// Records returns all stored grants for sweep/inspection.
func (s *ControlGrantStore) Records() []ControlGrantRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]ControlGrantRecord, 0, len(s.items))
	for _, record := range s.items {
		records = append(records, record)
	}
	return records
}

func (s *ControlGrantStore) sweepLocked(now time.Time) {
	for key, record := range s.items {
		if !record.Grant.ExpiresAt.After(now) {
			delete(s.items, key)
		}
	}
}

// Sweep reaps expired grants and returns records that just expired so callers
// can issue revokes.
func (s *ControlGrantStore) Sweep(now time.Time) []ControlGrantRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []ControlGrantRecord
	for key, record := range s.items {
		if !record.Grant.ExpiresAt.After(now) {
			expired = append(expired, record)
			delete(s.items, key)
		}
	}
	return expired
}
