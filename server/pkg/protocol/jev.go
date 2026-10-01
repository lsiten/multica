package protocol

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"time"
)

const (
	DaemonCapabilityJevV1   = "jev-models-v1"
	MapikaDecider2BRevision = "533964dae8be954c5b5e19fa4948e48408094c1e"
	EventJevOperation       = "jev:operation"
	EventJevResult          = "jev:result"
)

type WorkspaceJevConfig struct {
	Source         string `json:"source"`
	ModelID        string `json:"model_id,omitempty"`
	ModelRevision  string `json:"model_revision,omitempty"`
	Device         string `json:"device,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
	CredentialEnv  string `json:"credential_env,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Revision       int64  `json:"revision"`
}

func DefaultWorkspaceJevConfig() WorkspaceJevConfig {
	return WorkspaceJevConfig{Source: "agent_context", TimeoutSeconds: 45}
}
func (c WorkspaceJevConfig) Validate() error {
	invalid := errors.New("invalid Jev model configuration")
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 300 || c.Revision < 0 {
		return invalid
	}
	switch c.Source {
	case "agent_context":
		if c.Endpoint != "" || c.ModelID != "" || c.CredentialEnv != "" || c.ModelRevision != "" || c.Device != "" {
			return invalid
		}
	case "local":
		if c.ModelID != "Mapika/decider-2b" || c.ModelRevision != MapikaDecider2BRevision || c.Endpoint != "" || c.CredentialEnv != "" {
			return invalid
		}
		switch c.Device {
		case "auto", "cpu", "cuda", "mps":
		default:
			return invalid
		}
	case "remote":
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || c.ModelID == "" || len(c.ModelID) > 200 || c.Device != "" || c.ModelRevision != "" {
			return invalid
		}
		if c.CredentialEnv != "" && !regexp.MustCompile(`^JEV_[A-Z0-9_]{1,80}$`).MatchString(c.CredentialEnv) {
			return invalid
		}
	default:
		return invalid
	}
	return nil
}

type JevOperation struct {
	VscreenEnvelope
	Action    string    `json:"action"`
	ModelID   string    `json:"model_id,omitempty"`
	Revision  string    `json:"revision,omitempty"`
	Device    string    `json:"device,omitempty"`
	Confirmed bool      `json:"confirmed"`
	Deadline  time.Time `json:"deadline"`
}
type JevResult struct {
	VscreenEnvelope
	Data   json.RawMessage `json:"data,omitempty"`
	Reason string          `json:"reason,omitempty"`
}
