package mirror

import (
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// controlGrantTTL bounds a dropped renewal or revocation locally, in addition
// to the authoritative server-issued expiry.
const controlGrantTTL = 30 * time.Second

// controlGrant is an input capability independent of the read-only viewer
// grant. All fields, including the replay counter, are guarded by peer.mu.
type controlGrant struct {
	value      protocol.MirrorControlGrant
	generation uint64
	timer      *time.Timer
	deadline   time.Time
	lastSeq    uint64
	voiceSeq   uint64
}

func validControlGrant(viewerID string, g protocol.MirrorControlGrant) bool {
	return g.ViewerID == viewerID && g.Validate(time.Now()) == nil
}

func sameControlSource(a, b protocol.MirrorControlGrant) bool {
	return a.NativeEpoch == b.NativeEpoch && a.Source == b.Source && a.SourceGeneration == b.SourceGeneration
}
