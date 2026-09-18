package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
)

type performanceCapture struct {
	provider    mirror.StreamProvider
	mu          sync.Mutex
	streams     map[*performanceStream]bool
	opens       uint64
	bytes       atomic.Uint64
	closeErrors []string
}
type performanceStream struct {
	mirror.EncodedStream
	owner *performanceCapture
	once  sync.Once
	err   error
}

func (p *performanceCapture) Open(ctx context.Context, source mirror.EncodedSource) (mirror.EncodedStream, error) {
	stream, err := p.provider.Open(ctx, source)
	if err != nil {
		code := "capture_open_failed"
		if errors.Is(err, mirror.ErrCapturePermissionDenied) {
			code = "screen_recording_denied"
		}
		p.mu.Lock()
		p.closeErrors = append(p.closeErrors, code)
		p.mu.Unlock()
		return nil, err
	}
	s := &performanceStream{EncodedStream: stream, owner: p}
	p.mu.Lock()
	if p.streams == nil {
		p.streams = map[*performanceStream]bool{}
	}
	p.streams[s] = true
	p.opens++
	p.mu.Unlock()
	return s, nil
}
func (s *performanceStream) Next(ctx context.Context) (mirror.EncodedSample, error) {
	v, err := s.EncodedStream.Next(ctx)
	if err == nil {
		s.owner.bytes.Add(uint64(len(v.AnnexB)))
	}
	return v, err
}
func (s *performanceStream) Close() error {
	s.once.Do(func() {
		s.err = s.EncodedStream.Close()
		s.owner.mu.Lock()
		defer s.owner.mu.Unlock()
		if s.err != nil {
			s.owner.closeErrors = append(s.owner.closeErrors, "capture_close_unconfirmed")
		} else {
			delete(s.owner.streams, s)
		}
	})
	return s.err
}
func (p *performanceCapture) snapshot(ctx context.Context) (performanceShared, []*native.CaptureDescriptor, []string) {
	p.mu.Lock()
	shared := performanceShared{CaptureSessions: len(p.streams), TotalCaptureOpens: p.opens, Reason: "provider_opens_observed_native_encoder_instance_counter_unavailable"}
	streams := make([]*performanceStream, 0, len(p.streams))
	for s := range p.streams {
		streams = append(streams, s)
	}
	failures := append([]string(nil), p.closeErrors...)
	p.mu.Unlock()
	var stats []*native.CaptureDescriptor
	for _, s := range streams {
		if reader, ok := s.EncodedStream.(interface {
			CaptureStatus(context.Context) (*native.CaptureDescriptor, error)
		}); ok {
			value, err := reader.CaptureStatus(ctx)
			if err != nil {
				failures = append(failures, "capture_status_unavailable")
			} else {
				stats = append(stats, value)
			}
		}
	}
	return shared, stats, failures
}
