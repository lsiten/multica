//go:build browserintegration

package mirror

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type browserFixtureProvider struct{ sample []byte }

func (p browserFixtureProvider) Open(_ context.Context, s EncodedSource) (EncodedStream, error) {
	return &browserFixtureStream{sample: p.sample, ticker: time.NewTicker(time.Second / time.Duration(s.FPS))}, nil
}

type browserFixtureStream struct {
	sample []byte
	ticker *time.Ticker
}

func (s *browserFixtureStream) Next(ctx context.Context) (EncodedSample, error) {
	select {
	case <-ctx.Done():
		return EncodedSample{}, ctx.Err()
	case now := <-s.ticker.C:
		return EncodedSample{AnnexB: s.sample, KeyFrame: true, PTSNanos: now.UnixNano(), DurationNanos: int64(time.Second / 30)}, nil
	}
}
func (s *browserFixtureStream) ForceKeyframe() error { return nil }
func (s *browserFixtureStream) Close() error         { s.ticker.Stop(); return nil }

func TestVideoChromiumDecodesNativeFixture(t *testing.T) {
	dir := os.Getenv("MULTICA_MIRROR_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("MULTICA_MIRROR_EVIDENCE_DIR required for browser integration")
	}
	sample := loadIDRFixture(t, "testdata/video-1280x720.h264", 31)
	m := NewRuntimeMirror(nil, time.Hour)
	defer m.Close(context.Background())
	m.SetCaptureHub(NewCaptureHub(browserFixtureProvider{sample: sample}))
	mux := http.NewServeMux()
	results := make(chan []byte, 1)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, browserVideoPage)
	})
	mux.HandleFunc("/offer", func(w http.ResponseWriter, r *http.Request) {
		var offer SessionDescription
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&offer); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		source := fixtureSource()
		grant := fixtureGrant(source)
		grant.ExpiresAt = time.Now().Add(30 * time.Second)
		n, err := m.AnswerVideo(r.Context(), "viewer", offer, ICEConfig{}, source, grant, 1)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		n.Commit()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(struct {
			Answer  SessionDescription `json:"answer"`
			Quality *VideoQuality      `json:"quality"`
		}{n.SessionDescription, n.VideoQuality}); err != nil {
			t.Error(err)
		}
	})
	mux.HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128*1024))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		select {
		case results <- data:
		default:
		}
		w.WriteHeader(204)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	if err := os.WriteFile(filepath.Join(dir, "task-9-browser-server.txt"), []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("browser fixture URL=%s", server.URL)
	select {
	case raw := <-results:
		var result struct {
			Width         int                `json:"width"`
			Height        int                `json:"height"`
			FramesDecoded uint64             `json:"framesDecoded"`
			AudioTracks   int                `json:"audioTracks"`
			Quality       VideoQuality       `json:"quality"`
			Metadata      VideoFrameMetadata `json:"metadata"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if result.Width != 1280 || result.Height != 720 || result.FramesDecoded < 3 || result.AudioTracks != 0 || result.Quality.MaxLevelIDC != 31 || result.Metadata.Type != "mirror:video-meta" {
			t.Fatalf("browser decode failed: %s", raw)
		}
		if err := os.WriteFile(filepath.Join(dir, "task-9-browser-decoded.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("Chromium ontrack decoded %d frames at %dx%d; no audio; advertised quality level=%d", result.FramesDecoded, result.Width, result.Height, result.Quality.MaxLevelIDC)
	case <-time.After(90 * time.Second):
		t.Fatal("browser evidence did not arrive")
	}
}

const browserVideoPage = `<!doctype html><meta charset="utf-8"><title>Native H264 transport fixture</title><style>body{background:#121212;color:white;font:18px sans-serif}video{width:960px;display:block}pre{white-space:pre-wrap}</style><h1>Native H264 → Pion → Chromium</h1><video id="screen" autoplay muted playsinline></video><pre id="status">Negotiating receiver capabilities…</pre><script>
const video=document.getElementById("screen"),statusNode=document.getElementById("status");window.peer=new RTCPeerConnection();let stream;
peer.ontrack=e=>{stream=e.streams[0]||new MediaStream([e.track]);video.srcObject=stream;video.play()};
(async()=>{const control=peer.createDataChannel('mirror-control',{ordered:true});control.onmessage=e=>{if(typeof e.data!=='string')throw new Error('binary media on control channel');window.videoMetadata=JSON.parse(e.data)};peer.addTransceiver('video',{direction:'recvonly'});const offer=await peer.createOffer();await peer.setLocalDescription(offer);if(peer.iceGatheringState!=='complete')await new Promise(resolve=>peer.addEventListener('icegatheringstatechange',()=>{if(peer.iceGatheringState==='complete')resolve()}));const response=await fetch('/offer',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(peer.localDescription)});if(!response.ok)throw new Error(await response.text());const payload=await response.json();window.quality=payload.quality;await peer.setRemoteDescription(payload.answer);const poll=setInterval(async()=>{const stats=await peer.getStats();for(const row of stats.values()){if(row.type==='inbound-rtp'&&row.kind==='video'&&row.framesDecoded>=3&&window.videoMetadata){const canvas=document.createElement('canvas');canvas.width=video.videoWidth;canvas.height=video.videoHeight;const c=canvas.getContext('2d');c.drawImage(video,0,0);window.doneResult={width:video.videoWidth,height:video.videoHeight,framesDecoded:row.framesDecoded,bytesReceived:row.bytesReceived,decoderImplementation:row.decoderImplementation,quality,metadata:videoMetadata,audioTracks:stream.getAudioTracks().length,pixel:Array.from(c.getImageData(100,100,1,1).data),userAgent:navigator.userAgent};statusNode.textContent=JSON.stringify(doneResult,null,2);clearInterval(poll);await fetch("/result",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(doneResult)})}}},100)})().catch(e=>{window.decodeError=String(e);document.getElementById('status').textContent=String(e)})
</script>`
