package mirror

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"
)

func TestControlHookReplayDoesNotHoldMirrorLockWhileWaitingForPeer(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	peer := newControlGrantTestPeer(t)
	m.peers["viewer"] = peer
	grant := testControlGrant("control", "display", time.Now().Add(time.Minute))
	if !m.BindControlGrant("viewer", grant, 1) {
		t.Fatal("bind")
	}
	peer.mu.Lock()
	replayDone := make(chan struct{})
	go func() {
		m.SetControlStateHook(func(ControlStateChange) {}, 1)
		close(replayDone)
	}()
	// Wait until replay is blocked at the held peer mutex, without changing the
	// production lock path or relying on a scheduler delay.
	deadline := time.Now().Add(time.Second)
	stack := make([]byte, 65536)
	for {
		n := runtime.Stack(stack, true)
		found := false
		for _, goroutine := range bytes.Split(stack[:n], []byte("\n\n")) {
			if bytes.Contains(goroutine, []byte("SetControlStateHook")) && bytes.Contains(goroutine, []byte("sync.(*Mutex).Lock")) {
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			peer.mu.Unlock()
			t.Fatal("replay did not reach peer mutex")
		}
		runtime.Gosched()
	}
	revoked := make(chan struct{})
	go func() {
		m.Arbiter()
		close(revoked)
	}()
	select {
	case <-revoked:
		peer.mu.Unlock()
	case <-time.After(time.Second):
		peer.mu.Unlock()
		<-revoked
		<-replayDone
		t.Fatal("replay blocks grant cleanup through inverted mirror/peer lock order")
	}
	<-replayDone
	m.RevokeControlGrant("viewer", grant.GrantID, 1)
	if m.HasControl("viewer") {
		t.Fatal("grant survived revoke during replay")
	}
}
