package agentdesktop

import (
	"encoding/json"
	"sort"
	"strings"
)

type approvalRequirement struct {
	Tool     string
	Commands []string
	CallID   string
}

// approvalForToolCall reports the commands a member must approve before this
// tool call can reach agent-desktop. A batch is approved as one operation.
func approvalForToolCall(tool string, arguments json.RawMessage) approvalRequirement {
	if tool == "desktop_batch" {
		return batchApproval(arguments)
	}
	if commandNeedsApproval(tool, arguments) {
		return approvalRequirement{Tool: tool, Commands: []string{tool}}
	}
	return approvalRequirement{}
}

func batchApproval(arguments json.RawMessage) approvalRequirement {
	requirement := approvalRequirement{Tool: "desktop_batch"}
	var payload struct {
		CommandsJSON string `json:"commands_json"`
	}
	if len(arguments) == 0 || json.Unmarshal(arguments, &payload) != nil || strings.TrimSpace(payload.CommandsJSON) == "" {
		requirement.Commands = []string{"desktop_batch (unvalidated)"}
		return requirement
	}
	var entries []struct {
		Command string          `json:"command"`
		Args    json.RawMessage `json:"args"`
	}
	if json.Unmarshal([]byte(payload.CommandsJSON), &entries) != nil {
		requirement.Commands = []string{"desktop_batch (unvalidated)"}
		return requirement
	}
	commands := make(map[string]struct{})
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Command)
		if name == "" {
			commands["desktop_batch (unvalidated)"] = struct{}{}
			continue
		}
		tool := "desktop_" + strings.ReplaceAll(name, "-", "_")
		if tool == "desktop_batch" {
			commands[tool] = struct{}{}
			continue
		}
		if commandNeedsApproval(tool, entry.Args) {
			commands[tool] = struct{}{}
		}
	}
	for command := range commands {
		requirement.Commands = append(requirement.Commands, command)
	}
	sort.Strings(requirement.Commands)
	return requirement
}

func commandNeedsApproval(tool string, arguments json.RawMessage) bool {
	switch {
	case strings.HasPrefix(tool, "desktop_clipboard_"),
		strings.HasPrefix(tool, "desktop_trace"),
		strings.HasPrefix(tool, "desktop_sandbox_"),
		strings.Contains(tool, "notification"),
		tool == "desktop_close_app",
		tool == "desktop_open_system_surface",
		tool == "desktop_key_down",
		tool == "desktop_key_up",
		tool == "desktop_mouse_down",
		tool == "desktop_mouse_up",
		tool == "desktop_mouse_move",
		tool == "desktop_mouse_click",
		tool == "desktop_mouse_wheel",
		tool == "desktop_batch":
		return true
	case tool == "desktop_permissions":
		var payload struct {
			Request bool `json:"request"`
			Guide   bool `json:"guide"`
		}
		if len(arguments) == 0 {
			return false
		}
		if json.Unmarshal(arguments, &payload) != nil {
			return true
		}
		return payload.Request || payload.Guide
	default:
		return false
	}
}
