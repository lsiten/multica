package mirror

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestSessionQualityIsAtomicAndCannotReviveClosedSession(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "closed"}[closed], func(t *testing.T) {
			store, _, identity := newTestSessionStore(t)
			session := createTestSession(t, store, identity)
			consumeTestOffer(t, store, session.ID, identity)
			if closed {
				if err := store.Close(context.Background(), session.ID, identity); err != nil {
					t.Fatal(err)
				}
			}
			quality := &protocol.MirrorVideoQuality{Width: 1280, Height: 720, FPS: 30, Bitrate: 8000000, MaxLevelIDC: 31}
			err := store.SetAnswerWithQuality(context.Background(), session.ID, identity, protocol.MirrorSessionDescription{Type: "answer", SDP: "fixture-answer"}, quality)
			if closed {
				if err == nil {
					t.Fatal("late answer revived closed session")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			quality.Width = 2
			metadata, err := store.Metadata(context.Background(), session.ID, identity)
			if err != nil {
				t.Fatal(err)
			}
			if metadata.VideoQuality == nil || metadata.VideoQuality.Width != 1280 {
				t.Fatalf("quality not copied atomically: %+v", metadata)
			}
			metadata.VideoQuality.Width = 4
			again, err := store.Metadata(context.Background(), session.ID, identity)
			if err != nil || again.VideoQuality.Width != 1280 {
				t.Fatal("metadata alias mutated stored quality")
			}
		})
	}
}
