package mirror

import (
	"bytes"
	"context"
	"image/jpeg"
)

func (s *Source) captureAndBroadcast(ctx context.Context) error {
	image, err := s.capturer.Capture(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		hook := s.captureFailureHandler()
		if hook != nil {
			hook(err)
		}
		s.broadcastControl(captureControlMessage(err))
		return err
	}
	if image == nil {
		s.broadcastControl(captureControlMessage(ErrCaptureUnavailable))
		return ErrCaptureUnavailable
	}
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, image, &jpeg.Options{Quality: 60}); err != nil {
		encodeErr := wrapCaptureError(err)
		s.broadcastControl(captureControlMessage(encodeErr))
		return encodeErr
	}
	bounds := image.Bounds()
	s.mu.Lock()
	s.nextID++
	frame := Frame{
		ID:     s.nextID,
		Width:  bounds.Dx(),
		Height: bounds.Dy(),
		JPEG:   jpegData.Bytes(),
	}
	viewers := make(map[string]*sourceViewer, len(s.viewers))
	for id, viewer := range s.viewers {
		viewers[id] = viewer
	}
	s.mu.Unlock()

	for viewerID, viewer := range viewers {
		if err := viewer.sink.SendFrame(frame); err != nil {
			s.removeViewer(viewerID, viewer)
		}
	}
	return nil
}

func (s *Source) captureFailureHandler() func(error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.captureFailureHook
}

func (s *Source) broadcastControl(message ControlMessage) {
	s.mu.Lock()
	viewers := make(map[string]*sourceViewer, len(s.viewers))
	for id, viewer := range s.viewers {
		viewers[id] = viewer
	}
	s.mu.Unlock()

	for viewerID, viewer := range viewers {
		controlSink, ok := viewer.sink.(ControlSink)
		if ok {
			if err := controlSink.SendControl(message); err != nil {
				s.removeViewer(viewerID, viewer)
				continue
			}
		}
		s.removeViewer(viewerID, viewer)
	}
}
