package mirror

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type encodedFixture struct {
	samples  chan EncodedSample
	requests atomic.Int32
	closed   atomic.Bool
	failure  error
}

func (f *encodedFixture) Next(ctx context.Context) (EncodedSample, error) {
	select {
	case <-ctx.Done():
		return EncodedSample{}, ctx.Err()
	case s, ok := <-f.samples:
		if !ok {
			return s, f.failure
		}
		return s, nil
	}
}
func (f *encodedFixture) ForceKeyframe() error { f.requests.Add(1); return nil }
func (f *encodedFixture) Close() error         { f.closed.Store(true); return nil }

type fixtureProvider struct {
	mu      sync.Mutex
	streams []*encodedFixture
}

func (p *fixtureProvider) Open(_ context.Context, _ EncodedSource) (EncodedStream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := &encodedFixture{samples: make(chan EncodedSample, 16), failure: errors.New("display disconnected")}
	p.streams = append(p.streams, s)
	return s, nil
}
func fixtureSource() EncodedSource {
	return EncodedSource{Binding: protocol.MirrorSourceBinding{Resource: protocol.ResourceKey{BackendIdentity: "https://example.test", WorkspaceID: "ws", RuntimeID: "runtime", UID: 501}, Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "display-1"}, NativeEpoch: "epoch", Generation: "display-generation"}, GeometryRevision: 1, MaxLevelIDC: 40, DisplayID: 1, Width: 1600, Height: 900, FPS: 30, Bitrate: 4000000}
}
func readSample(t *testing.T, s *CaptureSubscription) EncodedSample {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sample, err := s.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return sample
}
func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if predicate() {
			return
		}
		select {
		case <-timer.C:
			t.Fatal("condition timeout")
		case <-tick.C:
		}
	}
}

