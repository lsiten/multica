package handler

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func graphTestUUID(seed byte) pgtype.UUID {
	var value pgtype.UUID
	value.Bytes[15] = seed
	value.Valid = true
	return value
}

func TestEnrichProjectGraphEventDataUsesServerTaskLineage(t *testing.T) {
	task := db.AgentTaskQueue{
		AgentID:             graphTestUUID(1),
		DelegatedFromTaskID: graphTestUUID(2),
		RetryOfTaskID:       graphTestUUID(3),
		RerunOfTaskID:       graphTestUUID(4),
		SquadID:             graphTestUUID(5),
	}
	raw, err := enrichProjectGraphEventData(json.RawMessage(`{"provider":"claude","agent_id":"forged"}`), task)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data["agent_id"] != task.AgentID.String() {
		t.Fatalf("agent_id=%v, want %s", data["agent_id"], task.AgentID.String())
	}
	for key, want := range map[string]string{
		"delegated_from_task_id": task.DelegatedFromTaskID.String(),
		"retry_of_task_id":       task.RetryOfTaskID.String(),
		"rerun_of_task_id":       task.RerunOfTaskID.String(),
		"squad_id":               task.SquadID.String(),
	} {
		if data[key] != want {
			t.Fatalf("%s=%v, want %s", key, data[key], want)
		}
	}
}

func TestEnrichProjectGraphEventDataRejectsNonObject(t *testing.T) {
	if _, err := enrichProjectGraphEventData(json.RawMessage(`[]`), db.AgentTaskQueue{}); err == nil {
		t.Fatal("expected non-object graph event data to be rejected")
	}
}
