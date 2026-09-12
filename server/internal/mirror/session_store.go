package mirror

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type SessionStore struct {
	mu    sync.Mutex
	clock Clock
	ttl   time.Duration
	items map[string]*storedSession
}

type storedSession struct {
	metadata   Session
	offer      protocol.MirrorSessionDescription
	answer     protocol.MirrorSessionDescription
	offerUsed  bool
	answerSet  bool
	answerUsed bool
}

func (s *SessionStore) Create(ctx context.Context, input CreateSessionInput) (Session, error) {
	if err := contextError(ctx); err != nil {
		return Session{}, err
	}
	identity, err := normalizeIdentity(input.Identity)
	if err != nil {
		return Session{}, err
	}
	if err := input.Offer.Validate("offer"); err != nil {
		return Session{}, fmt.Errorf("create mirror session: %w", err)
	}

	createdAt := s.clock.Now()
	expiresAt := createdAt.Add(s.ttl)
	id, err := newSessionID()
	if err != nil {
		return Session{}, fmt.Errorf("create mirror session id: %w", err)
	}
	metadata := newOfferedSession(id, identity, createdAt, expiresAt)
	s.mu.Lock()
	s.purgeExpiredLocked(createdAt)
	s.items[id] = &storedSession{metadata: metadata, offer: input.Offer}
	s.mu.Unlock()
	return metadata, nil
}

func (s *SessionStore) ConsumeOffer(ctx context.Context, sessionID string, identity SessionIdentity) (protocol.MirrorSessionDescription, error) {
	if err := contextError(ctx); err != nil {
		return protocol.MirrorSessionDescription{}, err
	}
	identity, err := normalizeIdentity(identity)
	if err != nil {
		return protocol.MirrorSessionDescription{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.lookupLocked(sessionID, identity)
	if err != nil {
		return protocol.MirrorSessionDescription{}, err
	}
	if stored.offerUsed {
		return protocol.MirrorSessionDescription{}, ErrSessionReplay
	}
	stored.offerUsed = true
	offer := stored.offer
	stored.offer = protocol.MirrorSessionDescription{}
	return offer, nil
}

func (s *SessionStore) SetAnswer(ctx context.Context, sessionID string, identity SessionIdentity, answer protocol.MirrorSessionDescription) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	identity, err := normalizeIdentity(identity)
	if err != nil {
		return err
	}
	if err := answer.Validate("answer"); err != nil {
		return fmt.Errorf("set mirror answer: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.lookupLocked(sessionID, identity)
	if err != nil {
		return err
	}
	if !stored.offerUsed || stored.answerSet || stored.metadata.State != SessionStateOffered {
		return ErrSessionReplay
	}
	stored.answer = answer
	stored.answerSet = true
	stored.metadata.State = SessionStateAnswered
	return nil
}

func (s *SessionStore) SetFailure(ctx context.Context, sessionID string, identity SessionIdentity, reason string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	identity, err := normalizeIdentity(identity)
	if err != nil {
		return err
	}
	if !validSessionFailureReason(reason) {
		return ErrInvalidSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.lookupLocked(sessionID, identity)
	if err != nil {
		return err
	}
	if !stored.offerUsed || stored.answerSet || stored.metadata.State != SessionStateOffered {
		return ErrSessionReplay
	}
	stored.metadata.State = SessionStateFailed
	stored.metadata.FailureReason = reason
	stored.clearSignalingData()
	return nil
}

func (s *SessionStore) Answer(ctx context.Context, sessionID string, identity SessionIdentity) (protocol.MirrorSessionDescription, error) {
	if err := contextError(ctx); err != nil {
		return protocol.MirrorSessionDescription{}, err
	}
	identity, err := normalizeIdentity(identity)
	if err != nil {
		return protocol.MirrorSessionDescription{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.lookupLocked(sessionID, identity)
	if err != nil {
		return protocol.MirrorSessionDescription{}, err
	}
	if !stored.answerSet {
		return protocol.MirrorSessionDescription{}, ErrSessionReplay
	}
	if stored.answerUsed {
		return protocol.MirrorSessionDescription{}, ErrSessionReplay
	}
	stored.answerUsed = true
	answer := stored.answer
	stored.answer = protocol.MirrorSessionDescription{}
	return answer, nil
}

func (s *SessionStore) Metadata(ctx context.Context, sessionID string, identity SessionIdentity) (SessionMetadata, error) {
	if err := contextError(ctx); err != nil {
		return SessionMetadata{}, err
	}
	identity, err := normalizeIdentity(identity)
	if err != nil {
		return SessionMetadata{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.lookupLocked(sessionID, identity)
	if err != nil {
		return SessionMetadata{}, err
	}
	return metadataFromSession(stored.metadata), nil
}

func (s *SessionStore) Close(ctx context.Context, sessionID string, identity SessionIdentity) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	identity, err := normalizeIdentity(identity)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.lookupIdentityLocked(sessionID, identity)
	if err != nil {
		return err
	}
	if stored.metadata.State == SessionStateClosed {
		return nil
	}
	now := s.clock.Now()
	if s.deleteExpiredLocked(sessionID, now) {
		stored.metadata.State = SessionStateExpired
		return ErrSessionExpired
	}
	stored.metadata.State = SessionStateClosed
	stored.clearSignalingData()
	return nil
}

func (s *storedSession) clearSignalingData() {
	s.offer = protocol.MirrorSessionDescription{}
	s.answer = protocol.MirrorSessionDescription{}
}

func (s *SessionStore) lookupLocked(sessionID string, identity SessionIdentity) (*storedSession, error) {
	stored, err := s.lookupIdentityLocked(sessionID, identity)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	if s.deleteExpiredLocked(sessionID, now) {
		if stored.metadata.State != SessionStateClosed {
			stored.metadata.State = SessionStateExpired
		}
		return nil, ErrSessionExpired
	}
	if stored.metadata.State == SessionStateClosed {
		return nil, ErrSessionClosed
	}
	if stored.metadata.State == SessionStateExpired {
		return nil, ErrSessionExpired
	}
	return stored, nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mirror session operation: %w", err)
	}
	return nil
}

func validSessionFailureReason(reason string) bool {
	switch reason {
	case protocol.MirrorAnswerFailurePermissionDenied,
		protocol.MirrorAnswerFailureUnsupported,
		protocol.MirrorAnswerFailureNoDisplay,
		protocol.MirrorAnswerFailureCaptureUnavailable,
		protocol.MirrorAnswerFailureNegotiation:
		return true
	default:
		return false
	}
}
