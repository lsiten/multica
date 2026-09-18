package mirror

import (
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

// VideoFrameMetadata describes an immutable viewer session. PTS is diagnostic;
// DataChannel delivery is not synchronized to a decoded RTP frame or AI snapshot.
type VideoFrameMetadata struct {
	Type             string                       `json:"type"`
	SourceBinding    protocol.MirrorSourceBinding `json:"source_binding"`
	GeometryRevision uint64                       `json:"geometry_revision"`
	PTSNanos         int64                        `json:"pts_nanos"`
	Quality          VideoQuality                 `json:"quality"`
}

func (m *RuntimeMirror) sendVideoMetadata(peer *mirrorPeer, pts int64, force bool) error {
	peer.mu.Lock()
	video := peer.video
	if peer.closed || video == nil || video.control == nil || video.control.ReadyState() != webrtc.DataChannelStateOpen || video.control.BufferedAmount() > 64*1024 || !force && time.Since(video.lastMetadata) < time.Second {
		peer.mu.Unlock()
		return nil
	}
	video.lastMetadata = time.Now()
	channel := video.control
	metadata := VideoFrameMetadata{Type: "mirror:video-meta", SourceBinding: video.source.Binding, GeometryRevision: video.source.GeometryRevision, PTSNanos: pts, Quality: video.source.videoQuality()}
	peer.mu.Unlock()
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return channel.SendText(string(encoded))
}
