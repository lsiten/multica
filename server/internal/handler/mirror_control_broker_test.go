package handler

import (
	"testing"
	"time"
)

func TestMirrorControlBrokerRequiresApprovalAndExpiresByIdle(t *testing.T) {
	// Given
	broker := newMirrorControlBroker()
	now := time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)

	// When
	requested := broker.request("runtime-1", "workspace-1", "member-1", now)
	ownerView := broker.status("runtime-1", "owner-1", true, now.Add(time.Second))

	// Then
	if requested.State != "waiting_for_approval" || requested.Request == nil {
		t.Fatalf("request state=%+v", requested)
	}
	if ownerView.State != "approval_pending" || ownerView.RequestedBy != "member-1" {
		t.Fatalf("owner view=%+v", ownerView)
	}
	pending, ok := broker.pending("runtime-1", requested.Request.ID, now.Add(time.Second))
	if !ok {
		t.Fatal("pending request was not available for approval")
	}
	lease, activated := broker.activate(pending, "daemon-1", "control-1", now.Add(time.Second))
	if !activated {
		t.Fatal("approved request did not activate")
	}
	if lease.UserID != "member-1" {
		t.Fatalf("lease user=%q", lease.UserID)
	}
	if view := broker.status("runtime-1", "member-1", false, now.Add(time.Minute)); view.State != "controlling" {
		t.Fatalf("controlling state=%+v", view)
	}
	if _, active := broker.touch("runtime-1", "member-1", now.Add(time.Minute+mirrorControlIdleTimeout)); active {
		t.Fatal("expired idle lease remained active")
	}
}

func TestMirrorControlBrokerCancelsPendingRequest(t *testing.T) {
	// Given
	broker := newMirrorControlBroker()
	now := time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)
	requested := broker.request("runtime-1", "workspace-1", "member-1", now)

	// When
	cancelled := broker.cancelRequest("runtime-1", "member-1")

	// Then
	if !cancelled || requested.Request == nil {
		t.Fatal("pending request was not cancelled")
	}
	if _, ok := broker.pending("runtime-1", requested.Request.ID, now.Add(time.Second)); ok {
		t.Fatal("cancelled request remained pending")
	}
}

func TestMirrorControlBrokerDistinguishesSelfReleaseFromForceEnd(t *testing.T) {
	// Given
	broker := newMirrorControlBroker()
	now := time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)
	if _, activated := broker.activateDirect(
		"workspace-1",
		"runtime-1",
		"daemon-1",
		"controller-1",
		"control-1",
		now,
	); !activated {
		t.Fatal("test lease did not activate")
	}

	// When
	selfRelease := broker.end("runtime-1", "someone-else")
	forceRelease := broker.end("runtime-1", "")

	// Then
	if selfRelease != nil {
		t.Fatal("an unrelated member ended another controller's lease")
	}
	if forceRelease == nil || forceRelease.UserID != "controller-1" {
		t.Fatalf("force release=%+v", forceRelease)
	}
	if _, active := broker.touch("runtime-1", "controller-1", now.Add(time.Second)); active {
		t.Fatal("force-ended lease remained active")
	}
}
