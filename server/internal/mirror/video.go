package mirror

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type videoPeer struct {
	source       EncodedSource
	control      *webrtc.DataChannel
	lastMetadata time.Time
	track        *webrtc.TrackLocalStaticSample
	sender       *webrtc.RTPSender
	attach       func() (*CaptureSubscription, error)
}

// SetCaptureHub installs the daemon-owned shared source registry before negotiation.
func (m *RuntimeMirror) SetCaptureHub(hub *CaptureHub) { m.mu.Lock(); m.hub = hub; m.mu.Unlock() }

// AnswerVideo negotiates H264 using a source and grant already authorized by the
// authenticated daemon control boundary. Media never enters signaling payloads.
func (m *RuntimeMirror) AnswerVideo(ctx context.Context, viewerID string, offer SessionDescription, ice ICEConfig, source EncodedSource, grant protocol.MirrorViewerGrant, generation uint64) (Negotiation, error) {
	if err := source.validate(); err != nil {
		return Negotiation{}, err
	}
	fmtp, level, err := selectVideoCodec(offer)
	if err != nil {
		return Negotiation{}, err
	}
	source = negotiateVideoQuality(source, level)
	source.ExcludedWindowIDs = append([]uint32(nil), source.ExcludedWindowIDs...)
	if !validVideoGrant(viewerID, source, grant) {
		return Negotiation{}, errors.New("mirror: invalid video grant binding")
	}
	m.mu.Lock()
	hub := m.hub
	m.mu.Unlock()
	if hub == nil {
		return Negotiation{}, errors.New("mirror: video capture unavailable")
	}
	negotiation, err := m.answer(ctx, viewerID, offer, ice, func(peer *mirrorPeer) error {
		track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: fmtp}, "screen", "mirror")
		if err != nil {
			return fmt.Errorf("mirror: video track: %w", err)
		}
		sender, err := peer.pc.AddTrack(track)
		if err != nil {
			return fmt.Errorf("mirror: add video track: %w", err)
		}
		peer.mu.Lock()
		defer peer.mu.Unlock()
		if peer.closed {
			return ErrViewerClosed
		}
		peer.video = &videoPeer{source: source, track: track, sender: sender, attach: func() (*CaptureSubscription, error) { return hub.Subscribe(context.WithoutCancel(ctx), source) }}
		peer.grant = &viewerGrant{value: grant, generation: generation}
		m.armViewerGrant(viewerID, peer)
		return nil
	}, fmtp)
	if err == nil {
		quality := source.videoQuality()
		negotiation.VideoQuality = &quality
	}
	return negotiation, err
}

func validVideoGrant(viewerID string, s EncodedSource, g protocol.MirrorViewerGrant) bool {
	return g.GrantID != "" && g.SessionID != "" && g.UserID != "" && g.ViewerID == viewerID && g.WorkspaceID == s.Binding.Resource.WorkspaceID && g.RuntimeID == s.Binding.Resource.RuntimeID && g.NativeEpoch == s.Binding.NativeEpoch && g.Source == s.Binding.Source && g.SourceGeneration == s.Binding.Generation && time.Until(g.ExpiresAt) > 0
}

func (m *RuntimeMirror) attachVideo(viewerID string, peer *mirrorPeer) {
	peer.mu.Lock()
	if peer.closed {
		peer.mu.Unlock()
		return
	}
	sub, err := peer.video.attach()
	if err != nil {
		peer.mu.Unlock()
		m.source.mu.Lock()
		hook := m.source.captureFailureHook
		m.source.mu.Unlock()
		if hook != nil {
			hook(err)
		}
		_ = m.removePeer(viewerID, peer, true)
		return
	}
	peer.detach = func() {
		if err := sub.Close(); err != nil {
			peer.mu.Lock()
			peer.closeErr = errors.Join(peer.closeErr, err)
			peer.mu.Unlock()
		}
		m.source.setVideoViewer(viewerID, false)
	}
	m.source.setVideoViewer(viewerID, true)
	if peer.attachTimer != nil {
		peer.attachTimer.Stop()
	}
	peer.mu.Unlock()
	go func() {
		var nextPTS int64
		for {
			sample, err := sub.Next(context.Background())
			if err != nil {
				peer.mu.Lock()
				closed := peer.closed
				peer.mu.Unlock()
				if !closed {
					m.source.reportVideoFailure(err)
				}
				_ = m.removePeer(viewerID, peer, true)
				return
			}
			if nextPTS > 0 && sample.PTSNanos > nextPTS {
				if err := peer.video.track.WriteSample(media.Sample{Duration: time.Duration(sample.PTSNanos - nextPTS)}); err != nil {
					_ = m.removePeer(viewerID, peer, true)
					return
				}
			}
			nextPTS = sample.PTSNanos + sample.DurationNanos
			if err := peer.video.track.WriteSample(media.Sample{Data: sample.AnnexB, Duration: time.Duration(sample.DurationNanos)}); err != nil {
				_ = m.removePeer(viewerID, peer, true)
				return
			}
			if err := m.sendVideoMetadata(peer, sample.PTSNanos, false); err != nil {
				_ = m.removePeer(viewerID, peer, true)
				return
			}
		}
	}()
	go func() {
		for {
			packets, _, err := peer.video.sender.ReadRTCP()
			if err != nil {
				return
			}
			for _, packet := range packets {
				switch packet.(type) {
				case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
					if err := sub.ForceKeyframe(); err != nil {
						_ = m.removePeer(viewerID, peer, true)
						return
					}
				}
			}
		}
	}()
}
