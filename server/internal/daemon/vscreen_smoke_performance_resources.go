package daemon

import (
	"context"
	"strconv"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/pion/webrtc/v4"
)

func unavailablePerformanceResources() performanceResources {
	a := map[string]performanceAvailability{}
	for _, name := range []string{"rss", "cpu", "fd", "callbacks", "encoder", "bandwidth", "gpu"} {
		a[name] = performanceAvailability{Reason: "metric_not_observed"}
	}
	a["gpu"] = performanceAvailability{Reason: "unprivileged_owned_process_gpu_counter_unavailable"}
	a["callbacks"] = performanceAvailability{Reason: "native_capture_api_does_not_export_active_callback_count"}
	a["encoder"] = performanceAvailability{Reason: "native_capture_api_does_not_export_independent_encoder_instance_count"}
	return performanceResources{Availability: a, Samples: []performanceResourceSample{}}
}
func (p *performanceProducer) sampleResources(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	elapsed := float64(time.Since(p.started).Microseconds()) / 1000
	shared, encoderStats, errs := p.capture.snapshot(ctx)
	p.shared.TotalCaptureOpens = shared.TotalCaptureOpens
	p.errors = append(p.errors, errs...)
	listed, listErr := p.client.Call(ctx, native.Request{Operation: "list"})
	live := map[string]*capture.LiveResourceStats{"native": listed.LiveResources}
	goLive := capture.LiveResources()
	live["go"] = &goLive
	if listErr != nil {
		live["native"] = nil
	}
	if n := live["native"]; n != nil && n.Available && len(p.peers) == 2 {
		source := ""
		same := true
		for _, peer := range p.peers {
			if source != "" && source != peer.sourceID {
				same = false
			}
			source = peer.sourceID
		}
		if same {
			p.shared.IndependentlyObserved = true
			p.shared.Reason = "native_atomic_peak_with_two_same_source_viewers"
			p.shared.CaptureSessions = max(p.shared.CaptureSessions, int(n.CaptureSessions))
			enc := int(n.EncoderSessions)
			if p.shared.EncoderSessions == nil || enc > *p.shared.EncoderSessions {
				p.shared.EncoderSessions = &enc
			}
		}
	}
	if gpu, err := p.readGPU(); err == nil {
		gpu.ElapsedMS = elapsed
		p.systemGPU.Samples = append(p.systemGPU.Samples, gpu)
	} else {
		p.systemGPU.Availability = performanceAvailability{Reason: "system_gpu_readback_unavailable"}
	}
	for name, pid := range map[string]int{"go": p.pid, "native": p.nativePID} {
		resources := p.resources[name]
		reading, err := p.readProcess(pid)
		sample := performanceResourceSample{ElapsedMS: elapsed}
		if counts := live[name]; counts != nil && counts.Available {
			callbacks, encoders := counts.ActiveCallbacks, counts.EncoderSessions
			sample.ActiveCallbacks = &callbacks
			sample.ActiveEncoders = &encoders
			resources.Availability["callbacks"] = performanceAvailability{Available: true, Method: "in_process_native_frame_and_encode_callback_entries"}
			resources.Availability["encoder"] = performanceAvailability{Available: true, Method: "in_process_VTCompressionSession_lifetime_counter"}
		} else {
			resources.Availability["callbacks"] = performanceAvailability{Reason: "native_resource_counter_unavailable"}
			resources.Availability["encoder"] = performanceAvailability{Reason: "native_resource_counter_unavailable"}
		}
		if err == nil && reading.Start == p.processStarts[name] && reading.Start != 0 {
			rss := reading.RSS
			sample.RSS = &rss
			sample.CPU = strconv.FormatUint(reading.CPU, 10)
			resources.Availability["rss"] = performanceAvailability{Available: true}
			resources.Availability["cpu"] = performanceAvailability{Available: true}
			if reading.FD >= 0 {
				fd := reading.FD
				sample.FD = &fd
				resources.Availability["fd"] = performanceAvailability{Available: true}
			} else {
				resources.Availability["fd"] = performanceAvailability{Reason: "owned_fd_readback_unavailable"}
			}
		} else {
			for _, k := range []string{"rss", "cpu", "fd"} {
				resources.Availability[k] = performanceAvailability{Reason: "owned_process_identity_changed_or_readback_unavailable"}
			}
		}
		var bytes uint64
		if name == "native" {
			bytes = p.capture.bytes.Load()
			sample.EncoderStats = encoderStats
		} else {
			bytes = p.closedPeerBytes
			for id, peer := range p.peers {
				if stats, ok := peer.runtime.PeerStatistics(id); ok {
					value, known := performanceRTPBytes(stats)
					bytes += value
					if !known {
						p.peerStatsMissing = true
					}
				} else {
					p.peerStatsMissing = true
				}
			}
		}
		sample.Bytes = &bytes
		resources.Availability["bandwidth"] = performanceAvailability{Available: true, Method: "encoded_native_payload_bytes"}
		if name == "go" {
			resources.Availability["bandwidth"] = performanceAvailability{Available: !p.peerStatsMissing, Method: "Pion_outbound_RTP_bytes"}
			if p.peerStatsMissing {
				sample.Bytes = nil
				resources.Availability["bandwidth"] = performanceAvailability{Reason: "peer_RTP_stats_unavailable"}
			}
		}
		resources.Samples = append(resources.Samples, sample)
		p.resources[name] = resources
	}
}
func performanceRTPBytes(stats webrtc.StatsReport) (uint64, bool) {
	var n uint64
	found := false
	for _, s := range stats {
		if r, ok := s.(webrtc.OutboundRTPStreamStats); ok {
			n += r.BytesSent
			found = true
		}
	}
	return n, found
}
