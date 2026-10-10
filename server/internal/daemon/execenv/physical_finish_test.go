package execenv

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestPhysicalFinishRequiresKernelReleaseAndSurvivesServiceLoss(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := UseSharedDirectory(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Finish(ctx, nil)
	if err = lease.bindWorktreeRun(SharedWorktreeDelivery{TaskID: "task", Namespace: "owned-test", WorkDir: path, Branch: "branch"}); err != nil {
		t.Fatal(err)
	}
	participant, err := lease.PhysicalParticipant()
	if err != nil {
		t.Fatal(err)
	}
	finish, permit, err := BeginPhysicalFinish(ctx, participant)
	if err != nil {
		t.Fatal(err)
	}
	defer finish.Close()
	called := false
	if err = finish.Confirm(permit, func(bool) error { called = true; return nil }); err == nil || called {
		t.Fatalf("live lock accepted: err=%v settled=%v", err, called)
	}
	changed := permit
	changed.ID = "stale"
	if err = lease.ReleasePhysicalParticipant(changed); err == nil {
		t.Fatal("stale permit closed live participant")
	}
	if err = lease.ReleasePhysicalParticipant(permit); err != nil {
		t.Fatal(err)
	}
	finish.Close()
	if _, err = UseSharedDirectory(ctx, path); !errors.Is(err, ErrPhysicalFinishPending) {
		t.Fatalf("unresolved finish admitted writer: %v", err)
	}
	if unsettled, err := SharedDirectoryUnsettled(ctx, path); err != nil || !unsettled {
		t.Fatalf("GC lost finish protection: %v %v", unsettled, err)
	}
	recovered, repeated, err := BeginPhysicalFinish(ctx, participant)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if repeated != permit {
		t.Fatal("service recovery changed release identity")
	}
	if err = recovered.Confirm(repeated, func(last bool) error {
		if !last {
			t.Fatal("expected last borrower")
		}
		called = true
		return resolveSharedWorktreeReceipts(recovered.dir, "commit")
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("settlement not called")
	}
	next, err := UseSharedDirectory(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = next.Finish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	receipt, err := readSharedDelivery(sharedDeliveryFile(recovered.dir, "task", "owned-test"))
	if err != nil || receipt.Commit != "commit" {
		t.Fatalf("durable delivery missing: %+v %v", receipt, err)
	}
	if err = AcknowledgeSharedWorktreeDelivery(ctx, receipt); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalFinishKeepsOtherBorrowerAndReceipt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := UseSharedDirectory(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Finish(ctx, nil)
	second, err := UseSharedDirectory(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Finish(ctx, nil)
	if err = first.bindWorktreeRun(SharedWorktreeDelivery{TaskID: "early", Namespace: "owned-test", WorkDir: path}); err != nil {
		t.Fatal(err)
	}
	participant, err := first.PhysicalParticipant()
	if err != nil {
		t.Fatal(err)
	}
	finish, permit, err := BeginPhysicalFinish(ctx, participant)
	if err != nil {
		t.Fatal(err)
	}
	defer finish.Close()
	if err = first.ReleasePhysicalParticipant(permit); err != nil {
		t.Fatal(err)
	}
	if err = finish.Confirm(permit, func(last bool) error {
		if last {
			t.Fatal("lost second borrower's kernel lease")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := readSharedDelivery(sharedDeliveryFile(finish.dir, "early", "owned-test"))
	if err != nil || receipt.Commit != "" || receipt.NoWork {
		t.Fatalf("early receipt settled: %+v %v", receipt, err)
	}
	if err = second.Finish(ctx, func(last bool) error {
		if !last {
			t.Fatal("second borrower not last")
		}
		return resolveSharedWorktreeReceipts(finish.dir, "")
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err = readSharedDelivery(sharedDeliveryFile(finish.dir, "early", "owned-test"))
	if err != nil || !receipt.NoWork {
		t.Fatalf("last borrower failed receipt: %+v %v", receipt, err)
	}
	if err = AcknowledgeSharedWorktreeDelivery(ctx, receipt); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalFinishCancellationAndFailedSettlementKeepFence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := UseSharedDirectory(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Finish(ctx, nil)
	if err = lease.bindWorktreeRun(SharedWorktreeDelivery{TaskID: "cancelled", Namespace: "owned-test", WorkDir: path}); err != nil {
		t.Fatal(err)
	}
	participant, err := lease.PhysicalParticipant()
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if finish, _, err := BeginPhysicalFinish(cancelled, participant); !errors.Is(err, context.Canceled) || finish != nil {
		t.Fatalf("cancel admitted finish: %v", err)
	}
	finish, permit, err := BeginPhysicalFinish(ctx, participant)
	if err != nil {
		t.Fatal(err)
	}
	defer finish.Close()
	if err = lease.ReleasePhysicalParticipant(permit); err != nil {
		t.Fatal(err)
	}
	settlementErr := errors.New("owned settlement failure")
	if err = finish.Confirm(permit, func(bool) error { return settlementErr }); !errors.Is(err, settlementErr) {
		t.Fatalf("settlement failure lost: %v", err)
	}
	finish.Close()
	if _, err = UseSharedDirectory(ctx, path); !errors.Is(err, ErrPhysicalFinishPending) {
		t.Fatalf("failed settlement admitted writer: %v", err)
	}
	retry, repeated, err := BeginPhysicalFinish(ctx, participant)
	if err != nil {
		t.Fatal(err)
	}
	defer retry.Close()
	if err = retry.Confirm(repeated, func(bool) error { return resolveSharedWorktreeReceipts(retry.dir, "") }); err != nil {
		t.Fatal(err)
	}
	receipt, err := readSharedDelivery(sharedDeliveryFile(retry.dir, "cancelled", "owned-test"))
	if err != nil {
		t.Fatal(err)
	}
	if err = AcknowledgeSharedWorktreeDelivery(ctx, receipt); err != nil {
		t.Fatal(err)
	}
}
