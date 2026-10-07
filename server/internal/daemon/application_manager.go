package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) applicationInstanceLock(id string) *sync.Mutex {
	lock, _ := d.applicationLocks.LoadOrStore(id, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func applicationObservation(command protocol.ApplicationControlCommand, process, health, message string) protocol.ApplicationObservation {
	if len(message) > 2048 {
		message = message[:2048]
	}
	return protocol.ApplicationObservation{InstanceID: command.InstanceID, Generation: command.Generation, Revision: command.Revision, ProcessState: process, HealthState: health, Error: message, Metrics: map[string]float64{}}
}

func matchingApplicationRecord(record applicationhost.Record, command protocol.ApplicationControlCommand) bool {
	return record.Command.InstanceID == command.InstanceID && record.Command.WorkspaceID == command.WorkspaceID && record.Command.RuntimeID == command.RuntimeID && record.Command.ApplicationID == command.ApplicationID
}

func (d *Daemon) applicationRecord(command protocol.ApplicationControlCommand) (string, applicationhost.Record, error) {
	directory, err := d.applicationDirectory(command)
	if err != nil {
		return "", applicationhost.Record{}, err
	}
	path := filepath.Join(directory, "host.json")
	record, err := applicationhost.ReadRecord(path)
	if err != nil {
		return path, record, err
	}
	if !matchingApplicationRecord(record, command) {
		return path, record, errors.New("application host scope changed")
	}
	return path, record, nil
}

func (d *Daemon) applicationHostProcess(path string) (*exec.Cmd, error) {
	if d.applicationHostLauncher != nil {
		return d.applicationHostLauncher(path)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, applicationhost.Entrypoint, path)
	detachApplicationHost(cmd)
	return cmd, nil
}

func applicationHostEnvironment(config protocol.ApplicationConfig) ([]string, error) {
	environment, _, err := applicationhost.Environment(config)
	if err != nil {
		return nil, err
	}
	for _, source := range config.LocalEnv {
		value, ok := os.LookupEnv(source)
		if !ok {
			return nil, fmt.Errorf("local environment reference %s is unavailable", source)
		}
		environment = append(environment, source+"="+value)
	}
	return environment, nil
}

func (d *Daemon) launchApplicationHost(ctx context.Context, command protocol.ApplicationControlCommand) (*applicationhost.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for index := range command.Connections {
		binding := &command.Connections[index]
		if binding.ResolverURL != "" {
			base, err := url.Parse(d.client.baseURL)
			if err != nil {
				return nil, err
			}
			path, err := url.Parse(binding.ResolverURL)
			if err != nil || path.IsAbs() || path.Host != "" || path.Path != "/api/application-connections/resolve" {
				return nil, errors.New("invalid application connection resolver")
			}
			binding.ResolverURL = base.ResolveReference(path).String()
		}
	}
	sourceRoot, workDir, version, dirty, err := d.applicationSource(ctx, command)
	if err != nil {
		d.recordUnstartedApplication(command, err)
		return nil, err
	}
	record, err := applicationhost.NewRecord(command, workDir)
	if err != nil {
		return nil, err
	}
	record.SourceRoot = sourceRoot
	record.Observation.CodeVersion = version
	record.Observation.Dirty = dirty
	directory, err := d.applicationDirectory(command)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "host.json")
	if err = applicationhost.WriteRecord(path, record); err != nil {
		return nil, err
	}
	cmd, err := d.applicationHostProcess(path)
	if err != nil {
		d.recordUnstartedApplication(command, err)
		return nil, err
	}
	cmd.Env, err = applicationHostEnvironment(command.Config)
	if err != nil {
		d.recordUnstartedApplication(command, err)
		return nil, err
	}
	cmd.Dir = directory
	diagnostics, err := os.OpenFile(filepath.Join(directory, "host-start.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		d.recordUnstartedApplication(command, err)
		return nil, err
	}
	cmd.Stdout = diagnostics
	cmd.Stderr = diagnostics
	if err = ctx.Err(); err == nil {
		err = cmd.Start()
	}
	if err != nil {
		diagnostics.Close()
		d.recordUnstartedApplication(command, err)
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait(); diagnostics.Close() }()
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, readErr := applicationhost.ReadRecord(path)
		if readErr == nil && current.HostID == record.HostID && current.Address != "" {
			client, clientErr := applicationhost.NewClient(current)
			if clientErr != nil {
				return nil, clientErr
			}
			if _, clientErr = client.Status(ctx); clientErr == nil {
				return client, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err = <-exited:
			if err == nil {
				err = errors.New("application host exited before readiness")
			}
			return nil, fmt.Errorf("application host could not start: %w", err)
		case <-timeout.C:
			return nil, errors.New("application host did not announce its control channel")
		case <-ticker.C:
		}
	}
}

func waitApplicationReady(ctx context.Context, client *applicationhost.Client, command protocol.ApplicationControlCommand) (protocol.ApplicationObservation, error) {
	budget := max(1, command.Config.Health.TimeoutSeconds) + 15
	for _, step := range command.Config.Prepare {
		budget += step.TimeoutSeconds
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(budget)*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	observation := applicationObservation(command, "unknown", "unknown", "")
	for {
		status, err := client.Status(waitCtx)
		if err != nil {
			return observation, err
		}
		observation = status.Observation
		if observation.Generation != command.Generation || observation.Revision != command.Revision {
			return observation, errors.New("application host generation or revision differs from the command")
		}
		if observation.ProcessState == "failed" || observation.ProcessState == "unknown" || observation.ProcessState == "stopped" {
			return observation, errors.New("application service did not start successfully")
		}
		if observation.ProcessState == "running" && (observation.HealthState == "healthy" || command.Config.Health.Kind == "none") {
			return observation, nil
		}
		select {
		case <-waitCtx.Done():
			return observation, errors.New("application did not become ready before its deadline")
		case <-ticker.C:
		}
	}
}

func (d *Daemon) stopApplication(ctx context.Context, command protocol.ApplicationControlCommand) (protocol.ApplicationObservation, error) {
	if command.Config.Mode == "external" {
		return applicationObservation(command, "stopped", "unknown", ""), nil
	}
	path, record, err := d.applicationRecord(command)
	if errors.Is(err, os.ErrNotExist) {
		return applicationObservation(command, "unknown", "unknown", "application ownership record is missing"), errors.New("application ownership record is missing; shutdown cannot be confirmed")
	}
	if err != nil {
		return applicationObservation(command, "unknown", "unknown", err.Error()), err
	}
	if record.Command.Generation > command.Generation {
		return applicationObservation(command, "unknown", "unknown", "local service has a newer generation"), errors.New("local application generation is newer than this command")
	}
	previousBoot, bootErr := applicationhost.PreviousBoot(record)
	if bootErr != nil {
		return applicationObservation(command, "unknown", "unknown", "machine boot identity could not be verified"), bootErr
	}
	if previousBoot {
		record.BootID, err = applicationhost.CurrentBootID()
		if err != nil {
			return applicationObservation(command, "unknown", "unknown", "machine boot identity could not be verified"), err
		}
		record.Command.Generation = command.Generation
		record.Command.Revision = command.Revision
		record.Observation = applicationObservation(command, "stopped", "unknown", "")
		record.Address = ""
		if err := applicationhost.WriteRecord(path, record); err != nil {
			return record.Observation, err
		}
		return record.Observation, nil
	}
	client, err := applicationhost.NewClient(record)
	if err == nil {
		status, stopErr := client.Stop(ctx, command.Generation)
		if stopErr == nil {
			if err := applicationhost.WaitStopped(ctx, path, record.HostID); err != nil {
				return applicationObservation(command, "unknown", "unknown", "application host shutdown is incomplete"), err
			}
			status.Observation.Revision = command.Revision
			return status.Observation, nil
		}
		err = stopErr
	}
	if record.Observation.ProcessState == "stopped" {
		if err := applicationhost.WaitStopped(ctx, path, record.HostID); err != nil {
			return applicationObservation(command, "unknown", "unknown", "application host shutdown is incomplete"), err
		}
		return applicationObservation(command, "stopped", "unknown", ""), nil
	}
	return applicationObservation(command, "unknown", "unknown", "service shutdown could not be confirmed"), err
}

func (d *Daemon) recordUnstartedApplication(command protocol.ApplicationControlCommand, cause error) {
	directory, err := d.applicationDirectory(command)
	if err != nil {
		return
	}
	path := filepath.Join(directory, "host.json")
	record, readErr := applicationhost.ReadRecord(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return
	}
	if readErr == nil && (!matchingApplicationRecord(record, command) || record.Address != "" && record.Observation.ProcessState != "stopped") {
		return
	}
	record, err = applicationhost.NewRecord(command, directory)
	if err != nil {
		return
	}
	record.Observation = applicationObservation(command, "stopped", "unknown", applicationHostError(cause, command))
	if err = applicationhost.WriteRecord(path, record); err != nil {
		d.logger.Debug("application pre-start failure receipt could not be saved", "instance_id", command.InstanceID, "error", err)
	}
}

func (d *Daemon) executeApplication(ctx context.Context, command protocol.ApplicationControlCommand) (protocol.ApplicationObservation, error) {
	lock := d.applicationInstanceLock(command.InstanceID)
	lock.Lock()
	defer lock.Unlock()
	executionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.applicationExecutions.Store(command.InstanceID, applicationExecution{workspaceID: command.WorkspaceID, runtimeID: command.RuntimeID, cancel: cancel})
	defer d.applicationExecutions.Delete(command.InstanceID)
	ctx = executionCtx
	if d.rememberApplicationGeneration(command) > command.Generation {
		return applicationObservation(command, "unknown", "unknown", "application command was superseded"), errors.New("application command was superseded")
	}
	if !d.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID}) {
		return applicationObservation(command, "unknown", "unknown", "runtime ownership changed"), errors.New("runtime ownership changed")
	}
	if command.Generation < 1 || command.Revision < 1 {
		return applicationObservation(command, "unknown", "unknown", "invalid application command"), errors.New("invalid application command")
	}
	if command.Action == "stop" {
		return d.stopApplication(ctx, command)
	}
	if command.Action == "unpublish" {
		return d.inspectApplication(ctx, command)
	}
	if command.Config.Mode == "external" {
		if command.Action == "restart" {
			return applicationObservation(command, "unknown", "unknown", "external services cannot be restarted"), errors.New("external services cannot be restarted")
		}
		deadline, cancel := context.WithTimeout(ctx, time.Duration(max(1, command.Config.Health.TimeoutSeconds))*time.Second)
		defer cancel()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			observation, err := d.inspectApplication(deadline, command)
			if err == nil && observation.ProcessState == "running" && (observation.HealthState == "healthy" || command.Config.Health.Kind == "none") {
				return observation, nil
			}
			select {
			case <-deadline.Done():
				return observation, errors.New("external application service is not ready")
			case <-ticker.C:
			}
		}
	}
	if command.Action == "resume" {
		_, record, err := d.applicationRecord(command)
		if err != nil {
			return applicationObservation(command, "unknown", "unknown", "existing application ownership is unavailable"), err
		}
		client, err := applicationhost.NewClient(record)
		if err != nil {
			return applicationObservation(command, "unknown", "unknown", "existing application ownership is unavailable"), err
		}
		status, err := client.Resume(ctx, command.Generation, command.Revision)
		return status.Observation, err
	}
	_, record, err := d.applicationRecord(command)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return applicationObservation(command, "unknown", "unknown", err.Error()), err
	}
	var client *applicationhost.Client
	if err == nil {
		if record.Command.Generation > command.Generation {
			return applicationObservation(command, "unknown", "unknown", "stale application command"), errors.New("stale application command")
		}
		previousBoot, bootErr := applicationhost.PreviousBoot(record)
		if bootErr != nil {
			return applicationObservation(command, "unknown", "unknown", "machine boot identity could not be verified"), bootErr
		}
		if previousBoot {
			record.Observation.ProcessState = "stopped"
			record.Address = ""
		}
		client, err = applicationhost.NewClient(record)
		if err == nil {
			status, statusErr := client.Status(ctx)
			if statusErr == nil {
				if record.Command.Generation == command.Generation && record.Command.Revision == command.Revision {
					return waitApplicationReady(ctx, client, command)
				}
				if command.Action != "restart" && status.Observation.ProcessState != "stopped" && status.Observation.ProcessState != "failed" {
					return status.Observation, errors.New("an earlier service is still running; stop or restart it")
				}
				if _, err = client.Stop(ctx, command.Generation); err != nil {
					return status.Observation, err
				}
			} else if record.Observation.ProcessState != "stopped" {
				return status.Observation, errors.New("application host ownership is unavailable; process replacement was refused")
			}
		} else if record.Observation.ProcessState != "stopped" {
			return applicationObservation(command, "unknown", "unknown", "application host ownership is unavailable"), err
		}
	}
	if command.Action == "publish" {
		return applicationObservation(command, "unknown", "unknown", "start the application before publishing"), errors.New("application host is not running")
	}
	client, err = d.launchApplicationHost(ctx, command)
	if err != nil {
		return applicationObservation(command, "failed", "unknown", err.Error()), err
	}
	return waitApplicationReady(ctx, client, command)
}

