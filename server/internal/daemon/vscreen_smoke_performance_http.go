package daemon

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func projectPerformanceMarker(marker smokefixture.PerformanceMarker, s native.SourceDescriptor, width, height int) performanceMarker {
	result := performanceMarker{Columns: 20, Reason: "marker_geometry_unavailable"}
	layout, err := capture.FitLayout(s.LogicalWidth, s.LogicalHeight, uint32(width), uint32(height))
	if err != nil || marker.CellSize <= 0 || marker.Columns != 20 {
		return result
	}
	scale := layout.ContentWidth / s.LogicalWidth
	result.X = layout.ContentX + (marker.X-float64(s.X))*scale
	result.Y = layout.ContentY + (marker.Y-float64(s.Y))*scale
	result.CellSize = marker.CellSize * scale
	if result.X < 0 || result.Y < 0 || result.X+20*result.CellSize > float64(width) || result.Y+8*result.CellSize > float64(height) || math.Abs(result.CellSize-math.Round(result.CellSize)) > 0.001 || math.Abs(result.X-math.Round(result.X)) > 0.001 || math.Abs(result.Y-math.Round(result.Y)) > 0.001 {
		result.Reason = "negotiated_marker_not_integral_or_visible"
		return result
	}
	result.Available = true
	result.Reason = ""
	return result
}
func performanceOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != "" && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}
func (p *performanceProducer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	receive := p.now()
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || !performanceOrigin(r.Header.Get("Origin")) {
		http.Error(w, "private_scope_required", 403)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == r.Header.Get("Authorization") || subtle.ConstantTimeCompare([]byte(token), []byte(p.config.Nonce)) != 1 {
		http.Error(w, "private_capability_required", 401)
		return
	}
	write := func(value any) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(value); err != nil {
			return
		}
	}
	decode := func(v any) error {
		r.Body = http.MaxBytesReader(w, r.Body, 512*1024)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil {
			return err
		}
		if d.Decode(new(any)) != io.EOF {
			return errors.New("trailing_json")
		}
		return nil
	}
	if r.Method == http.MethodGet && r.URL.Path == "/clock" {
		send := p.now()
		if receive == 0 || send < receive {
			http.Error(w, "host_clock_invalid", 503)
			return
		}
		write(map[string]string{"host_receive_ns": strconv.FormatUint(receive, 10), "host_send_ns": strconv.FormatUint(send, 10), "clock_epoch": p.clockEpoch})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/finish" {
		var body struct{}
		if decode(&body) != nil {
			http.Error(w, "invalid_finish", 400)
			return
		}
		result := p.finish(nil)
		write(result)
		p.finishedOnce.Do(func() { close(p.finished) })
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		http.Error(w, "performance_closed", 409)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/metrics" {
		write(map[string]any{"elapsed_ms": float64(time.Since(p.started).Microseconds()) / 1000, "resources": p.resources, "shared_source": p.shared, "system_gpu": p.systemGPU, "network": p.networkObservation(r.Context()), "errors": p.errors})
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	switch r.URL.Path {
	case "/offer":
		var in performanceOfferRequest
		if decode(&in) != nil || in.ViewerID == "" || len(in.ViewerID) > 128 || len(in.Offer.SDP) > 128*1024 || in.Offer.Type != "offer" {
			http.Error(w, "invalid_offer", 400)
			return
		}
		source, ok := p.sources[in.SourceID]
		if !ok {
			http.Error(w, "foreign_source", 403)
			return
		}
		if _, exists := p.peers[in.ViewerID]; exists || len(p.peers) >= 2 {
			http.Error(w, "viewer_limit_or_duplicate", 409)
			return
		}
		runtime := p.runtimes[source.RuntimeID]
		grant := protocol.MirrorViewerGrant{GrantID: uuid.NewString(), SessionID: uuid.NewString(), UserID: "performance-owner", ViewerID: in.ViewerID, WorkspaceID: source.source.Resource.WorkspaceID, RuntimeID: source.RuntimeID, Source: source.source.Source, NativeEpoch: source.NativeEpoch, SourceGeneration: source.Generation, ExpiresAt: time.Now().Add(25 * time.Second)}
		selected := mirror.EncodedSource{Binding: source.source.MirrorSourceBinding, GeometryRevision: source.source.GeometryRevision, DisplayID: source.source.DisplayID, Width: 1600, Height: 900, FPS: 30, Bitrate: 12000000}
		answer, err := runtime.AnswerVideo(r.Context(), in.ViewerID, in.Offer, mirror.ICEConfig{}, selected, grant, 1)
		if err != nil {
			http.Error(w, "video_negotiation_failed", 409)
			return
		}
		if answer.VideoQuality == nil {
			answer.Abandon()
			http.Error(w, "negotiated_geometry_unavailable", 409)
			return
		}
		answer.Commit()
		p.peers[in.ViewerID] = performancePeer{runtime: runtime, negotiation: answer, grant: grant, sourceID: in.SourceID}
		marker := source.Marker
		for _, owned := range p.owned {
			if owned.key.RuntimeID == source.RuntimeID && owned.marker != nil {
				marker = projectPerformanceMarker(*owned.marker, source.source, answer.VideoQuality.Width, answer.VideoQuality.Height)
			}
		}
		write(performanceOfferResponse{Answer: answer.SessionDescription, SourceID: in.SourceID, SourceTag: source.SourceTag, Marker: marker, Negotiated: VscreenPerformanceRequest{answer.VideoQuality.Width, answer.VideoQuality.Height, answer.VideoQuality.FPS}})
	case "/renew", "/viewer/close":
		var in struct {
			ViewerID string `json:"viewer_id"`
		}
		if decode(&in) != nil {
			http.Error(w, "invalid_viewer", 400)
			return
		}
		peer, ok := p.peers[in.ViewerID]
		if !ok {
			http.Error(w, "unknown_viewer", 404)
			return
		}
		if r.URL.Path == "/renew" {
			peer.grant.ExpiresAt = time.Now().Add(25 * time.Second)
			if !peer.runtime.RenewViewerGrant(in.ViewerID, peer.grant, 1) {
				http.Error(w, "viewer_expired", 409)
				return
			}
			p.peers[in.ViewerID] = peer
		} else {
			if stats, ok := peer.runtime.PeerStatistics(in.ViewerID); ok {
				value, known := performanceRTPBytes(stats)
				p.closedPeerBytes += value
				if !known {
					p.peerStatsMissing = true
				}
			} else {
				p.peerStatsMissing = true
			}
			if peer.negotiation.Abandon() != nil {
				p.errors = append(p.errors, "viewer_close_unconfirmed")
				http.Error(w, "viewer_close_unconfirmed", 409)
				return
			}
			delete(p.peers, in.ViewerID)
		}
		write(map[string]bool{"ok": true})
	case "/cycles":
		var in struct {
			Count int `json:"count"`
		}
		if decode(&in) != nil {
			http.Error(w, "invalid_cycles", 400)
			return
		}
		cycles, err := p.runCycles(r.Context(), in.Count)
		errs := []string{}
		if err != nil {
			errs = append(errs, "lifecycle_cycles_failed")
			p.errors = append(p.errors, errs...)
		}
		write(map[string]any{"cycles": cycles, "errors": errs})
	case "/samples":
		var sample performanceBrowserSample
		if decode(&sample) != nil || sample.validate(p) != nil {
			http.Error(w, "invalid_sample_scope_or_shape", 400)
			return
		}
		raw, err := json.Marshal(sample)
		if err != nil || p.journal == nil {
			http.Error(w, "sample_journal_unavailable", 500)
			return
		}
		if p.journalBytes+uint64(len(raw)+1) > 128*1024*1024 {
			p.errors = append(p.errors, "sample_journal_limit")
			http.Error(w, "sample_journal_limit", 413)
			return
		}
		p.journalBytes += uint64(len(raw) + 1)
		if _, err = p.journal.Write(append(raw, '\n')); err != nil {
			p.errors = append(p.errors, "sample_journal_failed")
			http.Error(w, "sample_journal_failed", 500)
			return
		}
		write(map[string]bool{"ok": true})
	default:
		http.NotFound(w, r)
	}
}

type performanceBrowserFrame struct {
	FrameID       uint32  `json:"frame_id"`
	SourceTag     uint32  `json:"source_tag"`
	DrawNS        string  `json:"fixture_draw_host_ns"`
	ObservedMS    float64 `json:"browser_observed_ms"`
	LatencyMS     float64 `json:"latency_upper_ms"`
	UncertaintyMS float64 `json:"clock_uncertainty_ms"`
}
type performanceBrowserSample struct {
	SchemaVersion int                       `json:"schema_version"`
	Phase         string                    `json:"phase"`
	ViewerID      string                    `json:"viewer_id"`
	SourceID      string                    `json:"source_id"`
	ElapsedMS     float64                   `json:"browser_elapsed_ms"`
	Frames        []performanceBrowserFrame `json:"frames"`
	Counters      map[string]uint64         `json:"counters"`
	SwitchMS      *float64                  `json:"switch_first_decoded_ms,omitempty"`
}

func (s performanceBrowserSample) validate(p *performanceProducer) error {
	peer, ok := p.peers[s.ViewerID]
	if !ok || peer.sourceID != s.SourceID || s.SchemaVersion != 1 || len(s.Frames) > 600 || len(s.Counters) > 8 || s.Phase != "steady" && s.Phase != "concurrent" && s.Phase != "switch" || !performanceFinite(s.ElapsedMS) {
		return errors.New("invalid_samples")
	}
	for _, frame := range s.Frames {
		stamp, err := strconv.ParseUint(frame.DrawNS, 10, 64)
		if err != nil || stamp == 0 || frame.SourceTag == 0 || !performanceFinite(frame.ObservedMS) || !performanceFinite(frame.LatencyMS) || !performanceFinite(frame.UncertaintyMS) {
			return errors.New("invalid_frame")
		}
	}
	if s.SwitchMS != nil && !performanceFinite(*s.SwitchMS) {
		return errors.New("invalid_switch")
	}
	return nil
}
func performanceFinite(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
