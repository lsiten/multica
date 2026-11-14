package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/internal/daemon/localenv"
)

type localEnvironmentFactPayload struct {
	Environments []TaskEnvironmentReport  `json:"environments,omitempty"`
	Declared     []localenv.Declared      `json:"declared,omitempty"`
	CommandID    string                   `json:"command_id,omitempty"`
	Result       EnvironmentCommandResult `json:"result,omitempty"`
}

type localEnvironmentReporter struct{ facts *environmentFactTransport }

func (r localEnvironmentReporter) send(ctx context.Context, fact environmentFact, result any) error {
	reply, err := r.facts.fetch(ctx, fact)
	if err != nil {
		return err
	}
	if reply.Status != 200 {
		return fmt.Errorf("environment report failed with status %d", reply.Status)
	}
	if result != nil {
		return json.Unmarshal(reply.Body, result)
	}
	return nil
}
func (r localEnvironmentReporter) ReportTaskEnvironments(ctx context.Context, id string, envs []TaskEnvironmentReport) error {
	return r.send(ctx, environmentFact{Kind: "local_environments", ID: id, Local: &localEnvironmentFactPayload{Environments: envs}}, nil)
}
func (r localEnvironmentReporter) ReportTaskEnvironmentConfigs(ctx context.Context, id string, envs []localenv.Declared) error {
	return r.send(ctx, environmentFact{Kind: "local_declared", ID: id, Local: &localEnvironmentFactPayload{Declared: envs}}, nil)
}
func (r localEnvironmentReporter) ClaimEnvironmentCommands(ctx context.Context, id string) ([]EnvironmentCommand, error) {
	var commands []EnvironmentCommand
	err := r.send(ctx, environmentFact{Kind: "local_claim", RuntimeID: id}, &commands)
	return commands, err
}
func (r localEnvironmentReporter) ReportEnvironmentCommandResult(ctx context.Context, runtimeID, commandID string, result EnvironmentCommandResult) error {
	return r.send(ctx, environmentFact{Kind: "local_result", RuntimeID: runtimeID, Local: &localEnvironmentFactPayload{CommandID: commandID, Result: result}}, nil)
}

func (c *environmentFactCallback) localEnvironmentFact(ctx context.Context, fact environmentFact) environmentFactReply {
	var value any = struct{}{}
	var err error
	switch fact.Kind {
	case "local_environments", "local_declared":
		if fact.Local == nil || fact.ID == "" {
			err = errors.New("missing environment report")
			break
		}
		var task *TaskGCStatus
		task, err = c.daemon.client.GetTaskGCCheck(ctx, fact.ID)
		if err != nil {
			break
		}
		if task == nil {
			err = errors.New("missing task environment owner")
			break
		}
		c.daemon.mu.Lock()
		_, owned := c.daemon.runtimeIndex[task.RuntimeID]
		c.daemon.mu.Unlock()
		if !owned || task.Missing {
			err = errors.New("task is outside this environment owner")
			break
		}
		if fact.Kind == "local_environments" {
			err = c.daemon.client.ReportTaskEnvironments(ctx, fact.ID, fact.Local.Environments)
		} else {
			err = c.daemon.client.ReportTaskEnvironmentConfigs(ctx, fact.ID, fact.Local.Declared)
		}
	case "local_claim", "local_result":
		c.daemon.mu.Lock()
		_, owned := c.daemon.runtimeIndex[fact.RuntimeID]
		c.daemon.mu.Unlock()
		if !owned {
			err = errors.New("runtime is outside this environment owner")
			break
		}
		if fact.Kind == "local_claim" {
			value, err = c.daemon.client.ClaimEnvironmentCommands(ctx, fact.RuntimeID)
		} else if fact.Local != nil {
			err = c.daemon.client.ReportEnvironmentCommandResult(ctx, fact.RuntimeID, fact.Local.CommandID, fact.Local.Result)
		} else {
			err = errors.New("missing command result")
		}
	default:
		err = errors.New("unsupported environment report")
	}
	if err != nil {
		return environmentFactReply{Status: 503, Body: json.RawMessage(`{"error":"environment facts unavailable"}`)}
	}
	body, err := json.Marshal(value)
	if err != nil {
		return environmentFactReply{Status: 500, Body: json.RawMessage(`{"error":"invalid environment facts"}`)}
	}
	return environmentFactReply{Status: 200, Body: body}
}