func (d *Daemon) rememberApplicationGeneration(command protocol.ApplicationControlCommand) int64 {
	for {
		stored, loaded := d.applicationGenerations.LoadOrStore(command.InstanceID, command.Generation)
		if !loaded {
			return command.Generation
		}
		generation := stored.(int64)
		if generation >= command.Generation {
			return generation
		}
		if d.applicationGenerations.CompareAndSwap(command.InstanceID, generation, command.Generation) {
			return command.Generation
		}
	}
}

func (d *Daemon) inspectApplication(ctx context.Context, command protocol.ApplicationControlCommand) (protocol.ApplicationObservation, error) {
	if command.Config.Mode == "external" {
		observation := applicationObservation(command, "unknown", "unhealthy", "")
		connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(command.Config.Port)))
		if err != nil {
			return observation, errors.New("external application port is unavailable")
		}
		if err = connection.Close(); err != nil {
			return observation, err
		}
		observation.ProcessState = "running"
		if err = applicationhost.CheckHealth(ctx, command.Config); err != nil {
			observation.Error = err.Error()
			return observation, err
		}
		observation.HealthState = "healthy"
		if command.Config.Health.Kind == "none" {
			observation.HealthState = "none"
		}
		return observation, nil
	}
	_, record, err := d.applicationRecord(command)
	if err != nil {
		return applicationObservation(command, "unknown", "unknown", "application host is unavailable"), err
	}
	previousBoot, bootErr := applicationhost.PreviousBoot(record)
	if bootErr != nil {
		return applicationObservation(command, "unknown", "unknown", "machine boot identity could not be verified"), bootErr
	}
	if previousBoot {
		return applicationObservation(command, "stopped", "unknown", "machine restarted since the application was running"), errors.New("machine restarted since the application was running")
	}
	client, err := applicationhost.NewClient(record)
	if err != nil {
		return applicationObservation(command, "unknown", "unknown", err.Error()), err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return applicationObservation(command, "unknown", "unknown", err.Error()), err
	}
	if status.Observation.Generation != command.Generation || status.Observation.Revision != command.Revision {
		return applicationObservation(command, "unknown", "unknown", "waiting for the requested application revision"), errors.New("application revision is not applied")
	}
	return status.Observation, nil
}

func applicationHostError(err error, command protocol.ApplicationControlCommand) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, source := range command.Config.LocalEnv {
		if secret, ok := os.LookupEnv(source); ok && secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	for name, value := range command.Config.Environment {
		lower := strings.ToLower(name)
		if value != "" && (strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "key")) {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	if len(message) > 2048 {
		message = message[:2048]
	}
	return message
}
