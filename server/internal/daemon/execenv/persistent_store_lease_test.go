package execenv

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistentStoreLeaseSerializesUseAndRemoval(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	store := filepath.Join(t.TempDir(), "session")
	first, err := UsePersistentStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Finish(ctx, nil)
	second, err := UsePersistentStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Finish(ctx, nil)
	if release, ok, err := ReservePersistentStoreDeletion(ctx, store); err != nil || ok {
		if release != nil {
			release()
		}
		t.Fatalf("live store deletion admitted: %v %v", ok, err)
	}
	if err = first.Finish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if release, ok, err := ReservePersistentStoreDeletion(ctx, store); err != nil || ok {
		if release != nil {
			release()
		}
		t.Fatalf("second borrower lost: %v %v", ok, err)
	}
	if err = second.Finish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	release, ok, err := ReservePersistentStoreDeletion(ctx, store)
	if err != nil || !ok {
		t.Fatalf("unused store not reserved: %v %v", ok, err)
	}
	deadline, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	if lease, err := UsePersistentStore(deadline, store); err == nil {
		lease.Finish(ctx, nil)
		t.Fatal("store mounted during removal")
	}
	cancel()
	release()
	third, err := UsePersistentStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if err = third.Finish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("lease touched private store contents: %v", err)
	}
}
