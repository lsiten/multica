package runtimeproc

import (
	"context"
	"slices"
)

// admit never holds the journal mutex while waiting or running a domain handler.
// Lifecycle writers first exclude new ordinary work. While an already admitted
// ordinary handler remains active, confirmations may enter to release its domain
// guard. Once it returns, writer preference closes confirmation admission too.
// This dependency window is necessary: a conventional writer-preferring RWMutex
// would deadlock stop -> ordinary -> confirmation. Every wait is caller bounded.
func (s *Service) admit(ctx context.Context, operation string) (func(), error) {
	lifecycle := isBuiltin(operation)
	concurrent := slices.Contains(s.config.ConcurrentCapabilities, operation)
	s.admission.Lock()
	if lifecycle {
		s.lifecycleWaiting++
	}
	for {
		if err := ctx.Err(); err != nil {
			if lifecycle {
				s.lifecycleWaiting--
				s.signalAdmissionLocked()
			}
			s.admission.Unlock()
			return nil, err
		}
		active := s.concurrentActive
		if s.ordinaryActive {
			active++
		}
		allowed := false
		switch {
		case lifecycle:
			allowed = !s.lifecycleActive && active == 0
		case concurrent:
			allowed = !s.lifecycleActive && active < 4 && (s.lifecycleWaiting == 0 || s.ordinaryActive)
		default:
			allowed = !s.lifecycleActive && !s.ordinaryActive && active < 4 && s.lifecycleWaiting == 0
		}
		if allowed {
			switch {
			case lifecycle:
				s.lifecycleWaiting--
				s.lifecycleActive = true
			case concurrent:
				s.concurrentActive++
			default:
				s.ordinaryActive = true
			}
			s.admission.Unlock()
			return func() {
				s.admission.Lock()
				switch {
				case lifecycle:
					s.lifecycleActive = false
				case concurrent:
					s.concurrentActive--
				default:
					s.ordinaryActive = false
				}
				s.signalAdmissionLocked()
				s.admission.Unlock()
			}, nil
		}
		changed := s.admissionChanged
		s.admission.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		s.admission.Lock()
	}
}
func (s *Service) signalAdmissionLocked() {
	close(s.admissionChanged)
	s.admissionChanged = make(chan struct{})
}
