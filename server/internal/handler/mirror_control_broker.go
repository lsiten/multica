package handler

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	mirrorControlIdleTimeout = 2 * time.Minute
	mirrorControlMaxLease    = 30 * time.Minute
	mirrorControlRequestTTL  = 10 * time.Minute
)

type mirrorControlLease struct {
	ID          string
	WorkspaceID string
	RuntimeID   string
	DaemonID    string
	UserID      string
	StartedAt   time.Time
	LastActive  time.Time
}

type mirrorControlRequest struct {
	ID          string
	WorkspaceID string
	RuntimeID   string
	UserID      string
	CreatedAt   time.Time
}

type mirrorControlStatus struct {
	State       string
	Lease       *mirrorControlLease
	Request     *mirrorControlRequest
	RequestedBy string
}

type mirrorControlBroker struct {
	mu       sync.Mutex
	active   map[string]*mirrorControlLease
	requests map[string]*mirrorControlRequest
}

func newMirrorControlBroker() *mirrorControlBroker {
	return &mirrorControlBroker{
		active:   make(map[string]*mirrorControlLease),
		requests: make(map[string]*mirrorControlRequest),
	}
}

func (b *mirrorControlBroker) status(runtimeID, userID string, canApprove bool, now time.Time) mirrorControlStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	lease := b.active[runtimeID]
	if lease != nil && mirrorControlExpired(lease, now) {
		delete(b.active, runtimeID)
		copy := *lease
		return mirrorControlStatus{State: "ended", Lease: &copy}
	}
	if lease != nil {
		state := "other_controller"
		if lease.UserID == userID {
			state = "controlling"
		}
		copy := *lease
		return mirrorControlStatus{State: state, Lease: &copy}
	}
	request := b.requestForRuntime(runtimeID, now)
	if request == nil {
		return mirrorControlStatus{State: "view_only"}
	}
	copy := *request
	if request.UserID == userID {
		return mirrorControlStatus{State: "waiting_for_approval", Request: &copy}
	}
	if canApprove {
		return mirrorControlStatus{State: "approval_pending", Request: &copy, RequestedBy: request.UserID}
	}
	return mirrorControlStatus{State: "other_controller", Request: &copy}
}

func (b *mirrorControlBroker) request(
	runtimeID, workspaceID, userID string,
	now time.Time,
) mirrorControlStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	lease := b.active[runtimeID]
	if lease != nil && !mirrorControlExpired(lease, now) {
		copy := *lease
		state := "other_controller"
		if lease.UserID == userID {
			state = "controlling"
		}
		return mirrorControlStatus{State: state, Lease: &copy}
	}
	delete(b.active, runtimeID)
	for _, request := range b.requests {
		if request.RuntimeID == runtimeID && !requestExpired(request, now) {
			copy := *request
			state := "other_controller"
			if request.UserID == userID {
				state = "waiting_for_approval"
			}
			return mirrorControlStatus{State: state, Request: &copy}
		}
	}
	request := &mirrorControlRequest{
		ID:          "mcr-" + uuid.NewString(),
		WorkspaceID: workspaceID,
		RuntimeID:   runtimeID,
		UserID:      userID,
		CreatedAt:   now,
	}
	b.requests[request.ID] = request
	copy := *request
	return mirrorControlStatus{State: "waiting_for_approval", Request: &copy}
}

func (b *mirrorControlBroker) pending(
	runtimeID, requestID string,
	now time.Time,
) (*mirrorControlRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	request := b.requests[requestID]
	if request == nil || request.RuntimeID != runtimeID {
		return nil, false
	}
	if requestExpired(request, now) {
		delete(b.requests, requestID)
		return nil, false
	}
	copy := *request
	return &copy, true
}

func (b *mirrorControlBroker) activate(
	request *mirrorControlRequest,
	daemonID, controlID string,
	now time.Time,
) (*mirrorControlLease, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active[request.RuntimeID] != nil {
		return nil, false
	}
	delete(b.requests, request.ID)
	lease := &mirrorControlLease{
		ID:          controlID,
		WorkspaceID: request.WorkspaceID,
		RuntimeID:   request.RuntimeID,
		DaemonID:    daemonID,
		UserID:      request.UserID,
		StartedAt:   now,
		LastActive:  now,
	}
	b.active[request.RuntimeID] = lease
	copy := *lease
	return &copy, true
}

func (b *mirrorControlBroker) activateDirect(
	workspaceID, runtimeID, daemonID, userID, controlID string,
	now time.Time,
) (*mirrorControlLease, bool) {
	return b.activate(&mirrorControlRequest{
		ID:          "direct-" + controlID,
		WorkspaceID: workspaceID,
		RuntimeID:   runtimeID,
		UserID:      userID,
		CreatedAt:   now,
	}, daemonID, controlID, now)
}

func (b *mirrorControlBroker) deny(runtimeID, requestID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	request := b.requests[requestID]
	if request == nil || request.RuntimeID != runtimeID {
		return false
	}
	delete(b.requests, requestID)
	return true
}

func (b *mirrorControlBroker) cancelRequest(runtimeID, userID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, request := range b.requests {
		if request.RuntimeID == runtimeID && request.UserID == userID {
			delete(b.requests, id)
			return true
		}
	}
	return false
}

func (b *mirrorControlBroker) touch(runtimeID, userID string, now time.Time) (*mirrorControlLease, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	lease := b.active[runtimeID]
	if lease == nil || lease.UserID != userID || mirrorControlExpired(lease, now) {
		delete(b.active, runtimeID)
		return nil, false
	}
	lease.LastActive = now
	copy := *lease
	return &copy, true
}

func (b *mirrorControlBroker) end(runtimeID, userID string) *mirrorControlLease {
	b.mu.Lock()
	defer b.mu.Unlock()
	lease := b.active[runtimeID]
	if lease == nil || (userID != "" && lease.UserID != userID) {
		return nil
	}
	delete(b.active, runtimeID)
	copy := *lease
	return &copy
}

func (b *mirrorControlBroker) requestForRuntime(runtimeID string, now time.Time) *mirrorControlRequest {
	for id, request := range b.requests {
		if request.RuntimeID != runtimeID {
			continue
		}
		if requestExpired(request, now) {
			delete(b.requests, id)
			continue
		}
		return request
	}
	return nil
}

func mirrorControlExpired(lease *mirrorControlLease, now time.Time) bool {
	return now.Sub(lease.LastActive) >= mirrorControlIdleTimeout ||
		now.Sub(lease.StartedAt) >= mirrorControlMaxLease
}

func requestExpired(request *mirrorControlRequest, now time.Time) bool {
	return now.Sub(request.CreatedAt) >= mirrorControlRequestTTL
}
