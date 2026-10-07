package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"path"
	"regexp"
	"strings"
)

// ApplicationInstanceDependency freezes placement and generation for a prerequisite.
type ApplicationInstanceDependency struct {
	InstanceID string `json:"instance_id"`
	Generation int64  `json:"generation"`
	Condition  string `json:"condition"`
}

// ApplicationControlCommand is a durable, generation-fenced service command.
type ApplicationControlCommand struct {
	InstanceID        string                          `json:"instance_id"`
	ApplicationID     string                          `json:"application_id"`
	WorkspaceID       string                          `json:"workspace_id"`
	RuntimeID         string                          `json:"runtime_id"`
	RootApplicationID string                          `json:"root_application_id"`
	RootRuntimeID     string                          `json:"root_runtime_id"`
	Revision          int64                           `json:"revision"`
	Generation        int64                           `json:"generation"`
	Action            string                          `json:"action"`
	Config            ApplicationConfig               `json:"config"`
	ResourceType      string                          `json:"resource_type,omitempty"`
	ResourceRef       json.RawMessage                 `json:"resource_ref,omitempty"`
	Dependencies      []ApplicationInstanceDependency `json:"dependencies"`
	ConsumerCreated   bool                            `json:"consumer_created"`
	AccessUserID      string                          `json:"access_user_id"`
	AccessMemberID    string                          `json:"access_member_id"`
	AccessAgentID     string                          `json:"access_agent_id,omitempty"`
	Connections       []ApplicationResolvedConnection `json:"connections"`
}

// ApplicationConnection binds an environment URL to a declared dependency.
type ApplicationConnection struct {
	TargetID    string `json:"target_id"`
	URLVariable string `json:"url_variable"`
}

// ApplicationResolvedConnection freezes the selected dependency instance without platform credentials.
type ApplicationResolvedConnection struct {
	URLVariable      string `json:"url_variable"`
	TargetInstanceID string `json:"target_instance_id"`
	TargetGeneration int64  `json:"target_generation"`
	LocalURL         string `json:"local_url,omitempty"`
	ResolverURL      string `json:"resolver_url,omitempty"`
	Grant            string `json:"grant,omitempty"`
}

// ApplicationClaim includes the short-lived lease identity used for acknowledgements.
type ApplicationClaim struct {
	StepID      string                    `json:"step_id"`
	OperationID string                    `json:"operation_id"`
	ClaimToken  string                    `json:"claim_token"`
	Command     ApplicationControlCommand `json:"command"`
}

// ApplicationObservation reports local process facts independently of operation completion.
type ApplicationObservation struct {
	InstanceID   string             `json:"instance_id"`
	Generation   int64              `json:"generation"`
	Revision     int64              `json:"revision"`
	ProcessState string             `json:"process_state"`
	HealthState  string             `json:"health_state"`
	Error        string             `json:"error"`
	CodeVersion  string             `json:"code_version"`
	Dirty        bool               `json:"dirty"`
	StartedAt    *string            `json:"started_at"`
	Metrics      map[string]float64 `json:"metrics"`
}

// ApplicationStepResult acknowledges one exact command claim and its observed state.
type ApplicationStepResult struct {
	ClaimToken  string                 `json:"claim_token"`
	State       string                 `json:"state"`
	Error       string                 `json:"error"`
	Observation ApplicationObservation `json:"observation"`
}

// ApplicationRuntimeInstance is the authoritative desired state for local recovery.
type ApplicationRuntimeInstance struct {
	DesiredState        string                    `json:"desired_state"`
	CanRestore          bool                      `json:"can_restore"`
	HasPendingOperation bool                      `json:"has_pending_operation"`
	ConfirmedStopped    bool                      `json:"confirmed_stopped"`
	Command             ApplicationControlCommand `json:"command"`
}

// DaemonCapabilityApplicationsV1 gates application commands independently of agent runs.
const DaemonCapabilityApplicationsV1 = "applications-v1"

