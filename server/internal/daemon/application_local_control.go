package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type localApplicationRequest struct {
	WorkspaceID   string `json:"workspace_id"`
	RuntimeID     string `json:"runtime_id"`
	ApplicationID string `json:"application_id"`
	Action        string `json:"action"`
	Revision      int64  `json:"revision"`
	CommandID     string `json:"command_id"`
}

func (d *Daemon) taskLocalApplicationControl(ctx context.Context, task Task, appID string, body map[string]any) (any, bool, error) {
	var input struct {
		Action         string `json:"action"`
		RuntimeID      string `json:"runtime_id"`
		Revision       int64  `json:"revision"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if json.Unmarshal(marshalRaw(body), &input) != nil || input.IdempotencyKey == "" || input.RuntimeID != task.RuntimeID || (input.Action != "start" && input.Action != "stop" && input.Action != "restart") {
		return nil, false, nil
	}
	request := localApplicationRequest{WorkspaceID: task.WorkspaceID, RuntimeID: input.RuntimeID, ApplicationID: appID, Action: input.Action, Revision: input.Revision, CommandID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(task.AgentID+"\x00"+appID+"\x00"+input.IdempotencyKey)).String()}
	record, err := d.findLocalApplication(request)
	if err != nil {
		return nil, false, nil
	}
	// The scoped task cannot acquire the machine owner's cached permissions.
	if task.AgentID == "" || record.Command.AccessAgentID != task.AgentID || record.Command.AccessUserID != d.accountID {
		return nil, false, nil
	}
	result, err := d.runLocalApplication(ctx, request)
	if err != nil {
		return nil, true, err
	}
	return map[string]any{"id": result.CommandID, "state": "completed", "local": true, "sync_pending": result.SyncPending, "observation": result.Observation}, true, nil
}

type localApplicationResult struct {
	CommandID   string                          `json:"command_id"`
	Observation protocol.ApplicationObservation `json:"observation"`
	SyncPending bool                            `json:"sync_pending"`
	Error       string                          `json:"error,omitempty"`
}

func (d *Daemon) findLocalApplication(input localApplicationRequest) (applicationhost.Record, error) {
	d.mu.Lock()
	account := d.accountID
	d.mu.Unlock()
	if account == "" {
		return applicationhost.Record{}, errors.New("verified local account identity unavailable")
	}
	if d.localAuthorityRejected.Load() && input.Action != "stop" {
		return applicationhost.Record{}, errors.New("account authority was rejected; sign in before starting an application")
	}
	if input.Action != "start" && input.Action != "stop" && input.Action != "restart" {
		return applicationhost.Record{}, errors.New("this operation requires backend authority")
	}
	if input.CommandID == "" {
		return applicationhost.Record{}, errors.New("local command identity required")
	}
	for _, id := range []string{input.WorkspaceID, input.RuntimeID, input.ApplicationID, input.CommandID} {
		if uuid.Validate(id) != nil {
			return applicationhost.Record{}, errors.New("invalid local application identity")
		}
	}
	records, err := d.applicationRecords()
	if err != nil {
		return applicationhost.Record{}, err
	}
	for _, record := range records {
		command := record.Command
		if command.WorkspaceID == input.WorkspaceID && command.RuntimeID == input.RuntimeID && command.ApplicationID == input.ApplicationID && command.Revision == input.Revision {
			if !d.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID}) {
				return applicationhost.Record{}, errors.New("local runtime ownership changed")
			}
			return record, nil
		}
	}
	var candidate applicationhost.Record
	found := false
	d.applicationCommands.Range(func(_, value any) bool {
		command, ok := value.(protocol.ApplicationControlCommand)
		if ok && command.WorkspaceID == input.WorkspaceID && command.RuntimeID == input.RuntimeID && command.ApplicationID == input.ApplicationID && command.Revision == input.Revision {
			candidate = applicationhost.Record{Command: command, Observation: protocol.ApplicationObservation{InstanceID: command.InstanceID, Generation: command.Generation, Revision: command.Revision, ProcessState: "stopped", HealthState: "unknown"}}
			found = true
		}
		return !found
	})
	if found && d.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: candidate.Command.WorkspaceID, RuntimeID: candidate.Command.RuntimeID}) {
		return candidate, nil
	}
	return applicationhost.Record{}, errors.New("application has no verified local recipe for this revision")
}

func (d *Daemon) runLocalApplication(ctx context.Context, input localApplicationRequest) (localApplicationResult, error) {
	record, err := d.findLocalApplication(input)
	if err != nil {
		return localApplicationResult{}, err
	}
	if d.applicationProcessMode() {
		client, err := d.ensureApplicationProcess(ctx)
		if err != nil {
			return localApplicationResult{}, err
		}
		var result localApplicationResult
		err = client.mutation(ctx, "application.local", input, &result)
		return result, err
	}
	command := record.Command
	release, err := d.lockLocalApplicationIntent(ctx, command.InstanceID)
	if err != nil {
		return localApplicationResult{}, err
	}
	defer release()
	previous, err := d.readLocalCommand(command.InstanceID, input.CommandID)
	if err != nil {
		return localApplicationResult{}, err
	}
	if previous != nil && previous.Input.CommandID == input.CommandID {
		if previous.Input != input {
			return localApplicationResult{}, errors.New("local command identity reused with different input")
		}
		if previous.Result != nil {
			if previous.Result.Error != "" {
				return *previous.Result, errors.New(previous.Result.Error)
			}
			return *previous.Result, nil
		}
		observation, inspectErr := d.inspectApplication(ctx, command)
		finished := inspectErr == nil && (input.Action == "stop" && observation.ProcessState == "stopped" || input.Action == "start" && observation.ProcessState == "running" || input.Action == "restart" && observation.ProcessState == "running" && record.HostID != previous.PreviousHostID)
		if finished {
			result := localApplicationResult{CommandID: input.CommandID, Observation: observation, SyncPending: true}
			previous.Result = &result
			if err := d.writeLocalIntent(*previous); err != nil {
				return result, err
			}
			if err := d.queueApplicationObservation(command.RuntimeID, observation); err != nil {
				d.logger.Warn("recovered local application observation deferred", "error", err)
			}
			return result, nil
		}
		return localApplicationResult{}, errors.New("local command outcome is pending; inspect runtime state before issuing another command")
	}
	desired := "running"
	if input.Action == "stop" {
		desired = "stopped"
	}
	intent := localApplicationIntent{Input: input, InstanceID: command.InstanceID, Generation: command.Generation, PreviousHostID: record.HostID, DesiredState: desired}
	if err := d.writeLocalIntent(intent); err != nil {
		return localApplicationResult{}, err
	}
	command.Action = input.Action
	observation, err := d.executeApplicationWithLocalRestart(ctx, command, input.Action == "restart")
	if err != nil {
		if observation.ProcessState == "failed" {
			result := localApplicationResult{CommandID: input.CommandID, Observation: observation, SyncPending: true, Error: applicationHostError(err, command)}
			intent.Result = &result
			if storageErr := d.writeLocalIntent(intent); storageErr != nil {
				d.logger.Warn("local application failure receipt deferred", "error", storageErr)
			}
			if storageErr := d.queueApplicationObservation(command.RuntimeID, observation); storageErr != nil {
				d.logger.Warn("local application failure observation deferred", "error", storageErr)
			}
		}
		return localApplicationResult{}, err
	}
	result := localApplicationResult{CommandID: input.CommandID, Observation: observation, SyncPending: true}
	intent.Result = &result
	if err := d.writeLocalIntent(intent); err != nil {
		return result, err
	}
	if err := d.queueApplicationObservation(command.RuntimeID, observation); err != nil {
		d.logger.Warn("local application finished; observation storage remains pending", "instance_id", command.InstanceID, "error", err)
	}
	return result, nil
}

func (d *Daemon) localApplicationsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !d.jevLocalAuthorized(r) {
			http.Error(w, "local profile authority required", 403)
			return
		}
		if r.Method == http.MethodGet {
			records, err := d.applicationRecords()
			if err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			applications := []map[string]any{}
			for _, record := range records {
				if d.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: record.Command.WorkspaceID, RuntimeID: record.Command.RuntimeID}) {
					applications = append(applications, map[string]any{"application_id": record.Command.ApplicationID, "runtime_id": record.Command.RuntimeID, "workspace_id": record.Command.WorkspaceID, "revision": record.Command.Revision, "instance_id": record.Command.InstanceID, "observation": record.Observation})
				}
			}
			d.applicationCommands.Range(func(_, value any) bool {
				command, ok := value.(protocol.ApplicationControlCommand)
				if !ok || !d.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID}) {
					return true
				}
				for _, existing := range applications {
					if existing["instance_id"] == command.InstanceID {
						return true
					}
				}
				applications = append(applications, map[string]any{"application_id": command.ApplicationID, "runtime_id": command.RuntimeID, "workspace_id": command.WorkspaceID, "revision": command.Revision, "instance_id": command.InstanceID, "observation": applicationObservation(command, "stopped", "unknown", "")})
				return true
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"applications": applications})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var input localApplicationRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid local application command", 400)
			return
		}
		result, err := d.runLocalApplication(r.Context(), input)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}
}
