package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// EnvironmentSelection is an opaque runtime-owned identity and a preview revision.
type EnvironmentSelection struct {
	EnvironmentID string `json:"environment_id"`
	Revision      string `json:"revision"`
}

// EnvironmentOperationRequest starts a scoped, receipt-backed runtime operation.
type EnvironmentOperationRequest struct {
	ID         string                 `json:"id"`
	Action     string                 `json:"action"`
	Selections []EnvironmentSelection `json:"selections"`
	ArchiveID  string                 `json:"archive_id,omitempty"`
}

// EnvironmentCommand is forwarded only to the runtime selected by its owner.
type EnvironmentCommand struct {
	Action      string                       `json:"action"`
	Operation   *EnvironmentOperationRequest `json:"operation,omitempty"`
	OperationID string                       `json:"operation_id,omitempty"`
	Policy      *EnvironmentPolicy           `json:"policy,omitempty"`
}

type EnvironmentPolicy struct {
	Enabled                 bool  `json:"enabled"`
	ArchiveAfterHours       int   `json:"archive_after_hours"`
	CacheAfterHours         int   `json:"cache_after_hours"`
	PressureCacheAfterHours int   `json:"pressure_cache_after_hours"`
	MaxIdleEnvironments     int   `json:"max_idle_environments"`
	MaxDirectoryBytes       int64 `json:"max_directory_bytes"`
	MinimumFreeBytes        int64 `json:"minimum_free_bytes"`
}

func (p EnvironmentPolicy) Validate() error {
	if p.CacheAfterHours > 0 && p.PressureCacheAfterHours > p.CacheAfterHours {
		return errors.New("pressure cache retention must not exceed normal cache retention")
	}
	if p.ArchiveAfterHours < 0 || p.ArchiveAfterHours > 87600 || p.CacheAfterHours < 0 || p.CacheAfterHours > 87600 ||
		p.PressureCacheAfterHours < 0 || p.PressureCacheAfterHours > 87600 || p.MaxIdleEnvironments < 0 || p.MaxIdleEnvironments > 100000 ||
		p.MaxDirectoryBytes < 0 || p.MaxDirectoryBytes > 1<<50 || p.MinimumFreeBytes < 0 || p.MinimumFreeBytes > 1<<50 {
		return errors.New("environment policy values outside supported range")
	}
	return nil
}

type EnvironmentPolicyStatus struct {
	WorkspaceID         string            `json:"workspace_id"`
	RuntimeID           string            `json:"runtime_id"`
	Policy              EnvironmentPolicy `json:"policy"`
	EffectiveEnabled    bool              `json:"effective_enabled"`
	ScanIntervalSeconds int64             `json:"scan_interval_seconds"`
	FreeBytes           *uint64           `json:"free_bytes"`
	LastScanAt          *time.Time        `json:"last_scan_at"`
	IdleEnvironments    int               `json:"idle_environments"`
	DirectoryBytes      int64             `json:"directory_bytes"`
	UnderPressure       bool              `json:"under_pressure"`
}

// EnvironmentOperationStatus remains available on the owning daemon after a
// client disconnect, including partial results and interrupted operations.
type EnvironmentOperationStatus struct {
	ID          string            `json:"id"`
	Action      string            `json:"action"`
	Status      string            `json:"status"`
	Automatic   bool              `json:"automatic,omitempty"`
	WorkspaceID string            `json:"workspace_id"`
	RuntimeID   string            `json:"runtime_id"`
	DaemonID    string            `json:"daemon_id"`
	Profile     string            `json:"profile"`
	StartedAt   time.Time         `json:"started_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Completed   int               `json:"completed"`
	Total       int               `json:"total"`
	Results     []json.RawMessage `json:"results"`
	Error       string            `json:"error"`
}

// ValidEnvironmentIdentity recognizes generated SHA-256 ids, never paths.
func ValidEnvironmentIdentity(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func (command EnvironmentCommand) Validate() error {
	if command.Action != "policy_update" && command.Policy != nil {
		return errors.New("policy input only allowed for policy update")
	}
	switch command.Action {
	case "inventory", "cache_preview", "archive_preview", "archives", "operations", "policy":
		if command.Operation != nil || command.OperationID != "" {
			return errors.New("read operation cannot carry mutation input")
		}
	case "policy_update":
		if command.Policy == nil || command.Operation != nil || command.OperationID != "" {
			return errors.New("policy update requires only policy input")
		}
		return command.Policy.Validate()
	case "operation_status", "operation_cancel":
		if !ValidEnvironmentIdentity(command.OperationID) || command.Operation != nil {
			return errors.New("operation identity required")
		}
	case "operation_start":
		if command.Operation == nil || command.OperationID != "" {
			return errors.New("operation input required")
		}
		return command.Operation.Validate()
	default:
		return errors.New("unknown environment action")
	}
	return nil
}

func (request EnvironmentOperationRequest) Validate() error {
	if !ValidEnvironmentIdentity(request.ID) || len(request.Selections) > 1000 {
		return errors.New("valid bounded operation identity required")
	}
	if request.Action == "restore" {
		if !ValidEnvironmentIdentity(request.ArchiveID) || len(request.Selections) != 0 {
			return errors.New("restore archive identity required")
		}
		return nil
	}
	if request.ArchiveID != "" || len(request.Selections) == 0 {
		return errors.New("environment selections required")
	}
	switch request.Action {
	case "clean_cache", "archive", "cleanup", "discard":
	default:
		return errors.New("unknown environment operation")
	}
	seen := make(map[string]bool)
	for _, selection := range request.Selections {
		if !ValidEnvironmentIdentity(selection.EnvironmentID) || seen[selection.EnvironmentID] {
			return errors.New("unique environment identities required")
		}
		if (request.Action == "archive" || request.Action == "clean_cache") && !ValidEnvironmentIdentity(selection.Revision) {
			return errors.New("preview revision required")
		}
		seen[selection.EnvironmentID] = true
	}
	return nil
}
