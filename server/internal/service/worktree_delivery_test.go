package service

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestSharedWorktreeDeliveryRefreshesScopedViewsWithoutCompletingAgain(t *testing.T) {
	bus := events.New()
	s := &TaskService{Bus: bus}
	task := db.AgentTaskQueue{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, IssueID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true}}
	var received []events.Event
	bus.SubscribeAll(func(e events.Event) { received = append(received, e) })
	s.NotifyWorktreeDelivery(task, "workspace")
	if len(received) != 1 || received[0].Type != protocol.EventTaskProgress || received[0].WorkspaceID != "workspace" {
		t.Fatalf("delivery emitted another lifecycle transition: %+v", received)
	}
	data, err := json.Marshal(received[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["task_id"] != util.UUIDToString(task.ID) || payload["issue_id"] != util.UUIDToString(task.IssueID) {
		t.Fatalf("scoped clients cannot refresh this delivery: %+v", payload)
	}
}
