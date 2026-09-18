package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (p *performanceProducer) createSource(ctx context.Context) error {
	key := protocol.ResourceKey{BackendIdentity: p.backendID, WorkspaceID: p.workspaceID, RuntimeID: uuid.NewString(), UID: uint32(p.uid())}
	response, err := p.client.Call(ctx, native.Request{Operation: "ensure", Resource: key, Width: 1600, Height: 900})
	if err != nil {
		return err
	}
	if response.Display == nil || !response.Display.Managed {
		return errors.New("performance_display_readback_invalid")
	}
	owned := &performanceOwned{key: key, epoch: response.Epoch, displayID: response.Display.ID}
	p.owned = append(p.owned, owned)
	sources, err := p.client.Sources(ctx, key)
	if err != nil {
		return err
	}
	var source native.SourceDescriptor
	for _, candidate := range sources {
		if candidate.DisplayID == owned.displayID && candidate.Source.Kind == protocol.MirrorSourceVirtual {
			source = candidate
			break
		}
	}
	if source.DisplayID == 0 {
		return errors.New("performance_virtual_source_missing")
	}
	tag := performanceTag()
	if tag == 0 {
		return errors.New("performance_source_tag_unavailable")
	}
	for _, s := range p.sources {
		if s.SourceTag == tag {
			return errors.New("performance_source_tag_collision")
		}
	}
	fixture, bundle, err := p.prepareFixture(tag)
	if err != nil {
		return err
	}
	owned.fixture = fixture
	authority := appcontrol.Authority{Resource: key, Epoch: owned.epoch, TaskID: "performance", TransactionID: uuid.NewString(), LeaseEpoch: 1}
	if err = p.client.Grant(ctx, authority, 15*time.Second); err != nil {
		return err
	}
	if err = p.client.ResumeApps(ctx, authority); err != nil {
		return err
	}
	fixture.BeforeLaunch()
	if _, err = p.client.LaunchApp(ctx, authority, appcontrol.LaunchRequest{BundleID: bundle}); err != nil {
		return err
	}
	if err = p.client.Revoke(ctx, authority); err != nil {
		return err
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, e := fixture.Read()
		if e == nil && state.DisplayID == source.DisplayID && state.Marker != nil && state.Marker.SourceTag == tag {
			owned.marker = state.Marker
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("performance_marker_geometry_unavailable")
		case <-ticker.C:
		}
	}
	meta := performanceSource{SourceID: source.Source.SourceID, RuntimeID: key.RuntimeID, SourceTag: tag, NativeEpoch: source.NativeEpoch, Generation: source.Generation, Width: 1600, Height: 900, FPS: 30, source: source}
	meta.Marker = projectPerformanceMarker(*owned.marker, source, 1600, 900)
	p.sources[meta.SourceID] = meta
	runtime := mirror.NewRuntimeMirror(nil, time.Second)
	runtime.SetCaptureHub(p.hub)
	p.runtimes[key.RuntimeID] = runtime
	return nil
}
func (p *performanceProducer) disposeOwned(ctx context.Context, owned *performanceOwned) error {
	if owned.disposed {
		return nil
	}
	_, err := p.client.Call(ctx, native.Request{Operation: "quiesce", Resource: owned.key, Epoch: owned.epoch})
	if err != nil {
		return err
	}
	_, err = p.client.Call(ctx, native.Request{Operation: "dispose", Resource: owned.key, Epoch: owned.epoch})
	if err != nil {
		return err
	}
	listed, err := p.client.Call(ctx, native.Request{Operation: "list"})
	if err != nil {
		return err
	}
	for _, d := range listed.Displays {
		if d.ID == owned.displayID {
			return errors.New("performance_display_remains")
		}
	}
	owned.disposed = true
	return nil
}
func (p *performanceProducer) closeBenchmarks(ctx context.Context) error {
	var errs []error
	for id, peer := range p.peers {
		if stats, ok := peer.runtime.PeerStatistics(id); ok {
			value, known := performanceRTPBytes(stats)
			p.closedPeerBytes += value
			if !known {
				p.peerStatsMissing = true
			}
		} else {
			p.peerStatsMissing = true
		}
		errs = append(errs, peer.negotiation.Abandon())
		delete(p.peers, id)
	}
	for _, r := range p.runtimes {
		errs = append(errs, r.Close(ctx))
	}
	for _, owned := range p.owned {
		errs = append(errs, p.disposeOwned(ctx, owned))
		if owned.fixture != nil && !owned.fixtureClosed {
			err := owned.fixture.Stop(ctx)
			owned.fixtureClosed = err == nil
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func (p *performanceProducer) finish(cause error) performanceResult {
	p.finishOnce.Do(func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.closed = true
		cleanup, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		err := p.closeBenchmarks(cleanup)
		shared, _, captureErrors := p.capture.snapshot(cleanup)
		p.shared.TotalCaptureOpens = shared.TotalCaptureOpens
		p.errors = append(p.errors, captureErrors...)
		_, listErr := p.awaitNativeIdle(cleanup)
		if listErr != nil {
			err = errors.Join(err, errors.New("native_resources_cleanup_unconfirmed"))
		}
		if shared.CaptureSessions != 0 {
			err = errors.Join(err, errors.New("performance_capture_not_closed"))
		}
		err = errors.Join(err, p.client.Close())
		if p.journal != nil {
			err = errors.Join(err, p.journal.Sync(), p.journal.Close())
		}
		if cause != nil {
			p.errors = append(p.errors, "performance_run_cancelled_or_failed")
		}
		if err != nil {
			p.errors = append(p.errors, "performance_cleanup_unconfirmed")
		}
		if p.config.Mode == "debug" {
			p.errors = append(p.errors, "debug_run_not_acceptance")
		} else if time.Since(p.started) < time.Duration(p.config.DurationMS)*time.Millisecond {
			p.errors = append(p.errors, "acceptance_duration_incomplete")
		}
		if len(p.cycles) != p.config.Cycles {
			p.errors = append(p.errors, "lifecycle_cycles_incomplete")
		}
		p.result = performanceResult{SystemGPU: p.systemGPU, Type: "performance-result", SchemaVersion: 1, Resources: p.resources, Shared: p.shared, Cycles: p.cycles, CleanupConfirmed: err == nil, Errors: p.errors}
	})
	return p.result
}
func (p *performanceProducer) runCycles(ctx context.Context, count int) ([]performanceCycle, error) {
	if len(p.peers) != 0 || count != p.config.Cycles || len(p.cycles) != 0 || p.closed {
		return nil, errors.New("invalid_cycle_phase")
	}
	if err := p.closeBenchmarks(ctx); err != nil {
		return nil, err
	}
	baseline, baselineErr := p.readProcess(p.nativePID)
	for i := 0; i < count; i++ {
		key := protocol.ResourceKey{BackendIdentity: p.backendID, WorkspaceID: p.workspaceID, RuntimeID: uuid.NewString(), UID: uint32(p.uid())}
		response, err := p.client.Call(ctx, native.Request{Operation: "ensure", Resource: key, Width: 1600, Height: 900})
		if err != nil {
			return p.cycles, err
		}
		if response.Display == nil {
			return p.cycles, errors.New("cycle_display_unavailable")
		}
		owned := &performanceOwned{key: key, epoch: response.Epoch, displayID: response.Display.ID}
		p.owned = append(p.owned, owned)
		cycleCapture, frameReceived, captureErr := p.exerciseCycleCapture(ctx, owned)
		err = errors.Join(captureErr, p.disposeOwned(ctx, owned))
		cycle := performanceCycle{CaptureOpened: cycleCapture, EncodedFrameReceived: frameReceived, Cycle: i + 1, Disposed: err == nil, FixtureExited: true}
		for _, o := range p.owned {
			if o.fixture != nil && !o.fixtureClosed {
				cycle.FixtureExited = false
			}
		}
		listed, listErr := p.awaitNativeIdle(ctx)
		if listErr == nil {
			n := 0
			for _, d := range listed.Displays {
				for _, own := range p.owned {
					if d.ID == own.displayID {
						n++
						break
					}
				}
			}
			cycle.ManagedDisplaysAfter = &n
		}
		current, readErr := p.readProcess(p.nativePID)
		if baselineErr == nil && readErr == nil && baseline.Start == current.Start && baseline.FD >= 0 && current.FD >= 0 {
			delta := current.FD - baseline.FD
			cycle.FDDelta = &delta
		}
		if listed.LiveResources != nil && listed.LiveResources.Available {
			callbacks, encoders := int(listed.LiveResources.ActiveCallbacks), int(listed.LiveResources.EncoderSessions)
			cycle.ActiveCallbacksAfter = &callbacks
			cycle.ActiveEncodersAfter = &encoders
			cycle.MeasurementsAvailable = cycle.FDDelta != nil && cycle.ManagedDisplaysAfter != nil
		}
		p.cycles = append(p.cycles, cycle)
		if err != nil || listErr != nil {
			return p.cycles, errors.Join(err, listErr)
		}
	}
	return p.cycles, nil
}

func (p *performanceProducer) uid() int { return os.Getuid() }

func (p *performanceProducer) exerciseCycleCapture(ctx context.Context, owned *performanceOwned) (bool, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sources, err := p.client.Sources(ctx, owned.key)
	if err != nil {
		return false, false, err
	}
	for _, source := range sources {
		if source.DisplayID == owned.displayID && source.Source.Kind == protocol.MirrorSourceVirtual {
			selected := mirror.EncodedSource{Binding: source.MirrorSourceBinding, GeometryRevision: source.GeometryRevision, DisplayID: source.DisplayID, Width: 1600, Height: 900, FPS: 30, Bitrate: 12000000, MaxLevelIDC: 40}
			sub, e := p.hub.Subscribe(ctx, selected)
			if e != nil {
				return false, false, e
			}
			sample, e := sub.Next(ctx)
			received := e == nil && len(sample.AnnexB) > 0
			if e == nil && !received {
				e = errors.New("cycle_encoded_frame_missing")
			}
			return true, received, errors.Join(e, sub.Close())
		}
	}
	return false, false, errors.New("cycle_source_missing")
}

func (p *performanceProducer) awaitNativeIdle(ctx context.Context) (native.Response, error) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := p.client.Call(ctx, native.Request{Operation: "list"})
		if err != nil {
			return response, err
		}
		counts := response.LiveResources
		if counts == nil || !counts.Available {
			return response, errors.New("native_resource_counter_unavailable")
		}
		if counts.ActiveCallbacks == 0 && counts.EncoderSessions == 0 && counts.CaptureSessions == 0 {
			return response, nil
		}
		select {
		case <-ctx.Done():
			return response, ctx.Err()
		case <-timer.C:
			return response, errors.New("native_resources_not_quiescent")
		case <-ticker.C:
		}
	}
}
