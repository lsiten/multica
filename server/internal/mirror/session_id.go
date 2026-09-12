package mirror

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
)

func newSessionID() (string, error) {
	buffer := make([]byte, minSessionIDBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (s *SessionStore) lookupIdentityLocked(sessionID string, identity SessionIdentity) (*storedSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) < minSessionIDBytes {
		return nil, ErrSessionNotFound
	}
	stored, ok := s.items[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !sameIdentity(stored.metadata, identity) {
		return nil, ErrSessionIdentityMismatch
	}
	return stored, nil
}

func (s *SessionStore) MetadataByID(ctx context.Context, sessionID string) (SessionMetadata, error) {
	if err := contextError(ctx); err != nil {
		return SessionMetadata{}, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) < minSessionIDBytes {
		return SessionMetadata{}, ErrSessionNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.items[sessionID]
	if !ok {
		return SessionMetadata{}, ErrSessionNotFound
	}
	now := s.clock.Now()
	if s.deleteExpiredLocked(sessionID, now) {
		if stored.metadata.State != SessionStateClosed {
			stored.metadata.State = SessionStateExpired
		}
		return SessionMetadata{}, ErrSessionExpired
	}
	if stored.metadata.State == SessionStateExpired {
		return SessionMetadata{}, ErrSessionExpired
	}
	return metadataFromSession(stored.metadata), nil
}
