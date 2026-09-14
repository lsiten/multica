package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// CaptureHub shares one encoder per exact source/configuration binding.
type CaptureHub struct {
	mu       sync.Mutex
	provider StreamProvider
	sources  map[string]*captureEntry
}
type captureEntry struct {
	stream      EncodedStream
	cancel      context.CancelFunc
	done        chan struct{}
	subscribers map[*CaptureSubscription]bool
	err         error
	closing     bool
	ready       chan struct{}
}

// CaptureSubscription has a bounded GOP-aware queue independent of other readers.
type CaptureSubscription struct {
	hub    *CaptureHub
	key    string
	entry  *captureEntry
	queue  chan EncodedSample
	closed bool
	stop   func() bool
}

// NewCaptureHub creates a lazily started source registry.
func NewCaptureHub(provider StreamProvider) *CaptureHub {
	return &CaptureHub{provider: provider, sources: make(map[string]*captureEntry)}
}

// Subscribe retains a source until Close or cancellation of the subscriber context.
func (h *CaptureHub) Subscribe(ctx context.Context, source EncodedSource) (*CaptureSubscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := source.validate(); err != nil {
		return nil, err
	}
	source.ExcludedWindowIDs = append([]uint32(nil), source.ExcludedWindowIDs...)

	keySource := source
	if source.Binding.Source.Kind != "virtual" {
		keySource.Binding.Resource = protocol.ResourceKey{}
		keySource.Binding.Primary = false
	}
	keyBytes, err := json.Marshal(keySource)
	if err != nil {
		return nil, fmt.Errorf("mirror: source key: %w", err)
	}
	key := string(keyBytes)
	h.mu.Lock()
	entry := h.sources[key]
	if entry != nil && entry.closing {
		done := entry.done
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-done:
			return h.Subscribe(ctx, source)
		}
	}
	if entry != nil && entry.ready != nil {
		ready := entry.ready
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ready:
			return h.Subscribe(ctx, source)
		}
	}
	if entry == nil {
		if h.provider == nil {
			h.mu.Unlock()
			return nil, errors.New("mirror: encoded stream provider unavailable")
		}
		// Reserve the source before opening, without blocking other sources.
		streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		entry = &captureEntry{cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), subscribers: make(map[*CaptureSubscription]bool)}
		h.sources[key] = entry
		h.mu.Unlock()
		stopOpen := context.AfterFunc(ctx, cancel)
		stream, openErr := h.provider.Open(streamCtx, source)
		stopOpen()
		h.mu.Lock()
		if openErr != nil {
			cancel()
			delete(h.sources, key)
			close(entry.ready)
			close(entry.done)
			h.mu.Unlock()
			return nil, fmt.Errorf("mirror: open selected source: %w", openErr)
		}
		entry.stream = stream
		close(entry.ready)
		entry.ready = nil

		go h.run(streamCtx, key, entry)
	}
	sub := &CaptureSubscription{hub: h, key: key, entry: entry, queue: make(chan EncodedSample, 3)}
	entry.subscribers[sub] = true
	sub.stop = context.AfterFunc(ctx, func() { _ = sub.Close() })
	h.mu.Unlock()
	if err := entry.stream.ForceKeyframe(); err != nil {
		_ = sub.Close()
		return nil, fmt.Errorf("mirror: request initial keyframe: %w", err)
	}
	return sub, nil
}

func (h *CaptureHub) run(ctx context.Context, key string, e *captureEntry) {
	defer close(e.done)
	var terminal error
	defer func() {
		terminal = errors.Join(terminal, e.stream.Close())
		h.mu.Lock()
		defer h.mu.Unlock()
		e.err = terminal
		if h.sources[key] == e {
			delete(h.sources, key)
		}
		for s := range e.subscribers {
			drainSamples(s.queue)
			close(s.queue)
			s.closed = true
			if s.stop != nil {
				s.stop()
			}
		}
		clear(e.subscribers)
	}()
	for {
		sample, err := e.stream.Next(ctx)
		if err != nil {
			if !errors.Is(err, ctx.Err()) {
				terminal = err
			}
			return
		}
		sample.AnnexB = append([]byte(nil), sample.AnnexB...)
		requestKeyframe := false
		h.mu.Lock()
		for s, waiting := range e.subscribers {
			if waiting && !sample.KeyFrame {
				continue
			}
			select {
			case s.queue <- sample:
				e.subscribers[s] = false
			default:
				drainSamples(s.queue)
				e.subscribers[s] = true
				if sample.KeyFrame {
					s.queue <- sample
					e.subscribers[s] = false
				} else {
					requestKeyframe = true
				}
			}
		}
		h.mu.Unlock()
		if requestKeyframe {
			if err := e.stream.ForceKeyframe(); err != nil {
				terminal = err
				return
			}
		}
	}
}

// Next returns complete access units or the terminal source error.
func (s *CaptureSubscription) Next(ctx context.Context) (EncodedSample, error) {
	select {
	case <-ctx.Done():
		return EncodedSample{}, ctx.Err()
	case sample, ok := <-s.queue:
		if ok {
			return sample, nil
		}
		s.hub.mu.Lock()
		err := s.entry.err
		s.hub.mu.Unlock()
		if err == nil {
			err = ErrViewerClosed
		}
		return EncodedSample{}, err
	}
}

// ForceKeyframe forwards RTCP PLI/FIR to the shared encoder.
func (s *CaptureSubscription) ForceKeyframe() error { return s.entry.stream.ForceKeyframe() }

// Close releases this reader only; the final reader synchronously joins capture.
func (s *CaptureSubscription) Close() error {
	h := s.hub
	h.mu.Lock()
	if s.closed {
		closing := s.entry.closing
		done := s.entry.done
		h.mu.Unlock()
		if closing {
			<-done
		}
		return nil
	}
	s.closed = true
	if s.stop != nil {
		s.stop()
	}
	delete(s.entry.subscribers, s)
	drainSamples(s.queue)
	close(s.queue)
	last := len(s.entry.subscribers) == 0
	if last {
		s.entry.closing = true
		s.entry.cancel()
	}
	h.mu.Unlock()
	if last {
		<-s.entry.done
		h.mu.Lock()
		err := s.entry.err
		h.mu.Unlock()
		return err
	}
	return nil
}

func drainSamples(queue chan EncodedSample) {
	for {
		select {
		case <-queue:
		default:
			return
		}
	}
}
