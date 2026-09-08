package daemon

import (
	"context"
	"path/filepath"
	"testing"
)

func TestReviewPathGuardRejectsOverlappingTasks(t *testing.T) {
	locks := NewLocalPathLocker()
	root := t.TempDir()
	release, err := locks.Acquire(context.Background(), filepath.Join(root, "src"), "task", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := locks.guardReviewPaths([]string{root}); ok {
		t.Fatal("accepted active child directory")
	}
	if _, ok := locks.guardReviewPaths([]string{filepath.Join(root, "src", "nested")}); ok {
		t.Fatal("accepted active parent directory")
	}
	unlock, ok := locks.guardReviewPaths([]string{t.TempDir()})
	if !ok {
		t.Fatal("unrelated task blocks review")
	}
	unlock()
	release()
	unlock, ok = locks.guardReviewPaths([]string{root})
	if !ok {
		t.Fatal("finished task blocks review")
	}
	unlock()
}
