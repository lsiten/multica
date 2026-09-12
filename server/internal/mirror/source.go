package mirror

import (
	"context"
	"image"
	"sort"
	"strings"
	"sync"
	"time"
)

// Frame is one complete JPEG screenshot emitted by a runtime mirror source.
type Frame struct {
	ID     uint32
	Width  int
	Height int
	JPEG   []byte
}

// ViewerStateChange identifies one DataChannel viewer attaching to or
// detaching from the shared source.
type ViewerStateChange struct {
	ViewerID string
	Active   bool
}

// Capturer reads the runtime host's primary display. Implementations must not
// retain the returned image after the next Capture call.
type Capturer interface {
	Capture(ctx context.Context) (image.Image, error)
}

// FrameSink receives complete frames for one WebRTC viewer connection.
type FrameSink interface {
	SendFrame(frame Frame) error
}

// Source shares one runtime's screen across its active viewers. It starts
// capture lazily at the first viewer and stops it after the final viewer leaves.
type Source struct {
	capturer Capturer
	interval time.Duration

	mu                        sync.Mutex
	viewers                   map[string]*sourceViewer
	cancel                    context.CancelFunc
	done                      chan struct{}
	closeDone                 chan struct{}
	nextID                    uint32
	closed                    bool
	viewerStateHook           func(ViewerStateChange)
	viewerStateHookGeneration uint64
	captureFailureHook        func(error)
}

type sourceViewer struct {
	sink FrameSink
}

// NewSource constructs a source with a bounded capture cadence.
func NewSource(capturer Capturer, interval time.Duration) *Source {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	return &Source{
		capturer: capturer,
		interval: interval,
		viewers:  make(map[string]*sourceViewer),
	}
}

// AddViewer begins capture when needed and returns an idempotent detacher.
func (s *Source) AddViewer(viewerID string, sink FrameSink) func() {
	viewerID = strings.TrimSpace(viewerID)
	s.mu.Lock()
	if s.closed || viewerID == "" || sink == nil {
		s.mu.Unlock()
		return func() {}
	}
	if _, exists := s.viewers[viewerID]; exists {
		s.mu.Unlock()
		return func() {}
	}
	viewer := &sourceViewer{sink: sink}
	s.viewers[viewerID] = viewer
	if s.cancel == nil {
		s.startLocked()
	}
	hook := s.viewerStateHook
	if hook != nil {
		hook(ViewerStateChange{ViewerID: viewerID, Active: true})
	}
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() { s.removeViewer(viewerID, viewer) })
	}
}

func (s *Source) removeViewer(viewerID string, viewer *sourceViewer) {
	s.mu.Lock()
	if current, exists := s.viewers[viewerID]; !exists || current != viewer {
		s.mu.Unlock()
		return
	}
	delete(s.viewers, viewerID)
	becameIdle := len(s.viewers) == 0
	if becameIdle && s.cancel != nil {
		s.cancel()
	}
	hook := s.viewerStateHook
	if hook != nil {
		hook(ViewerStateChange{ViewerID: viewerID, Active: false})
	}
	s.mu.Unlock()
}

// SetViewerStateHook observes viewer attachments and detachments. It replays
// one active event per current viewer while holding the source lock so
// reconnecting a control connection cannot reorder replay against departure.
// A hook from an older control connection can never replace the current hook.
func (s *Source) SetViewerStateHook(hook func(ViewerStateChange), generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.viewerStateHookGeneration != 0 && generation < s.viewerStateHookGeneration {
		return false
	}
	s.viewerStateHookGeneration = generation
	s.viewerStateHook = hook
	active := len(s.viewers) > 0
	if !active || hook == nil {
		return active
	}
	viewerIDs := make([]string, 0, len(s.viewers))
	for viewerID := range s.viewers {
		viewerIDs = append(viewerIDs, viewerID)
	}
	sort.Strings(viewerIDs)
	for _, viewerID := range viewerIDs {
		hook(ViewerStateChange{ViewerID: viewerID, Active: true})
	}
	return true
}

// SetCaptureFailureHandler observes one terminal capture failure for the
// shared source. The callback receives metadata/errors only; frames never pass
// through it.
func (s *Source) SetCaptureFailureHandler(handler func(error)) {
	s.mu.Lock()
	s.captureFailureHook = handler
	s.mu.Unlock()
}

// HasViewers reports whether the shared capture source is active.
func (s *Source) HasViewers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.viewers) > 0
}

// Close stops capture and waits for the capture loop to terminate.
func (s *Source) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closeDone != nil {
		done := s.closeDone
		s.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.closed = true
	s.closeDone = make(chan struct{})
	s.viewers = make(map[string]*sourceViewer)
	if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	s.mu.Unlock()
	if done == nil {
		close(s.closeDone)
		return nil
	}
	select {
	case <-s.closeDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Source) run(ctx context.Context, done chan struct{}) {
	defer func() {
		s.mu.Lock()
		if s.done == done {
			s.done = nil
			s.cancel = nil
		}
		if !s.closed && len(s.viewers) > 0 && s.cancel == nil {
			s.startLocked()
		}
		close(done)
		if s.closed {
			close(s.closeDone)
		}
		s.mu.Unlock()
	}()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		if err := s.captureAndBroadcast(ctx); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Source) startLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(ctx, s.done)
}
