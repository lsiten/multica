package handler

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestChatTaskExecutionScopeMatches(t *testing.T) {
	projectA := pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Valid: true}
	projectB := pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Valid: true}
	squadA := pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000003"), Valid: true}
	squadB := pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000004"), Valid: true}

	tests := []struct {
		name      string
		task      db.AgentTaskQueue
		projectID pgtype.UUID
		squadID   pgtype.UUID
		wantMatch bool
	}{
		{name: "unscoped legacy", task: db.AgentTaskQueue{}, wantMatch: true},
		{name: "project match", task: db.AgentTaskQueue{}, projectID: projectA, wantMatch: true},
		{name: "project mismatch", task: db.AgentTaskQueue{}, projectID: projectB, wantMatch: false},
		{name: "squad match", task: db.AgentTaskQueue{SquadID: squadA}, squadID: squadA, wantMatch: true},
		{name: "squad mismatch", task: db.AgentTaskQueue{SquadID: squadA}, squadID: squadB, wantMatch: false},
	}
	tests[1].task.Context = []byte(`{"project_id":"00000000-0000-0000-0000-000000000001"}`)
	tests[2].task.Context = []byte(`{"project_id":"00000000-0000-0000-0000-000000000001"}`)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := chatTaskExecutionScopeMatches(test.task, test.projectID, test.squadID); got != test.wantMatch {
				t.Fatalf("scope match = %v, want %v", got, test.wantMatch)
			}
		})
	}
}