func TestCaptureHubSharedObserversAndSelectedSource(t *testing.T) {
	p := &fixtureProvider{}
	hub := NewCaptureHub(p)
	ctx := context.Background()
	source := fixtureSource()
	ai, err := hub.Subscribe(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer ai.Close()
	viewer, err := hub.Subscribe(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	otherRuntime := source
	otherRuntime.Binding.Resource.RuntimeID = "other-runtime"
	projected, err := hub.Subscribe(ctx, otherRuntime)
	if err != nil {
		t.Fatal(err)
	}
	defer projected.Close()
	if len(p.streams) != 1 {
		t.Fatalf("physical projections opened %d streams", len(p.streams))
	}
	other := source
	other.DisplayID = 2
	other.Binding.Source.SourceID = "display-2"
	selected, err := hub.Subscribe(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	if len(p.streams) != 2 {
		t.Fatal("different source shared encoder")
	}
	if err := viewer.Close(); err != nil {
		t.Fatal(err)
	}
	if p.streams[0].closed.Load() {
		t.Fatal("viewer close stopped AI observer")
	}
	if err := ai.Close(); err != nil {
		t.Fatal(err)
	}
	if err := projected.Close(); err != nil {
		t.Fatal(err)
	}
	if !p.streams[0].closed.Load() || p.streams[1].closed.Load() {
		t.Fatal("incorrect independent source cleanup")
	}
	t.Log("one physical encoder across runtime projections; final subscriber closes only selected source")
}

func TestCaptureHubSlowReaderWaitsForIDR(t *testing.T) {
	p := &fixtureProvider{}
	hub := NewCaptureHub(p)
	fast, _ := hub.Subscribe(context.Background(), fixtureSource())
	defer fast.Close()
	slow, _ := hub.Subscribe(context.Background(), fixtureSource())
	defer slow.Close()
	stream := p.streams[0]
	for i := range 6 {
		stream.samples <- EncodedSample{AnnexB: []byte{byte(i)}, KeyFrame: i == 0}
		if got := readSample(t, fast); got.AnnexB[0] != byte(i) {
			t.Fatal("fast viewer stalled")
		}
	}
	waitFor(t, func() bool { return stream.requests.Load() > 2 })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := slow.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dependent delta leaked after overflow: %v", err)
	}
	stream.samples <- EncodedSample{AnnexB: []byte{99}, KeyFrame: true}
	if got := readSample(t, slow); !got.KeyFrame || got.AnnexB[0] != 99 {
		t.Fatal("reader did not resume on IDR")
	}
	t.Logf("fast reader received 6 units; slow reader resumed only IDR; keyframe requests=%d", stream.requests.Load())
}

func TestCaptureHubTerminalErrorAndCancelledSubscription(t *testing.T) {
	p := &fixtureProvider{}
	hub := NewCaptureHub(p)
	sub, err := hub.Subscribe(context.Background(), fixtureSource())
	if err != nil {
		t.Fatal(err)
	}
	close(p.streams[0].samples)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := sub.Next(ctx); err == nil || err.Error() != "display disconnected" {
		t.Fatalf("source error = %v", err)
	}
	if len(hub.sources) != 0 {
		t.Fatal("dead source retained")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := hub.Subscribe(canceled, fixtureSource()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type blockingSourceProvider struct {
	fixtureProvider
	opening chan struct{}
	release chan struct{}
}

func (p *blockingSourceProvider) Open(ctx context.Context, s EncodedSource) (EncodedStream, error) {
	if s.DisplayID == 2 {
		close(p.opening)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.release:
		}
	}
	return p.fixtureProvider.Open(ctx, s)
}

func TestCaptureHubOpeningOtherSourceDoesNotBlockFrames(t *testing.T) {
	p := &blockingSourceProvider{opening: make(chan struct{}), release: make(chan struct{})}
	hub := NewCaptureHub(p)
	ctx := context.Background()
	fast, err := hub.Subscribe(ctx, fixtureSource())
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()
	other := fixtureSource()
	other.DisplayID = 2
	other.Binding.Source.SourceID = "second"
	done := make(chan struct{})
	go func() {
		defer close(done)
		slow, err := hub.Subscribe(ctx, other)
		if err != nil {
			t.Error(err)
			return
		}
		_ = slow.Close()
	}()
	<-p.opening
	p.streams[0].samples <- EncodedSample{AnnexB: []byte{1}, KeyFrame: true}
	timed, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err = fast.Next(timed)
	close(p.release)
	<-done
	if err != nil {
		t.Fatalf("opening another source blocked an active encoder: %v", err)
	}
}

type unavailableProvider struct {
	calls    int
	selected EncodedSource
	err      error
}

func (p *unavailableProvider) Open(_ context.Context, source EncodedSource) (EncodedStream, error) {
	p.calls++
	p.selected = source
	return nil, p.err
}

func TestCaptureHubUnavailableSourceNeverFallsBack(t *testing.T) {
	unavailable := errors.New("selected display disconnected")
	p := &unavailableProvider{err: unavailable}
	hub := NewCaptureHub(p)
	source := fixtureSource()
	source.DisplayID = 9
	source.Binding.Source.SourceID = "missing-selected-display"
	if _, err := hub.Subscribe(context.Background(), source); !errors.Is(err, unavailable) {
		t.Fatalf("selected-source error lost: %v", err)
	}
	if p.calls != 1 || p.selected.DisplayID != 9 {
		t.Fatal("unavailable selected display triggered fallback")
	}
}

func TestCaptureHubOldEpochClosePreservesReplacement(t *testing.T) {
	p := &fixtureProvider{}
	hub := NewCaptureHub(p)
	source := fixtureSource()
	old, err := hub.Subscribe(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	source.Binding.NativeEpoch = "replacement-native"
	next, err := hub.Subscribe(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if len(p.streams) != 2 || !p.streams[0].closed.Load() || p.streams[1].closed.Load() {
		t.Fatal("old epoch cleanup affected replacement source")
	}
}
