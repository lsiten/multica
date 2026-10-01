package projectgraph

import (
	"testing"
	"time"
)

func TestStoreAppendsAndReloadsEvents(t *testing.T) {
	store, err := Open(t.TempDir(), "project-1")
	if err != nil {
		t.Fatal(err)
	}
	want := Event{At: time.Unix(10, 0), Type: "task_started", ProjectID: "project-1", NodeID: "task-1", Data: map[string]any{"provider": "claude"}}
	if err := store.Append(want); err != nil {
		t.Fatal(err)
	}
	events, err := store.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != want.Type || events[0].NodeID != want.NodeID {
		t.Fatalf("events=%+v", events)
	}
}

func TestOpenRejectsPathTraversalProjectID(t *testing.T) {
	root := t.TempDir()
	for _, projectID := range []string{"../escape", "nested/project", ".", ".."} {
		if _, err := Open(root, projectID); err == nil {
			t.Fatalf("Open(%q) unexpectedly accepted a path-like project id", projectID)
		}
	}
}
