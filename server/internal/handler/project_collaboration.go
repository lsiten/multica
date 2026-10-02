package handler

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const projectCollaborationScanLimit = 5000

type projectCollaborationPage struct {
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"has_more"`
}
type projectCollaborationDataset struct {
	ProjectID       string                                `json:"project_id"`
	AsOf            string                                `json:"as_of"`
	Truncated       bool                                  `json:"truncated"`
	CoverageReasons []string                              `json:"coverage_reasons"`
	Runs            []db.ListProjectCollaborationRunsRow  `json:"-"`
	GraphEvents     []db.ListVisibleProjectGraphEventsRow `json:"-"`
	projectCollaborationPage
}

func (h *Handler) loadProjectCollaboration(w http.ResponseWriter, r *http.Request) (projectCollaborationDataset, bool) {
	out := projectCollaborationDataset{AsOf: time.Now().UTC().Format(time.RFC3339Nano), CoverageReasons: []string{}, projectCollaborationPage: projectCollaborationPage{Limit: 100}}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return out, false
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return out, false
	}
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(workspaceID), "project not found")
	if !ok {
		return out, false
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: workspaceID}); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "project not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load project")
		}
		return out, false
	}
	out.ProjectID = uuidToString(projectID)
	params := db.ListProjectCollaborationRunsParams{WorkspaceID: workspaceID, ProjectID: projectID, UserID: member.UserID, IsAdmin: roleAllowed(member.Role, "owner", "admin"), ScanLimit: projectCollaborationScanLimit + 1}
	q := r.URL.Query()
	if snapshot := q.Get("snapshot_at"); snapshot != "" {
		at, err := time.Parse(time.RFC3339Nano, snapshot)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid snapshot timestamp")
			return out, false
		}
		out.AsOf = at.UTC().Format(time.RFC3339Nano)
	}
	if cursor := q.Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		parts := strings.SplitN(string(decoded), "|", 2)
		if err != nil || len(parts) != 2 {
			writeError(w, http.StatusBadRequest, "invalid collaboration cursor")
			return out, false
		}
		at, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid collaboration cursor")
			return out, false
		}
		id, valid := parseUUIDOrBadRequest(w, parts[1], "cursor id")
		if !valid {
			return out, false
		}
		params.CursorAt = pgtype.Timestamptz{Time: at, Valid: true}
		params.CursorID = id
	}
	for name, dest := range map[string]*pgtype.UUID{"issue_id": &params.IssueID, "squad_id": &params.SquadID, "agent_id": &params.AgentID, "node_agent_id": &params.NodeAgentID, "run_id": &params.RunID} {
		if value := q.Get(name); value != "" {
			id, valid := parseUUIDOrBadRequest(w, value, name)
			if !valid {
				return out, false
			}
			*dest = id
		}
	}
	for name, dest := range map[string]*pgtype.Timestamptz{"from": &params.FromAt, "to": &params.ToAt} {
		if value := q.Get(name); value != "" {
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid "+name+" timestamp")
				return out, false
			}
			*dest = pgtype.Timestamptz{Time: parsed, Valid: true}
		}
	}
	if params.FromAt.Valid && params.ToAt.Valid && params.FromAt.Time.After(params.ToAt.Time) {
		writeError(w, http.StatusBadRequest, "from must not be after to")
		return out, false
	}
	if q.Get("cursor_mode") == "true" {
		at, err := time.Parse(time.RFC3339Nano, out.AsOf)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "invalid collaboration snapshot")
			return out, false
		}
		if !params.ToAt.Valid || params.ToAt.Time.After(at) {
			params.ToAt = pgtype.Timestamptz{Time: at, Valid: true}
		}
	}
	params.StatusFilter = q.Get("status")
	params.ActivityFilter = q.Get("activity")
	params.RelationType = q.Get("relation_type")
	params.TaskQuery = q.Get("task_query")
	if sort := q.Get("sort"); sort != "" && sort != "newest" && sort != "oldest" {
		writeError(w, http.StatusBadRequest, "invalid collaboration sort")
		return out, false
	}
	params.OldestFirst = q.Get("sort") == "oldest"
	if params.StatusFilter == "" && params.ActivityFilter == "" {
		params.ActivityFilter = "active"
	}
	switch params.StatusFilter {
	case "", "queued", "dispatched", "running", "waiting_local_directory", "completed", "failed", "cancelled":
	default:
		writeError(w, http.StatusBadRequest, "invalid task status")
		return out, false
	}
	switch params.ActivityFilter {
	case "", "active", "all", "ended":
	default:
		writeError(w, http.StatusBadRequest, "invalid activity filter")
		return out, false
	}
	switch params.RelationType {
	case "", "root", "delegated", "retry", "rerun", "parent_child":
	default:
		writeError(w, http.StatusBadRequest, "invalid relation type")
		return out, false
	}
	for name, dest := range map[string]*int{"limit": &out.Limit, "offset": &out.Offset} {
		if value := q.Get(name); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 || (name == "limit" && (parsed == 0 || parsed > 500)) {
				writeError(w, http.StatusBadRequest, "invalid "+name)
				return out, false
			}
			*dest = parsed
		}
	}
	rows, err := h.Queries.ListProjectCollaborationRuns(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project collaboration")
		return out, false
	}
	if len(rows) > projectCollaborationScanLimit {
		out.Truncated = true
		out.CoverageReasons = append(out.CoverageReasons, "scan_limit")
		rows = rows[:projectCollaborationScanLimit]
	}
	out.Runs = rows
	for _, row := range rows {
		if row.RelationType != "root" && !row.SourceTaskID.Valid && !row.SourceIssueID.Valid {
			out.CoverageReasons = append(out.CoverageReasons, "unresolved_lineage")
			break
		}
	}
	return out, true
}

func (h *Handler) GetProjectCollaborationGraph(w http.ResponseWriter, r *http.Request) {
	data, ok := h.loadProjectCollaboration(w, r)
	if !ok {
		return
	}
	graph := buildProjectCollaboration(data)
	if r.URL.Query().Get("complete") == "true" {
		graph.Limit = len(graph.Edges)
		graph.Offset = 0
		graph.HasMore = false
		writeJSON(w, http.StatusOK, graph)
		return
	}
	start, end := collaborationPageBounds(len(graph.Edges), data.projectCollaborationPage)
	graph.HasMore = end < len(graph.Edges)
	graph.Edges = graph.Edges[start:end]
	writeJSON(w, http.StatusOK, graph)
}

func collaborationPageBounds(length int, page projectCollaborationPage) (int, int) {
	start := min(page.Offset, length)
	return start, start + min(page.Limit, length-start)
}
