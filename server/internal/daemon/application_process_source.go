package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Only physical-source inputs cross back to the current repository owner.
type applicationSourceRequest struct {
	ResourceID    string            `json:"resource_id"`
	WorkspaceID   string            `json:"workspace_id"`
	RuntimeID     string            `json:"runtime_id"`
	ApplicationID string            `json:"application_id"`
	InstanceID    string            `json:"instance_id"`
	Revision      int64             `json:"revision"`
	ResourceType  string            `json:"resource_type"`
	ResourceRef   json.RawMessage   `json:"resource_ref"`
	Ref           string            `json:"ref"`
	WorkDir       string            `json:"work_dir"`
	LocalEnv      map[string]string `json:"local_env"`
}
type applicationPreparedSource struct {
	Root        string            `json:"root"`
	WorkDir     string            `json:"work_dir"`
	Version     string            `json:"version"`
	Dirty       bool              `json:"dirty"`
	LocalValues map[string]string `json:"local_values"`
}

func applicationSourceInput(command protocol.ApplicationControlCommand) applicationSourceRequest {
	return applicationSourceRequest{ResourceID: command.Config.ResourceID, WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID, ApplicationID: command.ApplicationID, InstanceID: command.InstanceID, Revision: command.Revision, ResourceType: command.ResourceType, ResourceRef: command.ResourceRef, Ref: command.Config.Ref, WorkDir: command.Config.WorkDir, LocalEnv: command.Config.LocalEnv}
}
func (d *Daemon) prepareApplicationSource(ctx context.Context, command protocol.ApplicationControlCommand) (applicationPreparedSource, error) {
	if d.applicationSourceProvider != nil {
		return d.applicationSourceProvider(ctx, applicationSourceInput(command))
	}
	var prepared applicationPreparedSource
	var err error
	if d.environmentProcessMode() {
		child, childErr := d.ensureEnvironmentProcess(ctx)
		if childErr == nil {
			childErr = child.sync(ctx)
		}
		if childErr == nil {
			childErr = child.mutation(ctx, "application.source", environmentApplicationSource{ResourceID: command.Config.ResourceID, WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID, ApplicationID: command.ApplicationID, InstanceID: command.InstanceID, Revision: command.Revision, ResourceType: command.ResourceType, ResourceRef: command.ResourceRef, Ref: command.Config.Ref, WorkDir: command.Config.WorkDir}, &prepared)
		}
		err = childErr
	} else {
		prepared.Root, prepared.WorkDir, prepared.Version, prepared.Dirty, err = d.applicationSource(ctx, command)
	}
	if err != nil {
		return prepared, err
	}
	prepared.LocalValues = map[string]string{}
	for _, source := range command.Config.LocalEnv {
		value, ok := os.LookupEnv(source)
		if !ok {
			return prepared, fmt.Errorf("local environment reference %s is unavailable", source)
		}
		prepared.LocalValues[source] = value
	}
	return prepared, nil
}
func preparedApplicationEnvironment(config protocol.ApplicationConfig, prepared applicationPreparedSource) ([]string, error) {
	// Preserve LocalEnv references in the protected host record. Values exist only
	// in this launch environment and are resolved by the actual host process.
	refs := config.LocalEnv
	config.LocalEnv = nil
	environment, _, err := applicationhost.Environment(config)
	if err != nil {
		return nil, err
	}
	for name, value := range prepared.LocalValues {
		declared := false
		for _, source := range refs {
			if source == name {
				declared = true
				break
			}
		}
		if !declared {
			return nil, errors.New("undeclared application environment value")
		}
		environment = append(environment, name+"="+value)
	}
	for _, source := range refs {
		if _, ok := prepared.LocalValues[source]; !ok {
			return nil, fmt.Errorf("local environment reference %s is unavailable", source)
		}
	}
	return environment, nil
}
