package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestStableSquadRelationID_IsDeterministic(t *testing.T) {
	rel := squadCollaborationRelationInput{FromMemberID: "from", ToMemberID: "to", FromMemberType: "agent", ToMemberType: "member", Type: "handoff"}
	if got, want := stableSquadRelationID(rel), stableSquadRelationID(rel); got != want || got == "" {
		t.Fatalf("stable relation id = %q, want deterministic non-empty id", got)
	}
}

func TestSquadDerivedRelations_OnlyAgentWorkers(t *testing.T) {
	leader := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	worker := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	squad := db.Squad{LeaderID: leader}
	members := []db.SquadMember{{MemberID: leader, MemberType: "agent"}, {MemberID: worker, MemberType: "agent"}, {MemberID: worker, MemberType: "member"}}
	got := squadDerivedRelations(squad, members)
	if len(got) != 1 || got[0].Type != "coordinate" || got[0].FromMemberID != uuidToString(leader) || got[0].ToMemberID != uuidToString(worker) {
		t.Fatalf("derived relations = %+v", got)
	}
}