// ApplicationTunnelRequest targets one validated loopback service or local log reader.
type ApplicationTunnelRequest struct {
	StreamID    string `json:"stream_id"`
	Token       string `json:"token"`
	EndpointID  string `json:"endpoint_id"`
	InstanceID  string `json:"instance_id"`
	WorkspaceID string `json:"workspace_id"`
	RuntimeID   string `json:"runtime_id"`
	Generation  int64  `json:"generation"`
	Kind        string `json:"kind"`
	Port        int    `json:"port"`
	Cursor      string `json:"cursor,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// ApplicationCommand describes an executable and its arguments without implicit shell expansion.
type ApplicationCommand struct {
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// ApplicationHealthCheck defines when a service is ready for dependent applications.
type ApplicationHealthCheck struct {
	Kind            string `json:"kind"`
	Path            string `json:"path,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	IntervalSeconds int    `json:"interval_seconds"`
}

// ApplicationRestartPolicy bounds automatic recovery after unexpected process exit.
type ApplicationRestartPolicy struct {
	Enabled      bool `json:"enabled"`
	MaxAttempts  int  `json:"max_attempts"`
	DelaySeconds int  `json:"delay_seconds"`
	Restore      bool `json:"restore"`
}

// ApplicationConfig is an immutable service revision. LocalEnv maps variable names
// to names on the host; its values are references, never uploaded credentials.
type ApplicationConfig struct {
	Mode        string                   `json:"mode"`
	ResourceID  string                   `json:"resource_id,omitempty"`
	Ref         string                   `json:"ref,omitempty"`
	WorkDir     string                   `json:"work_dir,omitempty"`
	Command     []string                 `json:"command"`
	Prepare     []ApplicationCommand     `json:"prepare"`
	Environment map[string]string        `json:"environment"`
	LocalEnv    map[string]string        `json:"local_env"`
	Port        int                      `json:"port"`
	Health      ApplicationHealthCheck   `json:"health"`
	Restart     ApplicationRestartPolicy `json:"restart"`
	AutoPublish bool                     `json:"auto_publish"`
	EntryPath   string                   `json:"entry_path"`
	Connections []ApplicationConnection  `json:"connections"`
}

// DefaultApplicationConfig provides explicit readiness and retry budgets.
func DefaultApplicationConfig() ApplicationConfig {
	return ApplicationConfig{
		Mode: "managed", Command: []string{}, Prepare: []ApplicationCommand{},
		Environment: map[string]string{}, LocalEnv: map[string]string{},
		Health:  ApplicationHealthCheck{Kind: "tcp", TimeoutSeconds: 60, IntervalSeconds: 5},
		Restart: ApplicationRestartPolicy{MaxAttempts: 3, DelaySeconds: 5}, EntryPath: "/",
		Connections: []ApplicationConnection{},
	}
}

var applicationEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Validate rejects ambiguous directories, unbounded retries and invalid service endpoints.
func (c ApplicationConfig) Validate(kind string) error {
	if kind != "service" && kind != "composition" {
		return errors.New("application kind must be service or composition")
	}
	if kind == "composition" {
		if len(c.Command) != 0 || len(c.Prepare) != 0 || c.ResourceID != "" || c.Port != 0 || len(c.Environment) != 0 || len(c.LocalEnv) != 0 || c.AutoPublish || len(c.Connections) > 0 {
			return errors.New("compositions reference services and cannot launch a process")
		}
		return nil
	}
	if c.Mode != "managed" && c.Mode != "external" {
		return errors.New("application mode must be managed or external")
	}
	if len(c.Connections) > 16 || c.Mode == "external" && len(c.Connections) > 0 {
		return errors.New("connections require a managed service and at most 16 bindings")
	}
	variables := map[string]bool{}
	for _, binding := range c.Connections {
		if _, err := uuid.Parse(binding.TargetID); err != nil {
			return errors.New("connection target must be an application UUID")
		}
		if !applicationEnvName.MatchString(binding.URLVariable) || strings.HasPrefix(binding.URLVariable, "MULTICA_") || binding.URLVariable == "PORT" || variables[binding.URLVariable] {
			return errors.New("connection URL variables must be unique application environment names")
		}
		if _, exists := c.Environment[binding.URLVariable]; exists {
			return errors.New("connection URL variable overlaps an environment value")
		}
		if _, exists := c.LocalEnv[binding.URLVariable]; exists {
			return errors.New("connection URL variable overlaps a local environment reference")
		}
		variables[binding.URLVariable] = true
	}
	if c.Port < 0 || c.Port > 65535 || (c.Mode == "external" && c.Port == 0) {
		return errors.New("invalid application port")
	}
	if c.Mode == "managed" {
		if c.ResourceID == "" || len(c.Command) == 0 || strings.TrimSpace(c.Command[0]) == "" {
			return errors.New("managed applications require a resource and command")
		}
	} else if len(c.Command) != 0 || len(c.Prepare) != 0 || c.Restart.Enabled || c.Restart.Restore {
		return errors.New("external services cannot be started or restarted")
	}
	if len(c.Command) > 128 || len(c.Prepare) > 16 {
		return errors.New("too many application command arguments or preparation steps")
	}
	for _, arg := range c.Command {
		if strings.ContainsRune(arg, '\x00') || len(arg) > 16384 {
			return errors.New("invalid application command argument")
		}
	}
	if strings.ContainsAny(c.WorkDir, "\\:\x00") || strings.HasPrefix(c.WorkDir, "/") || path.Clean(c.WorkDir) == ".." || strings.HasPrefix(path.Clean(c.WorkDir), "../") {
		return errors.New("work_dir must stay within the application resource")
	}
	if strings.ContainsAny(c.Ref, "\x00\r\n") || strings.HasPrefix(c.Ref, "-") || len(c.Ref) > 256 {
		return errors.New("invalid application ref")
	}
	for _, step := range c.Prepare {
		if len(step.Args) == 0 || len(step.Args) > 128 || strings.TrimSpace(step.Args[0]) == "" || step.TimeoutSeconds < 1 || step.TimeoutSeconds > 1800 {
			return errors.New("preparation steps require a command and timeout from 1 to 1800 seconds")
		}
		for _, arg := range step.Args {
			if strings.ContainsRune(arg, '\x00') || len(arg) > 16384 {
				return errors.New("invalid preparation command argument")
			}
		}
	}
	if len(c.Environment)+len(c.LocalEnv) > 128 {
		return errors.New("too many application environment variables")
	}
	for name, value := range c.Environment {
		if !applicationEnvName.MatchString(name) || strings.ContainsRune(value, '\x00') || len(value) > 16384 || strings.HasPrefix(name, "MULTICA_") {
			return fmt.Errorf("invalid application environment variable %q", name)
		}
		if _, duplicate := c.LocalEnv[name]; duplicate {
			return fmt.Errorf("environment variable %q has two sources", name)
		}
	}
	for name, source := range c.LocalEnv {
		if !applicationEnvName.MatchString(name) || !applicationEnvName.MatchString(source) || strings.HasPrefix(name, "MULTICA_") || strings.HasPrefix(source, "MULTICA_") {
			return errors.New("invalid local environment reference")
		}
	}
	if c.Health.Kind != "none" && c.Health.Kind != "tcp" && c.Health.Kind != "http" {
		return errors.New("health kind must be none, tcp or http")
	}
	if c.Health.Kind != "none" && (c.Port == 0 || c.Health.TimeoutSeconds < 1 || c.Health.TimeoutSeconds > 600 || c.Health.IntervalSeconds < 1 || c.Health.IntervalSeconds > 60) {
		return errors.New("health checks require a port and bounded check intervals")
	}
	if c.Health.Kind == "http" && !validApplicationPath(c.Health.Path) {
		return errors.New("health path must be an absolute local URL path")
	}
	if c.Restart.MaxAttempts < 0 || c.Restart.MaxAttempts > 10 || c.Restart.DelaySeconds < 1 || c.Restart.DelaySeconds > 300 || (c.Restart.Enabled && c.Restart.MaxAttempts == 0) {
		return errors.New("restart policy requires a bounded delay and at most 10 attempts")
	}
	if !validApplicationPath(c.EntryPath) || (c.AutoPublish && c.Port == 0) {
		return errors.New("publishing requires a port and an absolute local entry path")
	}
	return nil
}

func validApplicationPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "\\\x00\r\n#")
}
