package service

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"testing"
)

func TestVscreenInterventionTransition(t *testing.T) {
	// Given a stopped run awaiting explicit local takeover.
	// When the producer reports each possible transition.
	// Then only ordered or idempotent transitions are accepted.
	for _, tc := range []struct {
		from, to protocol.VscreenInterventionState
		want     bool
	}{
		{"awaiting_takeover", "human", true}, {"human", "ready_to_continue", true},
		{"awaiting_takeover", "ready_to_continue", false}, {"continued", "human", false},
		{"ready_to_continue", "ready_to_continue", true}, {"human", "cancelled", true},
	} {
		if got := validInterventionTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("%s -> %s = %v", tc.from, tc.to, got)
		}
	}
}

func TestVscreenInterventionNeverAutomaticallyRetries(t *testing.T) {
	// Given a stopped provider whose native gate requires human intervention.
	// When the normal retry service receives its canonical failure.
	// Then it returns without touching the database or queuing a run.
	s := &TaskService{}
	task, err := s.MaybeRetryFailedTask(context.Background(), db.AgentTaskQueue{FailureReason: pgtype.Text{String: "gui_human_intervention", Valid: true}})
	if err != nil || task != nil {
		t.Fatalf("task=%v error=%v", task, err)
	}
}
