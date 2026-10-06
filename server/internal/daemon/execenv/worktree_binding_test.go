package execenv

import (
	"testing"
)

func TestWorktreeConsumersAreClaimScopedAndReleaseIndependently(t *testing.T) {
	claim, err := ClaimEnvRoot(RootDirParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "workspace", TaskID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	path := claim.RootDir()
	first := WorktreeConsumer{TaskID: "first", AgentID: "agent", RuntimeID: "runtime", DispatchedAt: "old-claim"}
	second := WorktreeConsumer{TaskID: "second", AgentID: "agent", RuntimeID: "runtime"}
	for _, consumer := range []WorktreeConsumer{first, second} {
		if err := UpdateWorktreeConsumer(path, consumer, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := UpdateWorktreeConsumer(path, first, true); err != nil {
		t.Fatal(err)
	}
	binding, err := ReadWorktreeBinding(path)
	if err != nil || len(binding.Consumers) != 1 || binding.Consumers[0] != second {
		t.Fatalf("other consumer was released: %+v %v", binding, err)
	}
	first.DispatchedAt = "new-claim"
	if err := UpdateWorktreeConsumer(path, first, false); err != nil {
		t.Fatal(err)
	}
	stale := first
	stale.DispatchedAt = "old-claim"
	if err := UpdateWorktreeConsumer(path, stale, true); err != nil {
		t.Fatal(err)
	}
	binding, err = ReadWorktreeBinding(path)
	if err != nil || len(binding.Consumers) != 2 {
		t.Fatalf("old claim removed the new consumer: %+v %v", binding, err)
	}
	for _, consumer := range []WorktreeConsumer{first, second} {
		if err := UpdateWorktreeConsumer(path, consumer, true); err != nil {
			t.Fatal(err)
		}
	}
	binding, err = ReadWorktreeBinding(path)
	if err != nil || len(binding.Consumers) != 0 {
		t.Fatalf("ended runs retain execution references: %+v %v", binding, err)
	}
}
