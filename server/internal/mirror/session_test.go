package mirror

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestSessionStore(t *testing.T) (*SessionStore, *testClock, SessionIdentity) {
	t.Helper()
	clock := &testClock{now: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}
	store := NewSessionStore(SessionStoreOptions{Clock: clock, TTL: time.Minute})
	identity := SessionIdentity{
		WorkspaceID: "workspace-1",
		RuntimeID:   "runtime-1",
		UserID:      "user-1",
		DaemonID:    "daemon-1",
		ViewerID:    "viewer-1",
	}
	return store, clock, identity
}

func createTestSession(t *testing.T, store *SessionStore, identity SessionIdentity) Session {
	t.Helper()
	session, err := store.Create(context.Background(), CreateSessionInput{
		Identity: identity,
		Offer:    protocol.MirrorSessionDescription{Type: "offer", SDP: "offer-sdp"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return session
}

func TestSessionStore_ConsumeOffer_rejectsExpiredSession(t *testing.T) {
	// Given
	store, clock, identity := newTestSessionStore(t)
	session := createTestSession(t, store, identity)
	clock.Advance(time.Minute)

	// When
	_, err := store.ConsumeOffer(context.Background(), session.ID, identity)

	// Then
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("consume expired offer error = %v, want %v", err, ErrSessionExpired)
	}
}

func TestSessionStorePurgeExpiredDeletesExpiredSDP(t *testing.T) {
	// Given
	store, clock, identity := newTestSessionStore(t)
	session := createTestSession(t, store, identity)
	clock.Advance(time.Minute)

	// When
	removed, err := store.PurgeExpired(context.Background())
	if err != nil {
		t.Fatalf("PurgeExpired() error = %v", err)
	}

	// Then
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	store.mu.Lock()
	_, exists := store.items[session.ID]
	store.mu.Unlock()
	if exists {
		t.Fatal("expired session SDP remained in memory after purge")
	}
}

func TestSessionStoreCreatePurgesExpiredSessions(t *testing.T) {
	// Given
	store, clock, identity := newTestSessionStore(t)
	expired := createTestSession(t, store, identity)
	clock.Advance(time.Minute)

	// When
	created := createTestSession(t, store, identity)

	// Then
	store.mu.Lock()
	_, expiredExists := store.items[expired.ID]
	_, createdExists := store.items[created.ID]
	store.mu.Unlock()
	if expiredExists {
		t.Fatal("expired session SDP remained in memory when a new session was created")
	}
	if !createdExists {
		t.Fatal("new session was not retained")
	}
}

func TestSessionStore_ConsumeOffer_rejectsWrongIdentity(t *testing.T) {
	// Given
	store, _, identity := newTestSessionStore(t)
	session := createTestSession(t, store, identity)
	wrong := identity
	wrong.UserID = "other-user"

	// When
	_, err := store.ConsumeOffer(context.Background(), session.ID, wrong)

	// Then
	if !errors.Is(err, ErrSessionIdentityMismatch) {
		t.Fatalf("wrong user error = %v, want %v", err, ErrSessionIdentityMismatch)
	}
}

func TestSessionStore_ConsumeOffer_rejectsWrongRuntime(t *testing.T) {
	// Given
	store, _, identity := newTestSessionStore(t)
	session := createTestSession(t, store, identity)
	wrong := identity
	wrong.RuntimeID = "other-runtime"

	// When
	_, err := store.ConsumeOffer(context.Background(), session.ID, wrong)

	// Then
	if !errors.Is(err, ErrSessionIdentityMismatch) {
		t.Fatalf("wrong runtime error = %v, want %v", err, ErrSessionIdentityMismatch)
	}
}

func TestSessionStore_ConsumeOffer_isSingleUse(t *testing.T) {
	// Given
	store, _, identity := newTestSessionStore(t)
	session := createTestSession(t, store, identity)

	// When
	_, firstErr := store.ConsumeOffer(context.Background(), session.ID, identity)
	_, replayErr := store.ConsumeOffer(context.Background(), session.ID, identity)

	// Then
	if firstErr != nil {
		t.Fatalf("first consume error: %v", firstErr)
	}
	if !errors.Is(replayErr, ErrSessionReplay) {
		t.Fatalf("replay error = %v, want %v", replayErr, ErrSessionReplay)
	}
}

func TestSessionStore_ConsumeOffer_allowsExactlyOneConcurrentConsumer(t *testing.T) {
	// Given
	store, _, identity := newTestSessionStore(t)
	session := createTestSession(t, store, identity)
	start := make(chan struct{})
	errs := make(chan error, 2)

	// When
	for range 2 {
		go func() {
			<-start
			_, err := store.ConsumeOffer(context.Background(), session.ID, identity)
			errs <- err
		}()
	}
	close(start)
	firstErr, secondErr := <-errs, <-errs

	// Then
	if (firstErr == nil) == (secondErr == nil) {
		t.Fatalf("consume errors = %v, %v; want exactly one success", firstErr, secondErr)
	}
	if !errors.Is(firstErr, ErrSessionReplay) && !errors.Is(secondErr, ErrSessionReplay) {
		t.Fatalf("errors = %v, %v; want one replay", firstErr, secondErr)
	}
}

func TestSessionStore_Close_isIdempotent(t *testing.T) {
	// Given
	store, _, identity := newTestSessionStore(t)
	session := createTestSession(t, store, identity)

	// When
	firstErr := store.Close(context.Background(), session.ID, identity)
	secondErr := store.Close(context.Background(), session.ID, identity)

	// Then
	if firstErr != nil || secondErr != nil {
		t.Fatalf("close errors = %v, %v; want nil", firstErr, secondErr)
	}
	if _, err := store.ConsumeOffer(context.Background(), session.ID, identity); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("consume closed session error = %v, want %v", err, ErrSessionClosed)
	}
}

func TestSessionStore_SetAnswer_rejectsReplayAndKeepsSDPOutOfMetadata(t *testing.T) {
	// Given
	store, _, identity := newTestSessionStore(t)
	session := createTestSession(t, store, identity)
	if _, err := store.ConsumeOffer(context.Background(), session.ID, identity); err != nil {
		t.Fatalf("consume offer: %v", err)
	}
	answer := protocol.MirrorSessionDescription{Type: "answer", SDP: "answer-sdp"}

	// When
	firstErr := store.SetAnswer(context.Background(), session.ID, identity, answer)
	replayErr := store.SetAnswer(context.Background(), session.ID, identity, answer)
	metadata, metadataErr := store.Metadata(context.Background(), session.ID, identity)

	// Then
	if firstErr != nil {
		t.Fatalf("set answer: %v", firstErr)
	}
	if !errors.Is(replayErr, ErrSessionReplay) {
		t.Fatalf("answer replay error = %v, want %v", replayErr, ErrSessionReplay)
	}
	if metadataErr != nil {
		t.Fatalf("read metadata: %v", metadataErr)
	}
	if metadata.State != SessionStateAnswered || metadata.SDP != "" {
		t.Fatalf("metadata = %+v, want answered state without SDP", metadata)
	}
}

func TestSessionStoreAnswerIsSingleUseAndClearsSDP(t *testing.T) {
	// Given
	store, _, identity := newTestSessionStore(t)
	session := createTestSession(t, store, identity)
	consumeTestOffer(t, store, session.ID, identity)
	answer := protocol.MirrorSessionDescription{Type: "answer", SDP: "answer-sdp"}
	if err := store.SetAnswer(context.Background(), session.ID, identity, answer); err != nil {
		t.Fatalf("set answer: %v", err)
	}

	// When
	first, err := store.Answer(context.Background(), session.ID, identity)

	// Then
	if err != nil {
		t.Fatalf("first answer: %v", err)
	}
	if first != answer {
		t.Fatalf("first answer = %+v, want %+v", first, answer)
	}
	if _, err := store.Answer(context.Background(), session.ID, identity); !errors.Is(err, ErrSessionReplay) {
		t.Fatalf("second answer error = %v, want %v", err, ErrSessionReplay)
	}
	store.mu.Lock()
	remaining := store.items[session.ID].answer
	store.mu.Unlock()
	if remaining != (protocol.MirrorSessionDescription{}) {
		t.Fatalf("answer SDP remained after consumption: %+v", remaining)
	}
}

func TestSessionStoreSetFailureStoresReasonAfterConsumedOffer(t *testing.T) {
	// Given
	store, _, identity := newTestSessionStore(t)
	session := createTestSession(t, store, identity)
	if _, err := store.ConsumeOffer(context.Background(), session.ID, identity); err != nil {
		t.Fatalf("consume offer: %v", err)
	}

	// When
	err := store.SetFailure(context.Background(), session.ID, identity, protocol.MirrorAnswerFailureNoDisplay)

	// Then
	if err != nil {
		t.Fatalf("SetFailure() = %v, want nil", err)
	}
	metadata, err := store.MetadataByID(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("MetadataByID() = %v, want nil", err)
	}
	if metadata.State != SessionStateFailed || metadata.FailureReason != protocol.MirrorAnswerFailureNoDisplay || metadata.SDP != "" {
		t.Fatalf("metadata = %+v, want failed/no-display without SDP", metadata)
	}
}

func TestSessionStoreCloseForDaemonClosesOnlyMatchingActiveSessions(t *testing.T) {
	// Given
	store, _, identity := newTestSessionStore(t)
	matching := createTestSession(t, store, identity)
	if _, err := store.ConsumeOffer(context.Background(), matching.ID, identity); err != nil {
		t.Fatalf("consume matching offer: %v", err)
	}

	otherRuntime := identity
	otherRuntime.RuntimeID = "runtime-2"
	otherRuntime.ViewerID = "viewer-2"
	otherRuntimeSession := createTestSession(t, store, otherRuntime)

	otherDaemon := identity
	otherDaemon.DaemonID = "daemon-2"
	otherDaemon.ViewerID = "viewer-3"
	otherDaemonSession := createTestSession(t, store, otherDaemon)

	// When
	closed, err := store.CloseForDaemon(
		context.Background(),
		identity.DaemonID,
		[]string{identity.RuntimeID},
		nil,
	)

	// Then
	if err != nil {
		t.Fatalf("CloseForDaemon() error = %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	metadata, err := store.MetadataByID(context.Background(), matching.ID)
	if err != nil {
		t.Fatalf("matching metadata: %v", err)
	}
	if metadata.State != SessionStateClosed {
		t.Fatalf("matching state = %q, want %q", metadata.State, SessionStateClosed)
	}
	if _, err := store.ConsumeOffer(context.Background(), matching.ID, identity); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("consume matching after close error = %v, want %v", err, ErrSessionClosed)
	}
	for _, session := range []Session{otherRuntimeSession, otherDaemonSession} {
		metadata, err := store.MetadataByID(context.Background(), session.ID)
		if err != nil {
			t.Fatalf("unrelated session %s metadata: %v", session.ID, err)
		}
		if metadata.State != SessionStateOffered {
			t.Fatalf("unrelated session %s state = %q, want %q", session.ID, metadata.State, SessionStateOffered)
		}
	}
}

func TestSessionStoreCloseViewerClearsOnlyThatViewerSession(t *testing.T) {
	// Given
	store, _, firstIdentity := newTestSessionStore(t)
	first := createTestSession(t, store, firstIdentity)
	if _, err := store.ConsumeOffer(context.Background(), first.ID, firstIdentity); err != nil {
		t.Fatalf("consume first offer: %v", err)
	}
	if err := store.SetAnswer(context.Background(), first.ID, firstIdentity, protocol.MirrorSessionDescription{
		Type: "answer",
		SDP:  "answer-sdp",
	}); err != nil {
		t.Fatalf("set first answer: %v", err)
	}

	secondIdentity := firstIdentity
	secondIdentity.ViewerID = "viewer-2"
	second := createTestSession(t, store, secondIdentity)
	if _, err := store.ConsumeOffer(context.Background(), second.ID, secondIdentity); err != nil {
		t.Fatalf("consume second offer: %v", err)
	}
	if err := store.SetAnswer(context.Background(), second.ID, secondIdentity, protocol.MirrorSessionDescription{
		Type: "answer",
		SDP:  "second-answer-sdp",
	}); err != nil {
		t.Fatalf("set second answer: %v", err)
	}

	// When
	closed, err := store.CloseViewer(
		context.Background(),
		firstIdentity.DaemonID,
		firstIdentity.RuntimeID,
		firstIdentity.ViewerID,
	)

	// Then
	if err != nil {
		t.Fatalf("CloseViewer() error = %v", err)
	}
	if !closed {
		t.Fatal("CloseViewer() = false, want true")
	}
	if _, err := store.Answer(context.Background(), first.ID, firstIdentity); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("first answer after close error = %v, want %v", err, ErrSessionClosed)
	}
	answer, err := store.Answer(context.Background(), second.ID, secondIdentity)
	if err != nil {
		t.Fatalf("second answer after unrelated viewer close: %v", err)
	}
	if answer.SDP != "second-answer-sdp" {
		t.Fatalf("second answer SDP = %q, want retained answer", answer.SDP)
	}
}

func TestSessionStoreSetFailureRejectsInvalidTransitions(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, store *SessionStore, session Session, identity SessionIdentity)
		advance time.Duration
		reason  string
		want    error
	}{
		{
			name: "before offer consumption",
			want: ErrSessionReplay,
		},
		{
			name: "after answer",
			prepare: func(t *testing.T, store *SessionStore, session Session, identity SessionIdentity) {
				t.Helper()
				consumeTestOffer(t, store, session.ID, identity)
				if err := store.SetAnswer(context.Background(), session.ID, identity, protocol.MirrorSessionDescription{
					Type: "answer",
					SDP:  "answer-sdp",
				}); err != nil {
					t.Fatalf("set answer: %v", err)
				}
			},
			want: ErrSessionReplay,
		},
		{
			name: "after close",
			prepare: func(t *testing.T, store *SessionStore, session Session, identity SessionIdentity) {
				t.Helper()
				consumeTestOffer(t, store, session.ID, identity)
				if err := store.Close(context.Background(), session.ID, identity); err != nil {
					t.Fatalf("close session: %v", err)
				}
			},
			want: ErrSessionClosed,
		},
		{
			name:    "after expiry",
			advance: time.Minute,
			prepare: func(t *testing.T, store *SessionStore, session Session, identity SessionIdentity) {
				t.Helper()
				consumeTestOffer(t, store, session.ID, identity)
			},
			want: ErrSessionExpired,
		},
		{
			name: "replayed failure",
			prepare: func(t *testing.T, store *SessionStore, session Session, identity SessionIdentity) {
				t.Helper()
				consumeTestOffer(t, store, session.ID, identity)
				if err := store.SetFailure(context.Background(), session.ID, identity, protocol.MirrorAnswerFailureUnsupported); err != nil {
					t.Fatalf("first failure: %v", err)
				}
			},
			reason: protocol.MirrorAnswerFailureUnsupported,
			want:   ErrSessionReplay,
		},
		{
			name:   "unknown reason",
			reason: "internal-error",
			want:   ErrInvalidSession,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			store, clock, identity := newTestSessionStore(t)
			session := createTestSession(t, store, identity)
			if tt.prepare != nil {
				tt.prepare(t, store, session, identity)
			}
			if tt.advance > 0 {
				clock.Advance(tt.advance)
			}
			reason := tt.reason
			if reason == "" {
				reason = protocol.MirrorAnswerFailureNegotiation
			}

			// When
			err := store.SetFailure(context.Background(), session.ID, identity, reason)

			// Then
			if !errors.Is(err, tt.want) {
				t.Fatalf("SetFailure() = %v, want %v", err, tt.want)
			}
		})
	}
}

func consumeTestOffer(t *testing.T, store *SessionStore, sessionID string, identity SessionIdentity) {
	t.Helper()
	if _, err := store.ConsumeOffer(context.Background(), sessionID, identity); err != nil {
		t.Fatalf("consume offer: %v", err)
	}
}
