package mirror

// setVideoViewer uses the existing replay/generation observer without starting
// the JPEG capturer. Hooks follow Source's serialized, metadata-only contract.
func (s *Source) setVideoViewer(viewerID string, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if active {
		s.videoViewers[viewerID] = struct{}{}
	} else {
		delete(s.videoViewers, viewerID)
	}
	if s.viewerStateHook != nil {
		s.viewerStateHook(ViewerStateChange{ViewerID: viewerID, Active: active})
	}
}

func (s *Source) reportVideoFailure(err error) {
	s.mu.Lock()
	hook := s.captureFailureHook
	s.mu.Unlock()
	if hook != nil {
		hook(err)
	}
}
