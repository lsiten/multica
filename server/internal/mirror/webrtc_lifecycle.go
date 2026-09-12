package mirror

import (
	"context"
	"errors"
	"fmt"
)

type peerCloseOptions struct {
	fromCallback     bool
	onlyUnattached   bool
	includeCommitted bool
}

func (m *RuntimeMirror) removePeer(viewerID string, peer *mirrorPeer, fromCallback bool) error {
	_, err := m.closePeer(viewerID, peer, peerCloseOptions{fromCallback: fromCallback})
	return err
}

func (m *RuntimeMirror) removeUnattachedPeer(
	viewerID string,
	peer *mirrorPeer,
	fromCallback bool,
	includeCommitted bool,
) (bool, error) {
	return m.closePeer(viewerID, peer, peerCloseOptions{
		fromCallback:     fromCallback,
		onlyUnattached:   true,
		includeCommitted: includeCommitted,
	})
}

func (m *RuntimeMirror) closePeer(viewerID string, peer *mirrorPeer, opts peerCloseOptions) (bool, error) {
	peer.mu.Lock()
	if peer.closed {
		done := peer.done
		peer.mu.Unlock()
		if opts.fromCallback {
			return false, nil
		}
		<-done
		peer.mu.Lock()
		err := peer.closeErr
		peer.mu.Unlock()
		return false, err
	}
	committed := peer.negotiationCommitted
	if opts.onlyUnattached && (peer.detach != nil || (!opts.includeCommitted && committed)) {
		peer.mu.Unlock()
		return false, nil
	}
	peer.closed = true
	if peer.attachTimer != nil {
		peer.attachTimer.Stop()
	}
	detach := peer.detach
	stopNegotiationCleanup := peer.stopNegotiationCleanup
	peer.detach = nil
	peer.stopNegotiationCleanup = nil
	peer.mu.Unlock()
	if stopNegotiationCleanup != nil {
		stopNegotiationCleanup()
	}

	peer.closeOnce.Do(func() {
		if detach != nil {
			detach()
		}
		m.mu.Lock()
		if current, ok := m.peers[viewerID]; ok && current == peer {
			delete(m.peers, viewerID)
		}
		m.mu.Unlock()
		if closeErr := peer.pc.Close(); closeErr != nil {
			peer.mu.Lock()
			peer.closeErr = fmt.Errorf("mirror: close viewer %q: %w", viewerID, closeErr)
			peer.mu.Unlock()
		}
		close(peer.done)
	})
	peer.mu.Lock()
	err := peer.closeErr
	peer.mu.Unlock()
	return true, err
}

// CloseUnattachedPeers closes negotiated peers whose DataChannel never opened.
// A control reconnect invokes it synchronously so a stale pre-answer peer cannot
// block the replacement handshake for the same viewer ID.
func (m *RuntimeMirror) CloseUnattachedPeers(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	refs := make([]peerRef, 0, len(m.peers))
	for viewerID, peer := range m.peers {
		refs = append(refs, peerRef{viewerID: viewerID, peer: peer})
	}
	m.mu.Unlock()

	var closeErr error
	for _, ref := range refs {
		_, err := m.removeUnattachedPeer(ref.viewerID, ref.peer, false, true)
		if err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

// Close idempotently closes all viewers and the shared capture source.
func (m *RuntimeMirror) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closeDone != nil {
		done := m.closeDone
		m.mu.Unlock()
		select {
		case <-done:
			m.mu.Lock()
			err := m.closeErr
			m.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.closed = true
	m.closeDone = make(chan struct{})
	peerRefs := make([]peerRef, 0, len(m.peers))
	for viewerID, peer := range m.peers {
		peerRefs = append(peerRefs, peerRef{viewerID: viewerID, peer: peer})
	}
	m.mu.Unlock()

	var closeErr error
	for _, ref := range peerRefs {
		if err := m.removePeer(ref.viewerID, ref.peer, false); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	if err := m.source.Close(ctx); err != nil {
		closeErr = errors.Join(closeErr, fmt.Errorf("mirror: close source: %w", err))
	}
	m.mu.Lock()
	m.closeErr = closeErr
	close(m.closeDone)
	m.mu.Unlock()
	return closeErr
}
