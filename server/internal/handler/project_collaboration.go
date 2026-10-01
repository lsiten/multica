package handler

import (
	"net/http"
	"strconv"
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
	for name, dest := range map[string]*pgtype.UUID{"issue_id": &params.IssueID, "squad_id": &params.SquadID, "agent_id": &params.AgentID} {
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
	params.StatusFilter = q.Get("status")
	switch params.StatusFilter {
	case "", "queued", "dispatched", "running", "waiting_local_directory", "completed", "failed", "cancelled":
	default:
		writeError(w, http.StatusBadRequest, "invalid task status")
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
	// Load graph events through the same tenant and member visibility query.
	// The aggregate graph uses these rows only to materialize proven artifact
	// and derived_from edges; missing provenance remains absent from the graph.
	events, eventsErr := h.Queries.ListVisibleProjectGraphEvents(r.Context(), db.ListVisibleProjectGraphEventsParams{
		WorkspaceID: workspaceID, ProjectID: projectID, PageOffset: 0,
		PageLimit: projectCollaborationScanLimit, UserID: member.UserID,
		IsAdmin: roleAllowed(member.Role, "owner", "admin"),
	})
	if eventsErr != nil {
		// Collaboration evidence remains useful on older databases that have not
		// applied the optional graph-event migration yet; report partial coverage
		// instead of failing the whole project graph request.
		out.CoverageReasons = append(out.CoverageReasons, "graph_events_unavailable")
	} else {
		out.GraphEvents = events
	}
	if len(events) >= projectCollaborationScanLimit {
		out.Truncated = true
		seenScanLimit := false
		for _, reason := range out.CoverageReasons {
			if reason == "scan_limit" {
				seenScanLimit = true
				break
			}
		}
		if !seenScanLimit {
			out.CoverageReasons = append(out.CoverageReasons, "scan_limit")
		}
	}
	for _, row := range rows {
		if row.RelationType != "root" && !row.SourceTaskID.Valid {
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
	start, end := collaborationPageBounds(len(graph.Edges), data.projectCollaborationPage)
	graph.HasMore = end < len(graph.Edges)
	graph.Edges = graph.Edges[start:end]
	writeJSON(w, http.StatusOK, graph)
}

func collaborationPageBounds(length int, page projectCollaborationPage) (int, int) {
	start := min(page.Offset, length)
	return start, start + min(page.Limit, length-start)
}
