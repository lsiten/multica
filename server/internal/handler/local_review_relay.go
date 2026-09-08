package handler

import (
	"context"
	"errors"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const localReviewRelayCapacity = 64

var errLocalReviewRelayBusy = errors.New("local review relay is busy")

// localReviewRelay retains only active HTTP exchanges. It has no durable queue:
// disconnects remove unclaimed operations and late results are discarded.
// Mutating operations must recover from receipts on the owning runtime.
type localReviewRelay struct {
	mu       sync.Mutex
	sequence uint64
	pending  map[string]*localReviewExchange
}

type localReviewExchange struct {
	command   protocol.LocalReviewCommand
	sequence  uint64
	claimed   bool
	completed bool
	response  chan protocol.LocalReviewResult
}

func (relay *localReviewRelay) enqueue(command protocol.LocalReviewCommand) (*localReviewExchange, error) {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	if len(relay.pending) >= localReviewRelayCapacity {
		return nil, errLocalReviewRelayBusy
	}
	if relay.pending == nil {
		relay.pending = make(map[string]*localReviewExchange)
	}
	command.ID, command.ClaimToken = randomID(), randomID()
	relay.sequence++
	exchange := &localReviewExchange{command: command, sequence: relay.sequence, response: make(chan protocol.LocalReviewResult, 1)}
	relay.pending[command.ID] = exchange
	return exchange, nil
}

func (relay *localReviewRelay) wait(ctx context.Context, exchange *localReviewExchange) (protocol.LocalReviewResult, error) {
	defer func() {
		relay.mu.Lock()
		defer relay.mu.Unlock()
		delete(relay.pending, exchange.command.ID)
	}()
	select {
	case <-ctx.Done():
		return protocol.LocalReviewResult{}, ctx.Err()
	case result := <-exchange.response:
		return result, nil
	}
}

func (relay *localReviewRelay) claim(workspaceID, runtimeID string) *protocol.LocalReviewCommand {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	var oldest *localReviewExchange
	for _, exchange := range relay.pending {
		if exchange.claimed || exchange.command.WorkspaceID != workspaceID || exchange.command.RuntimeID != runtimeID {
			continue
		}
		if oldest == nil || exchange.sequence < oldest.sequence {
			oldest = exchange
		}
	}
	if oldest == nil {
		return nil
	}
	oldest.claimed = true
	command := oldest.command
	return &command
}

func (relay *localReviewRelay) complete(workspaceID, runtimeID, commandID string, result protocol.LocalReviewResult) bool {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	exchange := relay.pending[commandID]
	if exchange == nil || !exchange.claimed || exchange.command.WorkspaceID != workspaceID || exchange.command.RuntimeID != runtimeID || exchange.command.ClaimToken != result.ClaimToken {
		return false
	}
	if !exchange.completed {
		exchange.completed = true
		exchange.response <- result
	}
	return true
}
