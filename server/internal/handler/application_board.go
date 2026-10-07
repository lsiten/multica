package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/application"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type applicationInstanceView struct {
	ID                 string             `json:"id"`
	WorkspaceID        string             `json:"workspace_id"`
	ApplicationID      string             `json:"application_id"`
	RuntimeID          string             `json:"runtime_id"`
	Revision           int64              `json:"revision"`
	ObservedRevision   int64              `json:"observed_revision"`
	Generation         int64              `json:"generation"`
	ObservedGeneration int64              `json:"observed_generation"`
	DesiredState       string             `json:"desired_state"`
	ProcessState       string             `json:"process_state"`
	HealthState        string             `json:"health_state"`
	RuntimeState       string             `json:"runtime_state"`
	Status             string             `json:"status"`
	Error              string             `json:"error"`
	CodeVersion        string             `json:"code_version"`
	Dirty              bool               `json:"dirty"`
	StartedAt          *time.Time         `json:"started_at"`
	ObservedAt         *time.Time         `json:"observed_at"`
	Metrics            map[string]float64 `json:"metrics"`
	CanManage          bool               `json:"can_manage"`
}

type applicationEndpointView struct {
	ID            string `json:"id"`
	ApplicationID string `json:"application_id"`
	InstanceID    string `json:"instance_id"`
	Port          int32  `json:"port"`
	EntryPath     string `json:"entry_path"`
	Visibility    string `json:"visibility"`
	State         string `json:"state"`
	Revision      int64  `json:"revision"`
	CanManage     bool   `json:"can_manage"`
}

type applicationBoardSummary struct {
	Services     int `json:"services"`
	Compositions int `json:"compositions"`
	Instances    int `json:"instances"`
	Running      int `json:"running"`
	Unhealthy    int `json:"unhealthy"`
	Offline      int `json:"offline"`
	Stopped      int `json:"stopped"`
	Runtimes     int `json:"runtimes"`
}

type applicationBoardView struct {
	Applications []application.View          `json:"applications"`
	Instances    []applicationInstanceView   `json:"instances"`
	Endpoints    []applicationEndpointView   `json:"endpoints"`
	Operations   []application.OperationView `json:"operations"`
	Summary      applicationBoardSummary     `json:"summary"`
}

func applicationTimestamp(value pgtype.Timestamptz) *time.Time {
	if value.Valid {
		return &value.Time
	}
	return nil
}

// GetApplicationBoard projects catalog, runtime facts and activity without leaking local credentials.
func (h *Handler) GetApplicationBoard(w http.ResponseWriter, r *http.Request) {
	workspaceID, member, ok := h.applicationScope(w, r)
	if !ok {
		return
	}
	apps, err := h.applicationService().List(r.Context(), workspaceID, pgtype.UUID{})
	if err != nil {
		h.applicationError(w, err)
		return
	}
	apps, ok = h.filterReadableApplications(w, r, workspaceID, member, apps)
	if !ok {
		return
	}
	visibleApplications := make(map[string]bool, len(apps))
	for _, app := range apps {
		visibleApplications[app.ID] = true
	}
	instances, err := h.Queries.ListApplicationInstances(r.Context(), db.ListApplicationInstancesParams{WorkspaceID: workspaceID})
	if err != nil {
		h.applicationError(w, err)
		return
	}
	runtimes, err := h.Queries.ListAgentRuntimes(r.Context(), workspaceID)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	runtimeByID := make(map[string]db.AgentRuntime, len(runtimes))
	for _, runtime := range runtimes {
		runtimeByID[uuidToString(runtime.ID)] = runtime
	}
	view := applicationBoardView{Applications: apps, Instances: []applicationInstanceView{}, Endpoints: []applicationEndpointView{}, Operations: []application.OperationView{}}
	for _, app := range apps {
		if app.Kind == "composition" {
			view.Summary.Compositions++
		} else {
			view.Summary.Services++
		}
	}
	usedRuntimes := map[string]bool{}
	manageableInstances := map[string]bool{}
	now := time.Now()
	for _, instance := range instances {
		if !visibleApplications[uuidToString(instance.ApplicationID)] {
			continue
		}
		runtimeID := uuidToString(instance.RuntimeID)
		runtime, exists := runtimeByID[runtimeID]
		canManage := exists && canUseRuntimeForAgent(member, runtime)
		metrics := map[string]float64{}
		if err = json.Unmarshal(instance.Metrics, &metrics); err != nil {
			h.applicationError(w, err)
			return
		}
		row := applicationInstanceView{ID: uuidToString(instance.ID), WorkspaceID: uuidToString(instance.WorkspaceID), ApplicationID: uuidToString(instance.ApplicationID), RuntimeID: runtimeID, Revision: instance.Revision, ObservedRevision: instance.ObservedRevision, Generation: instance.Generation, ObservedGeneration: instance.ObservedGeneration, DesiredState: instance.DesiredState, ProcessState: instance.ProcessState, HealthState: instance.HealthState, RuntimeState: instance.RuntimeStatus.String, Error: instance.Error, CodeVersion: instance.CodeVersion, Dirty: instance.Dirty, StartedAt: applicationTimestamp(instance.StartedAt), ObservedAt: applicationTimestamp(instance.ObservedAt), Metrics: metrics, CanManage: canManage}
		row.Status = application.InstanceStatus(row.DesiredState, row.ProcessState, row.HealthState, row.RuntimeState, row.Generation, row.ObservedGeneration, row.ObservedAt, applicationTimestamp(instance.RuntimeLastSeenAt), now)
		manageableInstances[row.ID] = canManage
		usedRuntimes[runtimeID] = true
		view.Instances = append(view.Instances, row)
		switch row.Status {
		case "running":
			view.Summary.Running++
		case "failed", "unhealthy":
			view.Summary.Unhealthy++
		case "offline":
			view.Summary.Offline++
		case "stopped":
			view.Summary.Stopped++
		}
	}
	view.Summary.Instances = len(view.Instances)
	view.Summary.Runtimes = len(usedRuntimes)
	endpoints, err := h.Queries.ListApplicationEndpoints(r.Context(), db.ListApplicationEndpointsParams{WorkspaceID: workspaceID})
	if err != nil {
		h.applicationError(w, err)
		return
	}
	for _, endpoint := range endpoints {
		if !visibleApplications[uuidToString(endpoint.ApplicationID)] {
			continue
		}
		if endpoint.Visibility == "private" && endpoint.PublishedBy != member.UserID {
			continue
		}
		view.Endpoints = append(view.Endpoints, applicationEndpointView{ID: uuidToString(endpoint.ID), ApplicationID: uuidToString(endpoint.ApplicationID), InstanceID: uuidToString(endpoint.InstanceID), Port: endpoint.Port, EntryPath: endpoint.EntryPath, Visibility: endpoint.Visibility, State: endpoint.State, Revision: endpoint.Revision, CanManage: manageableInstances[uuidToString(endpoint.InstanceID)]})
	}
	view.Operations, err = h.applicationService().ListOperations(r.Context(), workspaceID, pgtype.UUID{})
	if err != nil {
		h.applicationError(w, err)
		return
	}
	operations := view.Operations[:0]
	for _, operation := range view.Operations {
		if visibleApplications[operation.ApplicationID] {
			operations = append(operations, operation)
		}
	}
	view.Operations = operations
	writeJSON(w, http.StatusOK, view)
}
