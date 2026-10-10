package runtimeproc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReconcileRequiresExactExitedOwnerAndPreservesInterruptedArchive(t *testing.T) {
	cfg := launchFixture(t, "normal")
	p := startFixture(t, context.Background(), cfg)
	inspected := false
	inspect := func(context.Context, Record) (Reconciliation, error) {
		inspected = true
		return Reconciliation{Inventory: json.RawMessage(`{"descendants_confirmed_absent":true}`), Release: func() error { return nil }}, nil
	}
	if err := Reconcile(t.Context(), cfg.Bootstrap.Root, cfg.Bootstrap.Identity, inspect); err == nil || inspected {
		t.Fatal("live owner was reconciled")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	wrong := cfg.Bootstrap.Identity
	wrong.InstanceID = "00000000000000000000000000000000"
	if err := Reconcile(t.Context(), cfg.Bootstrap.Root, wrong, inspect); err == nil || inspected {
		t.Fatal("wrong owner was reconciled")
	}
	path := RecordPath(cfg.Bootstrap.Root, cfg.Bootstrap.Identity.Scope)
	prior, err := ReadRecord(cfg.Bootstrap.Root, cfg.Bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	prior.Operations["1:uncertain"] = Receipt{RequestID: "1:uncertain", State: "pending", Digest: "fixture"}
	if err = writeRecord(path, prior); err != nil {
		t.Fatal(err)
	}
	released := false
	inspect = func(context.Context, Record) (Reconciliation, error) {
		released = false
		return Reconciliation{Inventory: json.RawMessage(`{"descendants_confirmed_absent":true}`), Release: func() error { released = true; return nil }}, nil
	}
	fault := errors.New("fixture interrupted retirement")
	err = reconcile(t.Context(), cfg.Bootstrap.Root, cfg.Bootstrap.Identity, inspect, func(target string, record Record) error {
		if released {
			t.Fatal("domain lock released before durable writes")
		}
		if target == path {
			return fault
		}
		return writeRecord(target, record)
	})
	if !errors.Is(err, fault) || !released {
		t.Fatalf("fault cleanup %v", err)
	}
	archivePath := filepath.Join(filepath.Dir(path), "reconciled-"+cfg.Bootstrap.Identity.InstanceID+".json")
	original, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = Reconcile(t.Context(), cfg.Bootstrap.Root, cfg.Bootstrap.Identity, inspect); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(archivePath)
	if err != nil || string(original) != string(after) {
		t.Fatal("retry overwrote historical unknown receipt")
	}
	archived, err := ReadReconciledRecord(cfg.Bootstrap.Root, cfg.Bootstrap.Identity)
	if err != nil || archived.Operations["1:uncertain"].State != "pending" {
		t.Fatal("uncertain history was lost")
	}
	current, err := ReadRecord(cfg.Bootstrap.Root, cfg.Bootstrap.Identity)
	if err != nil || current.State != "stopped" {
		t.Fatal("retirement did not finish")
	}
}
func TestModelSizedStartupBudgetAndDeadlineBoundedClient(t *testing.T) {
	cfg := launchFixture(t, "normal")
	cfg.StartupTimeout = time.Minute
	p := startFixture(t, context.Background(), cfg)
	if err := p.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	invalid := launchFixture(t, "normal")
	invalid.StartupTimeout = -time.Second
	if _, err := start(t.Context(), invalid, []string{"-test.run=^TestRuntimeChild$"}); err == nil {
		t.Fatal("invalid startup budget launched child")
	}
	if _, err := os.Stat(RecordPath(invalid.Bootstrap.Root, invalid.Bootstrap.Identity.Scope)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid launch touched owner record")
	}
	_, client, _, _ := startService(t, Config{Bootstrap: testBootstrap(t), ReadCapabilities: []string{"slow"}, ReadHandler: func(ctx context.Context, _ Request) (json.RawMessage, *Error) {
		select {
		case <-ctx.Done():
			return nil, &Error{Code: "deadline", Message: "cancelled"}
		case <-time.After(10500 * time.Millisecond):
			return json.RawMessage(`{}`), nil
		}
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 13*time.Second)
	defer cancel()
	if _, err := client.Read(ctx, "slow", nil); err != nil {
		t.Fatalf("fixed ten second client limit remained: %v", err)
	}
	short, finish := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer finish()
	if _, err := client.Read(short, "slow", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline not preserved: %v", err)
	}
}
func TestPrivateRootPreparationNeverRewritesExistingStorage(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "new-private-cache")
	if err = PrepareRoot(root); err != nil {
		t.Fatal(err)
	}
	if err = checkDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err = PrepareRoot(root); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err = PrepareRoot(root); err == nil {
		t.Fatal("existing public directory silently adopted")
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("existing directory permissions rewritten")
	}
}
