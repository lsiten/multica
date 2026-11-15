package nativecapture

import (
	"context"
	"sync"
)

type Stream struct {
	startDone  chan struct{}
	started    bool
	client     *Client
	selection  EncodedSelection
	options    CaptureOptions
	mu         sync.Mutex
	queue      []MediaSample
	waitingIDR bool
	err        error
	ready      chan struct{}
	done       chan struct{}
	keyframes  chan struct{}
	workerDone chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

func (s *Stream) request(operation string) Request {
	return Request{
		Operation: operation,
		Resource:  s.selection.Source.Resource,
		Epoch:     sourceEpoch(s.selection.Source),
		Capture:   &s.options,
	}
}

func (s *Stream) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	s.err = err
	s.queue = nil
	close(s.done)
}

func (s *Stream) push(sample MediaSample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	if len(s.queue) == 3 {
		s.queue = nil
		s.waitingIDR = true
		select {
		case s.keyframes <- struct{}{}:
		default:
		}
	}
	if s.waitingIDR && !sample.KeyFrame {
		return
	}
	if sample.KeyFrame {
		s.waitingIDR = false
	}
	s.queue = append(s.queue, sample)
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *Stream) Next(ctx context.Context) (MediaSample, error) {
	for {
		if err := ctx.Err(); err != nil {
			return MediaSample{}, err
		}
		s.mu.Lock()
		if s.err != nil {
			err := s.err
			s.mu.Unlock()
			return MediaSample{}, err
		}
		if len(s.queue) > 0 {
			sample := s.queue[0]
			s.queue[0] = MediaSample{}
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return sample, nil
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return MediaSample{}, ctx.Err()
		case <-s.done:
		case <-s.ready:
		}
	}
}

func (s *Stream) ForceKeyframe() error {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return err
	}
	_, err = s.client.Call(context.Background(), s.request("force_keyframe"))
	return err
}

func (s *Stream) requestKeyframes() {
	defer close(s.workerDone)
	for {
		select {
		case <-s.done:
			return
		case <-s.keyframes:
			if err := s.ForceKeyframe(); err != nil {
				s.finish(err)
				return
			}
		}
	}
}

func (s *Stream) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.client.shutdownTimeout)
	defer cancel()
	return s.close(ctx)
}

func (s *Stream) close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		<-s.startDone
		s.finish(ErrClosed)
		if !s.started {
			return
		}
		select {
		case <-s.client.closed:
		default:
			_, s.closeErr = s.client.Call(ctx, s.request("stop_capture"))
		}
		<-s.workerDone
	})
	return s.closeErr
}
