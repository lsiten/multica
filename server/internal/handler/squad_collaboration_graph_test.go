package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestSquadCollaborationGraphArrayContract(t *testing.T) {
	leader := dbfx.Agent(t, "agreement-leader", handlerTestRuntimeID(t))
	worker := dbfx.Agent(t, "agreement-worker", handlerTestRuntimeID(t))
	squad := dbfx.Squad(t, "agreement-array-contract", leader)
	dbfx.SquadMember(t, squad, "agent", leader)
	dbfx.SquadMember(t, squad, "agent", worker)
	dbfx.Cleanup(t, `DELETE FROM squad_collaboration_graph WHERE squad_id=$1`, squad)
	dbfx.Cleanup(t, `DELETE FROM squad_collaboration_history WHERE squad_id=$1`, squad)

	type graphResponse struct {
		Revision int32 `json:"revision"`
		Members  []struct {
			ID string `json:"member_id"`
		} `json:"members"`
		Relations []struct {
			Deliverables []string `json:"deliverables"`
		} `json:"relations"`
		DerivedRelations []struct {
			Deliverables []string `json:"deliverables"`
		} `json:"derived_relations"`
	}
	params := map[string]string{"id": squad}
	var initial graphResponse
	testutil.Call(t, testHandler.GetSquadCollaborationGraph, squadScopeReq("", http.MethodGet, "/api/squads/graph", nil, params)).Want(http.StatusOK).JSON(&initial)
	if len(initial.Members) != 2 || initial.Relations == nil || len(initial.DerivedRelations) != 1 || initial.DerivedRelations[0].Deliverables == nil {
		t.Fatalf("initial graph must retain members and empty arrays: %+v", initial)
	}

	var saved graphResponse
	testutil.Call(t, testHandler.UpdateSquadCollaborationGraph, squadScopeReq("", http.MethodPut, "/api/squads/graph", squadCollaborationGraphUpdate{
		ExpectedRevision: 0,
		Relations:        []squadCollaborationRelationInput{{FromMemberID: leader, ToMemberID: worker, FromMemberType: "agent", ToMemberType: "agent", Type: "review"}},
	}, params)).Want(http.StatusOK).JSON(&saved)
	if saved.Revision != 1 || len(saved.Relations) != 1 || saved.Relations[0].Deliverables == nil {
		t.Fatalf("saved relation must normalize omitted deliverables: %+v", saved)
	}

	var historical graphResponse
	dbfx.Exec(t, `UPDATE squad_collaboration_history SET snapshot=jsonb_set(jsonb_set(snapshot, '{relations,0,deliverables}', 'null'), '{derived_relations,0,deliverables}', 'null') WHERE squad_id=$1 AND revision=1`, squad)
	testutil.Call(t, testHandler.GetSquadCollaborationGraph, squadScopeReq("", http.MethodGet, "/api/squads/graph?revision=1", nil, params)).Want(http.StatusOK).JSON(&historical)
	if historical.Revision != 1 || len(historical.Relations) != 1 || historical.Relations[0].Deliverables == nil || historical.DerivedRelations[0].Deliverables == nil {
		t.Fatalf("historical snapshot must preserve the array contract: %+v", historical)
	}
}
