package communications

import "testing"

func TestFileIdempotencyStorePersistsPendingAndCompletedCalls(t *testing.T) {
	path := t.TempDir() + "/phone.json"
	store, err := NewFileIdempotencyStore(path)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := store.Reserve("task-1:call-1")
	if err != nil || !reserved {
		t.Fatalf("reserve=%v err=%v", reserved, err)
	}
	reloaded, err := NewFileIdempotencyStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Reserve("task-1:call-1"); err != ErrAmbiguousOperation {
		t.Fatalf("pending retry err=%v", err)
	}
	if err := store.Complete("task-1:call-1", Call{SID: "CA1"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err = NewFileIdempotencyStore(path)
	if err != nil {
		t.Fatal(err)
	}
	call, found, pending := reloaded.Get("task-1:call-1")
	if !found || pending || call.SID != "CA1" {
		t.Fatalf("call=%+v found=%v pending=%v", call, found, pending)
	}
}

func TestFileIdempotencyRejectsConcurrentReservationAfterRestart(t *testing.T) {
	path := t.TempDir() + "/ledger"
	first, err := NewFileIdempotencyStore(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileIdempotencyStore(path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 2)
	for _, store := range []*FileIdempotencyStore{first, second} {
		go func() { ok, _ := store.ReserveFingerprint("operation", "hash"); done <- ok }()
	}
	a, b := <-done, <-done
	if a == b {
		t.Fatalf("exactly one reservation required: %v %v", a, b)
	}
	if err := first.Complete("operation", Call{SID: "CA1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ReserveFingerprint("operation", "different"); err != ErrIdempotencyConflict {
		t.Fatalf("err=%v", err)
	}
}
