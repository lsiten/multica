package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type squadCollaborationRelationInput struct {
	ID             string   `json:"id,omitempty"`
	FromMemberID   string   `json:"from_member_id"`
	ToMemberID     string   `json:"to_member_id"`
	FromMemberType string   `json:"from_member_type"`
	ToMemberType   string   `json:"to_member_type"`
	Type           string   `json:"type"`
	Label          string   `json:"label"`
	Trigger        string   `json:"trigger"`
	Deliverables   []string `json:"deliverables"`
	Acceptance     string   `json:"acceptance"`
}

type squadCollaborationGraphUpdate struct {
	ExpectedRevision int32                             `json:"expected_revision"`
	Relations        []squadCollaborationRelationInput `json:"relations"`
}

var squadCollaborationRelationTypes = map[string]bool{"coordinate": true, "handoff": true, "review": true, "accept": true}

func stableSquadRelationID(rel squadCollaborationRelationInput) string {
	b, _ := json.Marshal(struct {
		From, To, FromType, ToType, Type string
	}{rel.FromMemberID, rel.ToMemberID, rel.FromMemberType, rel.ToMemberType, rel.Type})
	sum := sha256.Sum256(b)
	return "relation-" + hex.EncodeToString(sum[:8])
}

func squadDerivedRelations(squad db.Squad, members []db.SquadMember) []squadCollaborationRelationInput {
	leader := uuidToString(squad.LeaderID)
	out := make([]squadCollaborationRelationInput, 0, len(members))
	for _, member := range members {
		to := uuidToString(member.MemberID)
		if to == leader || member.MemberType != "agent" {
			continue
		}
		// API clients expect an array, including when no deliverables are required.
		rel := squadCollaborationRelationInput{
			FromMemberID: leader, ToMemberID: to,
			FromMemberType: "agent", ToMemberType: member.MemberType,
			Type: "coordinate", Label: "Squad leader coordination",
			Deliverables: []string{},
		}
		rel.ID = stableSquadRelationID(rel)
		out = append(out, rel)
	}
	return out
}

func (h *Handler) squadCollaborationGraphResponse(ctx context.Context, squad db.Squad, row *db.SquadCollaborationGraph, members []db.SquadMember) (map[string]any, error) {
	memberRows := make([]map[string]any, 0, len(members))
	for _, member := range members {
		label := uuidToString(member.MemberID)
		if member.MemberType == "agent" {
			if agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: member.MemberID, WorkspaceID: squad.WorkspaceID}); err == nil && strings.TrimSpace(agent.Name) != "" {
				label = agent.Name
			}
		}
		memberRows = append(memberRows, map[string]any{"member_id": uuidToString(member.MemberID), "member_type": member.MemberType, "label": label, "role": member.Role})
	}
	revision := int32(0)
	var updatedAt any
	rawRelations := []byte("[]")
	if row != nil {
		revision, rawRelations = row.Revision, row.Relations
		if row.UpdatedAt.Valid {
			updatedAt = timestampToString(row.UpdatedAt)
		}
	}
	relations := []squadCollaborationRelationInput{}
	if err := json.Unmarshal(rawRelations, &relations); err != nil {
		return nil, err
	}
	validMembers := make(map[string]bool, len(members))
	for _, member := range members {
		validMembers[member.MemberType+":"+uuidToString(member.MemberID)] = true
	}
	activeRelations := make([]squadCollaborationRelationInput, 0, len(relations))
	for i := range relations {
		if !validMembers[relations[i].FromMemberType+":"+relations[i].FromMemberID] || !validMembers[relations[i].ToMemberType+":"+relations[i].ToMemberID] {
			continue
		}
		if relations[i].ID == "" {
			relations[i].ID = stableSquadRelationID(relations[i])
		}
		if relations[i].Deliverables == nil {
			relations[i].Deliverables = []string{}
		}
		activeRelations = append(activeRelations, relations[i])
	}
	derived := squadDerivedRelations(squad, members)
	return map[string]any{"squad_id": uuidToString(squad.ID), "revision": revision, "updated_at": updatedAt, "members": memberRows, "relations": activeRelations, "derived_relations": derived}, nil
}

