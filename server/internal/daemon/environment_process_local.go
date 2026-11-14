package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/localenv"
)

type localEnvironmentRequest struct {
	Action    string                  `json:"action"`
	Task      localenv.Task           `json:"task"`
	Root      string                  `json:"root,omitempty"`
	RuntimeID string                  `json:"runtime_id,omitempty"`
	Start     environmentStartRequest `json:"start,omitempty"`
	Name      string                  `json:"name,omitempty"`
	Key       string                  `json:"key,omitempty"`
}

type localEnvironmentReply struct {
	environmentResponse
	Error  string `json:"error,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func (d *Daemon) localEnvironmentCall(ctx context.Context, req localEnvironmentRequest) (environmentResponse, error) {
	var reply localEnvironmentReply
	if d.environmentServiceOwner {
		reply = d.executeLocalEnvironment(ctx, req)
	} else {
		client, err := d.ensureEnvironmentProcess(ctx)
		if err != nil {
			return environmentResponse{}, err
		}
		if err := client.mutation(ctx, "environment.local", req, &reply); err != nil {
			return environmentResponse{}, err
		}
	}
	switch reply.Reason {
	case "unsupported":
		return reply.environmentResponse, localenv.ErrUnsupported
	case "not_found":
		return reply.environmentResponse, localenv.ErrNotFound
	case "gone":
		return reply.environmentResponse, localenv.ErrAppGone
	case "focus_unsupported":
		return reply.environmentResponse, localenv.ErrFocusUnsupported
	}
	if reply.Error != "" {
		return reply.environmentResponse, errors.New(reply.Error)
	}
	return reply.environmentResponse, nil
}

func (d *Daemon) executeLocalEnvironment(ctx context.Context, req localEnvironmentRequest) (reply localEnvironmentReply) {
	s := d.environments
	if s == nil {
		reply.Error = "environment service is unavailable"
		return
	}
	var err error
	switch req.Action {
	case "start":
		dir := req.Start.Dir
		if dir == "" {
			dir = req.Task.RootDir
		}
		env, startErr := s.manager.Start(req.Task, dir, strings.TrimSpace(req.Start.Name), req.Start.Path)
		err = startErr
		if err == nil && req.Start.Wait {
			waitCtx, cancel := context.WithTimeout(ctx, environmentStartTimeout)
			env = s.manager.WaitReady(waitCtx, env.Key)
			cancel()
		}
		reply.Environment = &env
		s.nudge()
	case "stop":
		env, stopErr := s.manager.Stop(req.Task, strings.TrimSpace(req.Name))
		err = stopErr
		reply.Environment = &env
		s.nudge()
	case "list":
		reply.Environments = s.manager.List(req.Task)
		if reply.Environments == nil {
			reply.Environments = []localenv.Environment{}
		}
	case "focus":
		err = s.focus(ctx, req.Key)
	case "track":
		s.trackTask(req.Root, req.Task)
	case "untrack":
		s.mu.Lock()
		if s.tasks[req.Root].TaskID == req.Task.TaskID {
			delete(s.tasks, req.Root)
		}
		s.mu.Unlock()
	case "claim":
		d.mu.Lock()
		_, owned := d.runtimeIndex[req.RuntimeID]
		d.mu.Unlock()
		if !owned {
			err = errors.New("unknown environment runtime")
		} else {
			s.claimCommands(ctx, req.RuntimeID)
		}
	case "declared":
		s.reportDeclared(ctx, req.Task.TaskID, req.Root)
	default:
		err = errors.New("unknown local environment action")
	}
	if err == nil {
		return
	}
	reply.Error = err.Error()
	switch {
	case errors.Is(err, localenv.ErrUnsupported):
		reply.Reason = "unsupported"
	case errors.Is(err, localenv.ErrNotFound):
		reply.Reason = "not_found"
	case errors.Is(err, localenv.ErrAppGone):
		reply.Reason = "gone"
	case errors.Is(err, localenv.ErrFocusUnsupported):
		reply.Reason = "focus_unsupported"
	}
	return
}

func (d *Daemon) trackLocalEnvironmentTask(ctx context.Context, root string, task localenv.Task) func() {
 if !d.environmentServiceOwner && !d.environmentProcessMode() { return func(){} }
	if _, err := d.localEnvironmentCall(ctx, localEnvironmentRequest{Action: "track", Root: root, Task: task}); err != nil {
		d.logger.Warn("track task environment", "task", task.TaskID, "error", err)
	}
	return func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if _, err := d.localEnvironmentCall(cleanup, localEnvironmentRequest{Action: "untrack", Root: root, Task: task}); err != nil {
			d.logger.Warn("untrack task environment", "task", task.TaskID, "error", err)
		}
	}
}

func (d *Daemon) claimEnvironmentCommands(ctx context.Context, runtimeID string) {
	if _, err := d.localEnvironmentCall(ctx, localEnvironmentRequest{Action: "claim", RuntimeID: runtimeID}); err != nil {
		d.logger.Warn("claim environment commands", "error", err)
	}
}

func decodeLocalEnvironment(raw json.RawMessage) (localEnvironmentRequest, error) {
	var req localEnvironmentRequest
	err := decodeEnvironmentPayload(raw, &req)
	return req, err
}
