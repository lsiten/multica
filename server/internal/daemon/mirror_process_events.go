package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const mirrorEventLimit = 128
const mirrorEventBytes = 256 << 10

// The reply channel is separate from serialized domain mutations. It confirms
// actual parent enqueue/persistence while the originating domain operation waits.
type mirrorEventBridge struct {
	token      string
	instance   string
	address    string
	server     *http.Server
	done       chan error
	stopped    chan struct{}
	once       sync.Once
	mu         sync.Mutex
	waiters    map[string]mirrorEventWaiter
	queue      chan mirrorBridgeEvent
	priority   chan mirrorBridgeEvent
	poll       func(uint64, bool) bool
	generation uint64
	bound      bool
}
type mirrorEventWaiter struct {
	generation uint64
	reply      chan mirrorBridgeReply
	deadline   time.Time
	safety     bool
}

func newMirrorEventBridge(token, instance string, poll func(uint64, bool) bool) (*mirrorEventBridge, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	bridge := &mirrorEventBridge{token: token, instance: instance, address: "http://" + listener.Addr().String(), done: make(chan error, 1), stopped: make(chan struct{}), waiters: map[string]mirrorEventWaiter{}, queue: make(chan mirrorBridgeEvent, mirrorEventLimit), priority: make(chan mirrorBridgeEvent, mirrorEventLimit+4), poll: poll}
	bridge.server = &http.Server{Handler: http.HandlerFunc(bridge.handle), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 7 * time.Second, WriteTimeout: 7 * time.Second, IdleTimeout: 3 * time.Second, MaxHeaderBytes: 8192}
	go func() { bridge.done <- bridge.server.Serve(listener) }()
	return bridge, nil
}
func (b *mirrorEventBridge) close() error {
	var result error
	b.once.Do(func() { close(b.stopped); result = b.server.Close(); <-b.done })
	return result
}
func (b *mirrorEventBridge) emit(ctx context.Context, generation uint64, kind string, payload any) (json.RawMessage, string, error) {
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > mirrorEventBytes {
		return nil, "", errors.New("mirror event exceeds bound")
	}
	id, err := randomBrokerToken()
	if err != nil {
		return nil, "", err
	}
	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	deadline, _ := timeout.Deadline()
	event := mirrorBridgeEvent{InstanceID: b.instance, ID: id, Generation: generation, Kind: kind, Payload: raw, Deadline: deadline}
	safety := kind == "stop_task" || kind == "native_fault"
	waiter := mirrorEventWaiter{generation: generation, reply: make(chan mirrorBridgeReply, 1), deadline: deadline, safety: safety}
	b.mu.Lock()
	if generation != b.generation || !b.bound && !safety {
		b.mu.Unlock()
		return nil, id, errors.New("mirror event binding inactive")
	}
	limit := mirrorEventLimit
	if safety {
		limit = 2*mirrorEventLimit + 4
	}
	if len(b.waiters) >= limit {
		b.mu.Unlock()
		return nil, id, errors.New("mirror event queue full")
	}
	b.waiters[id] = waiter
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.waiters, id); b.mu.Unlock() }()
	queue := b.queue
	if safety {
		queue = b.priority
	}
	select {
	case queue <- event:
	default:
		return nil, id, errors.New("mirror event queue full")
	}
	select {
	case <-timeout.Done():
		return nil, id, timeout.Err()
	case <-b.stopped:
		return nil, id, errors.New("mirror event channel closed")
	case reply := <-waiter.reply:
		if reply.Error != "" {
			return nil, id, errors.New(reply.Error)
		}
		return reply.Result, id, nil
	}
}
func (b *mirrorEventBridge) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.token)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, mirrorEventBytes))
	switch r.URL.Path {
	case "/events", "/unbind":
		var poll mirrorBridgePoll
		if decoder.Decode(&poll) != nil || decoder.Decode(new(any)) != io.EOF || poll.InstanceID != b.instance || !b.poll(poll.Generation, poll.Renew && r.URL.Path != "/unbind") {
			http.Error(w, "stale mirror binding", 409)
			return
		}
		if r.URL.Path == "/unbind" {
			w.WriteHeader(204)
			return
		}
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		for {
			var event mirrorBridgeEvent
			select {
			case event = <-b.priority:
			default:
				select {
				case event = <-b.priority:
				case event = <-b.queue:
				case <-deadline.C:
					w.WriteHeader(204)
					return
				case <-r.Context().Done():
					return
				case <-b.stopped:
					w.WriteHeader(503)
					return
				}
			}
			b.mu.Lock()
			waiter, live := b.waiters[event.ID]
			current, bound := b.generation, b.bound
			b.mu.Unlock()
			if !live || !event.Deadline.After(time.Now()) {
				continue
			}
			if event.Generation != current || !bound && !waiter.safety {
				select {
				case waiter.reply <- mirrorBridgeReply{ID: event.ID, Generation: event.Generation, Error: "mirror binding retired"}:
				default:
				}
				continue
			}
			if poll.Generation != current {
				queue := b.queue
				if waiter.safety {
					queue = b.priority
				}
				select {
				case queue <- event:
				default:
				}
				http.Error(w, "mirror generation replaced", 409)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(event)
			return
		}

	case "/reply":
		var reply mirrorBridgeReply
		if decoder.Decode(&reply) != nil || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "malformed reply", 400)
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		waiter, ok := b.waiters[reply.ID]
		if reply.InstanceID != b.instance || !b.bound && !waiter.safety || reply.Generation != b.generation || !ok || waiter.generation != reply.Generation || !waiter.deadline.After(time.Now()) {
			http.Error(w, "stale mirror reply", 409)
			return
		}
		select {
		case waiter.reply <- reply:
			w.WriteHeader(204)
		default:
			http.Error(w, "reply already supplied", 409)
		}
	default:
		http.NotFound(w, r)
	}
}

func (b *mirrorEventBridge) setGeneration(generation uint64, bound bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.generation = generation
	b.bound = bound
	for id, waiter := range b.waiters {
		if waiter.generation != generation || !bound && !waiter.safety {
			select {
			case waiter.reply <- mirrorBridgeReply{InstanceID: b.instance, ID: id, Generation: waiter.generation, Error: "mirror control binding retired"}:
			default:
			}
		}
	}
}