func (h *Handler) GetSquadCollaborationGraph(w http.ResponseWriter, r *http.Request) {
	squad, workspaceID, ok := h.loadSquadInWorkspace(w, r)
	if !ok {
		return
	}
	wsUUID := parseUUID(workspaceID)
	if rawRevision := r.URL.Query().Get("revision"); rawRevision != "" {
		revision, err := strconv.ParseInt(rawRevision, 10, 32)
		if err != nil || revision < 1 {
			writeError(w, http.StatusBadRequest, "invalid revision")
			return
		}
		snapshot, err := h.Queries.GetSquadCollaborationHistory(r.Context(), db.GetSquadCollaborationHistoryParams{SquadID: squad.ID, WorkspaceID: wsUUID, Revision: int32(revision)})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "squad collaboration revision not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load squad collaboration revision")
			return
		}
		var response map[string]any
		if err := json.Unmarshal(snapshot, &response); err != nil {
			writeError(w, http.StatusInternalServerError, "invalid squad collaboration revision")
			return
		}
		// Normalize old persisted snapshots without changing their member/revision data.
		for _, key := range []string{"relations", "derived_relations"} {
			if response[key] == nil {
				response[key] = []any{}
				continue
			}
			relations, valid := response[key].([]any)
			if !valid {
				writeError(w, http.StatusInternalServerError, "invalid squad collaboration revision")
				return
			}
			for _, rawRelation := range relations {
				relation, valid := rawRelation.(map[string]any)
				if !valid {
					writeError(w, http.StatusInternalServerError, "invalid squad collaboration revision")
					return
				}
				if relation["deliverables"] == nil {
					relation["deliverables"] = []string{}
				}
			}
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	row, err := h.Queries.GetSquadCollaborationGraph(r.Context(), db.GetSquadCollaborationGraphParams{SquadID: squad.ID, WorkspaceID: wsUUID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load squad collaboration graph")
		return
	}
	var graph *db.SquadCollaborationGraph
	if err == nil {
		graph = &row
	}
	members, err := h.Queries.ListSquadMembers(r.Context(), squad.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load squad members")
		return
	}
	response, err := h.squadCollaborationGraphResponse(r.Context(), squad, graph, members)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid squad collaboration graph")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) UpdateSquadCollaborationGraph(w http.ResponseWriter, r *http.Request) {
	squad, workspaceID, ok := h.loadSquadInWorkspace(w, r)
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found")
	if !ok || !canManageSquad(member, squad) {
		if ok {
			writeError(w, http.StatusForbidden, "you cannot manage this squad")
		}
		return
	}
	var req squadCollaborationGraphUpdate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid collaboration graph")
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid collaboration graph")
		return
	}
	if req.ExpectedRevision < 0 || len(req.Relations) > 200 {
		writeError(w, http.StatusBadRequest, "invalid collaboration graph revision or relation count")
		return
	}
	members, err := h.Queries.ListSquadMembers(r.Context(), squad.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load squad members")
		return
	}
	valid := map[string]bool{}
	for _, m := range members {
		valid[m.MemberType+":"+uuidToString(m.MemberID)] = true
	}
	seen := map[string]bool{}
	for i := range req.Relations {
		rel := &req.Relations[i]
		if !valid[rel.FromMemberType+":"+rel.FromMemberID] || !valid[rel.ToMemberType+":"+rel.ToMemberID] || rel.FromMemberType+":"+rel.FromMemberID == rel.ToMemberType+":"+rel.ToMemberID || !squadCollaborationRelationTypes[rel.Type] || len(rel.Label) > 200 || len(rel.Trigger) > 1000 || len(rel.Acceptance) > 2000 || len(rel.Deliverables) > 20 {
			writeError(w, http.StatusBadRequest, "invalid squad collaboration relation")
			return
		}
		for _, item := range rel.Deliverables {
			if len(item) > 500 {
				writeError(w, http.StatusBadRequest, "invalid squad collaboration deliverable")
				return
			}
		}
		rel.ID = stableSquadRelationID(*rel)
		if seen[rel.ID] {
			writeError(w, http.StatusBadRequest, "duplicate squad collaboration relation")
			return
		}
		seen[rel.ID] = true
		if rel.Deliverables == nil {
			rel.Deliverables = []string{}
		}
	}
	if req.Relations == nil {
		req.Relations = []squadCollaborationRelationInput{}
	}
	payload, err := json.Marshal(req.Relations)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid collaboration graph")
		return
	}
	updatedBy, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user_id")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save squad collaboration graph")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if _, err := qtx.LockWorkspaceForChatSessionCreate(r.Context(), squad.WorkspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save squad collaboration graph")
		return
	}
	if _, err := qtx.LockSquadForUpdate(r.Context(), db.LockSquadForUpdateParams{ID: squad.ID, WorkspaceID: squad.WorkspaceID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save squad collaboration graph")
		return
	}
	row, err := qtx.UpsertSquadCollaborationGraph(r.Context(), db.UpsertSquadCollaborationGraphParams{SquadID: squad.ID, WorkspaceID: squad.WorkspaceID, Relations: payload, ExpectedRevision: req.ExpectedRevision})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "squad collaboration graph revision conflict")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save squad collaboration graph")
		return
	}
	graphRow := db.SquadCollaborationGraph{SquadID: row.SquadID, WorkspaceID: row.WorkspaceID, Revision: row.Revision, Relations: row.Relations, UpdatedAt: row.UpdatedAt}
	response, err := h.squadCollaborationGraphResponse(r.Context(), squad, &graphRow, members)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid saved collaboration graph")
		return
	}
	snapshot, err := json.Marshal(response)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save squad collaboration history")
		return
	}
	if err := qtx.InsertSquadCollaborationHistory(r.Context(), db.InsertSquadCollaborationHistoryParams{SquadID: squad.ID, WorkspaceID: squad.WorkspaceID, Revision: row.Revision, Snapshot: snapshot, UpdatedBy: updatedBy}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save squad collaboration history")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save squad collaboration graph")
		return
	}
	writeJSON(w, http.StatusOK, response)
}
