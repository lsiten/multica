package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/h264reader"
)

func encodedIDRFixture(t *testing.T) []byte {
	t.Helper()
	return loadIDRFixture(t, "testdata/video-1600x900.h264", 40)
}

func loadIDRFixture(t *testing.T, path string, level byte) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := h264reader.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var accessUnit []byte
	for {
		nal, err := reader.NextNAL()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		kind := nal.Data[0] & 31
		if kind == 7 || kind == 8 || kind == 5 {
			accessUnit = append(accessUnit, 0, 0, 0, 1)
			accessUnit = append(accessUnit, nal.Data...)
		}
		if kind == 5 {
			break
		}
	}
	if !bytes.Contains(accessUnit, []byte{0, 0, 0, 1, 0x27, 0x42, 0xc0, level}) {
		t.Fatal("fixture does not match advertised constrained baseline level4")
	}
	return accessUnit
}

func TestVideoTwoRealPionPeersReceiveH264AndPLI(t *testing.T) {
	p := &fixtureProvider{}
	m := NewRuntimeMirror(nil, time.Hour)
	defer m.Close(context.Background())
	hub := NewCaptureHub(p)
	m.SetCaptureHub(hub)
	source := fixtureSource()
	ai, err := hub.Subscribe(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Close()
	sample := encodedIDRFixture(t)
	received := []chan []byte{make(chan []byte, 1), make(chan []byte, 1)}
	metadata := []chan VideoFrameMetadata{make(chan VideoFrameMetadata, 1), make(chan VideoFrameMetadata, 1)}
	peers := make([]*webrtc.PeerConnection, 2)
	ssrcs := make([]atomic.Uint32, 2)
	for i := range 2 {
		pc, offer := videoOffer(t, webrtc.RTPCodecTypeVideo, func(message webrtc.DataChannelMessage) {
			if !message.IsString {
				t.Error("video sent binary media over control datachannel")
				return
			}
			var state VideoFrameMetadata
			if err := json.Unmarshal(message.Data, &state); err != nil {
				t.Error(err)
				return
			}
			select {
			case metadata[i] <- state:
			default:
			}
		})
		peers[i] = pc
		pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			ssrcs[i].Store(uint32(track.SSRC()))
			decoder := &codecs.H264Packet{}
			var unit []byte
			for {
				packet, _, err := track.ReadRTP()
				if err != nil {
					return
				}
				payload, err := decoder.Unmarshal(packet.Payload)
				if err != nil {
					return
				}
				unit = append(unit, payload...)
				if packet.Marker {
					select {
					case received[i] <- unit:
					default:
					}
					unit = nil
				}
			}
		})
		grant := fixtureGrant(source)
		grant.ViewerID = string(rune('a' + i))
		grant.GrantID = grant.ViewerID
		grant.SessionID = grant.ViewerID
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		answer, err := m.AnswerVideo(ctx, grant.ViewerID, offer, ICEConfig{}, source, grant, 1)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if !strings.Contains(answer.SDP, "profile-level-id=42c028") {
			t.Fatal("answer did not advertise actual encoder profile/level")
		}
		answer.Commit()
		cancel()
		if err := pc.SetRemoteDescription(answer.Pion()); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { hub.mu.Lock(); defer hub.mu.Unlock(); return len(ai.entry.subscribers) == 3 })
	if !m.HasViewers() {
		t.Fatal("video viewer missing active state")
	}
	var replay atomic.Int32
	if !m.SetViewerStateHook(func(change ViewerStateChange) {
		if change.Active {
			replay.Add(1)
		}
	}, 2) || replay.Load() != 2 {
		t.Fatal("video active viewers not replayed")
	}
	if m.SetViewerStateHook(func(change ViewerStateChange) { t.Error("stale state hook called") }, 1) {
		t.Fatal("stale observer generation accepted")
	}
	if len(p.streams) != 1 {
		t.Fatal("two peers duplicated capture")
	}
	// The fixture is produced by VideoToolbox without screen access. This tests
	// actual SRTP/ICE/RTP and depacketization, not browser or physical capture.
	for i := range 2 {
		deadline := time.NewTimer(2 * time.Second)
		ticker := time.NewTicker(30 * time.Millisecond)
		done := false
		for !done {
			select {
			case <-ticker.C:
				p.streams[0].samples <- EncodedSample{AnnexB: sample, KeyFrame: true, DurationNanos: int64(time.Second / 30)}
			case got := <-received[i]:
				if !bytes.Equal(got, sample) {
					t.Fatalf("peer %d RTP access unit differs: got%d want%d", i, len(got), len(sample))
				}
				if dir := os.Getenv("MULTICA_MIRROR_EVIDENCE_DIR"); dir != "" {
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("task-9-peer-%d.h264", i)), got, 0600); err != nil {
						t.Fatal(err)
					}
				}
				done = true
			case <-deadline.C:
				t.Fatalf("peer %d received no actual RTP", i)
			}
		}
		ticker.Stop()
		deadline.Stop()
	}
	for i := range 2 {
		select {
		case state := <-metadata[i]:
			if state.Type != "mirror:video-meta" || state.SourceBinding != source.Binding || state.Quality.Width != 1600 {
				t.Fatalf("wrong control metadata: %+v", state)
			}
		case <-time.After(time.Second):
			t.Fatal("missing video metadata")
		}
	}
	before := p.streams[0].requests.Load()
	if err := peers[0].WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ssrcs[0].Load()}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.streams[0].requests.Load() > before })
	if !m.RevokeViewerGrant("a", "a", 1) {
		t.Fatal("revoke failed")
	}
	hub.mu.Lock()
	remaining := len(ai.entry.subscribers)
	hub.mu.Unlock()
	if remaining != 2 || p.streams[0].closed.Load() {
		t.Fatalf("one revoke disturbed other viewer/AI: %d", remaining)
	}
	t.Logf("two real Pion peers received identical %d-byte SPS/PPS/IDR via SRTP; PLI requested encoder IDR; revoke retained other viewer and AI", len(sample))
}
