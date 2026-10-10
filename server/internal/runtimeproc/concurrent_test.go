package runtimeproc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type concurrentReply struct {
	response Response
	err      error
}

func asyncConcurrentCall(c *Client, ctx context.Context, r Request) <-chan concurrentReply {
	done := make(chan concurrentReply, 1)
	go func() { out, err := c.Call(ctx, r); done <- concurrentReply{out, err} }()
	return done
}
func awaitConcurrent(t *testing.T, predicate func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !predicate() {
		select {
		case <-ctx.Done():
			t.Fatal("observable admission state did not converge")
		case <-ticker.C:
		}
	}
}
func concurrentCode(t *testing.T, reply concurrentReply, code string) {
	t.Helper()
	var problem *Error
	if !errors.As(reply.err, &problem) || problem.Code != code {
		t.Fatalf("error=%v want=%s", reply.err, code)
	}
}
func TestConcurrentCapabilitiesValidation(t *testing.T) {
	for _, selected := range [][]string{{"missing"}, {"read"}, {"stop"}, {"confirm", "confirm"}} {
		_, err := NewService(Config{Bootstrap: testBootstrap(t), Capabilities: []string{"confirm"}, ConcurrentCapabilities: selected, ReadCapabilities: []string{"read"}, Handler: func(context.Context, Request) (json.RawMessage, *Error) { return nil, nil }, ReadHandler: func(context.Context, Request) (json.RawMessage, *Error) { return nil, nil }})
		if err == nil {
			t.Fatalf("invalid concurrent subset accepted: %v", selected)
		}
	}
}
func TestConcurrentConfirmationUnblocksOrdinaryWithLifecycleWaiting(t *testing.T) {
	for _, lifecycle := range []string{"acknowledge", "stop", "drain", "resume"} {
		t.Run(lifecycle, func(t *testing.T) {
			entered := make(chan struct{}, 2)
			guard := make(chan struct{}, 1)
			var shutdown atomic.Bool
			s, c, _, _ := startService(t, Config{Bootstrap: testBootstrap(t), Capabilities: []string{"prepare", "physical_finish_confirm"}, ConcurrentCapabilities: []string{"physical_finish_confirm"}, Shutdown: func(context.Context) error { shutdown.Store(true); return nil }, Handler: func(ctx context.Context, r Request) (json.RawMessage, *Error) {
				if r.Operation == "prepare" {
					entered <- struct{}{}
					select {
					case <-guard:
					case <-ctx.Done():
						return nil, nil
					}
				} else {
					guard <- struct{}{}
				}
				return json.RawMessage(`{}`), nil
			}})
			ordinary := request(t, c, "prepare", `{}`)
			prepareDone := asyncConcurrentCall(c, t.Context(), ordinary)
			<-entered
			life := request(t, c, lifecycle, "")
			lifeDone := asyncConcurrentCall(c, t.Context(), life)
			awaitConcurrent(t, func() bool { s.admission.Lock(); defer s.admission.Unlock(); return s.lifecycleWaiting == 1 })
			ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
			defer cancel()
			blocked := request(t, c, "prepare", `{}`)
			_, err := c.Call(ctx, blocked)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("new ordinary entered despite lifecycle preference: %v", err)
			}
			confirm := call(t, c, request(t, c, "physical_finish_confirm", `{}`))
			result := <-prepareDone
			if result.err != nil || result.response.Receipt.State != "completed" {
				t.Fatalf("ordinary did not settle: %v", result.err)
			}
			if confirm.Receipt.Fence.Revision == result.response.Receipt.Fence.Revision || confirm.Receipt.Fence.Revision+result.response.Receipt.Fence.Revision != 5 {
				t.Fatal("completion revisions are not unique monotonic2,3")
			}
			concurrentCode(t, <-lifeDone, "stale_fence")
			if shutdown.Load() {
				t.Fatal("old-fence stop ran shutdown")
			}
			select {
			case <-entered:
				t.Fatal("new ordinary handler entered")
			default:
			}
			status, err := c.Health(t.Context())
			if err != nil || status.State != "ready" {
				t.Fatal("stale lifecycle altered state")
			}
			for _, id := range []string{ordinary.RequestID, confirm.Receipt.RequestID} {
				receipt, err := c.QueryOperation(t.Context(), id)
				if err != nil || receipt.State != "completed" {
					t.Fatal("lifecycle retired concurrent receipt")
				}
			}
		})
	}
}
func TestConcurrentPendingDuplicateQuotaAndACK(t *testing.T) {
	entered := make(chan struct{}, 4)
	var calls atomic.Int32
	s, c, _, _ := startService(t, Config{Bootstrap: testBootstrap(t), Capabilities: []string{"confirm"}, ConcurrentCapabilities: []string{"confirm"}, Handler: func(ctx context.Context, r Request) (json.RawMessage, *Error) {
		calls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return nil, nil
	}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := request(t, c, "confirm", `{}`)
	done := asyncConcurrentCall(c, ctx, first)
	<-entered
	duplicate := call(t, c, first)
	if duplicate.Receipt.State != "pending" || calls.Load() != 1 {
		t.Fatal("pending duplicate reran handler")
	}
	conflict := first
	conflict.Payload = json.RawMessage(`{"different":true}`)
	wantCode(t, c, conflict, "request_conflict")
	ack := request(t, c, "acknowledge", "")
	ackDone := asyncConcurrentCall(c, t.Context(), ack)
	awaitConcurrent(t, func() bool { s.admission.Lock(); defer s.admission.Unlock(); return s.lifecycleWaiting == 1 })
	short, stop := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer stop()
	_, err := c.Call(short, request(t, c, "confirm", `{}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("confirmation crossed writer barrier: %v", err)
	}
	cancel()
	<-done
	concurrentCode(t, <-ackDone, "uncertain_operation")
	receipt, err := c.QueryOperation(t.Context(), first.RequestID)
	if err != nil || receipt.State != "pending" {
		t.Fatal("ACK retired uncertain receipt")
	}
}
func TestConcurrentActiveLimitAndDefaultSerialization(t *testing.T) {
	for _, opted := range []bool{false, true} {
		t.Run(map[bool]string{false: "default_serial", true: "four_total"}[opted], func(t *testing.T) {
			entered := make(chan struct{}, 5)
			var active, maximum atomic.Int32
			cfg := Config{Bootstrap: testBootstrap(t), Capabilities: []string{"work"}, Handler: func(ctx context.Context, r Request) (json.RawMessage, *Error) {
				n := active.Add(1)
				for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
				}
				entered <- struct{}{}
				<-ctx.Done()
				active.Add(-1)
				return nil, nil
			}}
			want := 1
			if opted {
				cfg.ConcurrentCapabilities = []string{"work"}
				want = 4
			}
			_, c, _, _ := startService(t, cfg)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dones := []<-chan concurrentReply{}
			for range want {
				dones = append(dones, asyncConcurrentCall(c, ctx, request(t, c, "work", `{}`)))
				<-entered
			}
			short, stop := context.WithTimeout(t.Context(), 40*time.Millisecond)
			defer stop()
			_, err := c.Call(short, request(t, c, "work", `{}`))
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("active bound bypassed: %v", err)
			}
			if maximum.Load() != int32(want) {
				t.Fatalf("active maximum=%d want=%d", maximum.Load(), want)
			}
			cancel()
			for _, done := range dones {
				<-done
			}
			awaitConcurrent(t, func() bool { return active.Load() == 0 })
		})
	}
}
func TestConcurrentStorageFailureKeepsSuspectAndPending(t *testing.T) {
	b := testBootstrap(t)
	path := RecordPath(b.Root, b.Identity.Scope)
	backup := path + ".backup"
	entered := make(chan struct{})
	release := make(chan struct{})
	_, c, _, _ := startService(t, Config{Bootstrap: b, Capabilities: []string{"slow", "break"}, ConcurrentCapabilities: []string{"slow", "break"}, Handler: func(ctx context.Context, r Request) (json.RawMessage, *Error) {
		if r.Operation == "slow" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		} else {
			if err := os.Rename(path, backup); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Error(err)
			}
		}
		return json.RawMessage(`{}`), nil
	}})
	slow := request(t, c, "slow", `{}`)
	done := asyncConcurrentCall(c, t.Context(), slow)
	<-entered
	broken := request(t, c, "break", `{}`)
	wantCode(t, c, broken, "storage")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	close(release)
	if result := <-done; result.err != nil {
		t.Fatal(result.err)
	}
	status, err := c.Health(t.Context())
	if err != nil || status.State != "suspect" {
		t.Fatal("later completion erased suspect state")
	}
	receipt, err := c.QueryOperation(t.Context(), broken.RequestID)
	if err != nil || receipt.State != "pending" {
		t.Fatal("undurable outcome exposed")
	}
	disk, err := ReadRecord(b.Root, b.Identity)
	if err != nil || disk.State != "suspect" || disk.Operations[broken.RequestID].State != "pending" {
		t.Fatal("later durable write lost suspect intent")
	}
}

func TestConcurrentStopJoinsCanceledHandler(t *testing.T) {
	entered := make(chan struct{})
	var active atomic.Bool
	s, c, _, _ := startService(t, Config{Bootstrap: testBootstrap(t), Capabilities: []string{"confirm"}, ConcurrentCapabilities: []string{"confirm"}, Handler: func(ctx context.Context, r Request) (json.RawMessage, *Error) {
		active.Store(true)
		defer active.Store(false)
		close(entered)
		<-ctx.Done()
		return nil, nil
	}, Shutdown: func(context.Context) error {
		if active.Load() {
			t.Error("shutdown crossed active handler")
		}
		return nil
	}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	mutation := request(t, c, "confirm", `{}`)
	done := asyncConcurrentCall(c, ctx, mutation)
	<-entered
	stop := request(t, c, "stop", "")
	stopped := asyncConcurrentCall(c, t.Context(), stop)
	awaitConcurrent(t, func() bool { s.admission.Lock(); defer s.admission.Unlock(); return s.lifecycleWaiting == 1 })
	cancel()
	<-done
	result := <-stopped
	if result.err != nil || result.response.Status.State != "stopped" || active.Load() {
		t.Fatalf("stop did not join: %v", result.err)
	}
	disk, err := ReadRecord(s.config.Bootstrap.Root, s.config.Bootstrap.Identity)
	if err != nil || disk.State != "stopped" || disk.Operations[mutation.RequestID].State != "pending" {
		t.Fatal("stopped owner lost canceled intent")
	}
}

func TestConcurrentACKExcludesNewIntentDuringRetirement(t *testing.T) {
	var calls atomic.Int32
	s, c, _, _ := startService(t, Config{Bootstrap: testBootstrap(t), Capabilities: []string{"confirm"}, ConcurrentCapabilities: []string{"confirm"}, Handler: func(context.Context, Request) (json.RawMessage, *Error) {
		calls.Add(1)
		return json.RawMessage(`{}`), nil
	}})
	ack := request(t, c, "acknowledge", "")
	mutation := request(t, c, "confirm", `{}`)
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	ackDone := asyncConcurrentCall(c, t.Context(), ack)
	awaitConcurrent(t, func() bool { s.admission.Lock(); defer s.admission.Unlock(); return s.lifecycleActive })
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	_, err := c.Call(ctx, mutation)
	cancel()
	s.mu.Unlock()
	locked = false
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mutation crossed ACK exclusion: %v", err)
	}
	result := <-ackDone
	if result.err != nil || result.response.Status.ReplayEpoch != 2 {
		t.Fatalf("ACK did not complete: %v", result.err)
	}
	disk, err := ReadRecord(s.config.Bootstrap.Root, s.config.Bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || len(disk.Operations) != 1 || disk.Operations[ack.RequestID].State != "completed" {
		t.Fatal("ACK retired a concurrently admitted mutation")
	}
	call(t, c, request(t, c, "confirm", `{}`))
	if calls.Load() != 1 {
		t.Fatal("new replay epoch did not admit fresh mutation")
	}
}

func TestConcurrentDeadlineWhileWaitingJournalNeverCallsDomain(t *testing.T) {
	var calls atomic.Int32
	s, c, _, _ := startService(t, Config{Bootstrap: testBootstrap(t), Capabilities: []string{"confirm"}, ConcurrentCapabilities: []string{"confirm"}, Handler: func(context.Context, Request) (json.RawMessage, *Error) { calls.Add(1); return nil, nil }})
	mutation := request(t, c, "confirm", `{}`)
	mutation.Deadline = time.Now().Add(80 * time.Millisecond)
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := asyncConcurrentCall(c, ctx, mutation)
	awaitConcurrent(t, func() bool { s.admission.Lock(); defer s.admission.Unlock(); return s.concurrentActive == 1 })
	result := <-done
	s.mu.Unlock()
	locked = false
	if !errors.Is(result.err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", result.err)
	}
	awaitConcurrent(t, func() bool { s.admission.Lock(); defer s.admission.Unlock(); return s.concurrentActive == 0 })
	if calls.Load() != 0 {
		t.Fatal("expired request invoked domain")
	}
	_, err := c.QueryOperation(t.Context(), mutation.RequestID)
	var problem *Error
	if !errors.As(err, &problem) || problem.Code != "not_found" {
		t.Fatalf("expired waiting request was admitted: %v", err)
	}
}
