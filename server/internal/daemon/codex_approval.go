package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) requestTaskApproval(task Task) func(context.Context, agent.ApprovalRequest) (bool, error) {
	return func(ctx context.Context, request agent.ApprovalRequest) (bool, error) {
		if task.InitiatorType != "member" || task.InitiatorID == "" {
			return false, errors.New("daemon: no human approval initiator")
		}
		d.mu.Lock()
		rm := d.runtimeMirrors[task.RuntimeID]
		d.mu.Unlock()
		if rm == nil {
			return false, errors.New("daemon: no mirror reviewer connected")
		}
		operation := codexApprovalOperation(request)
		return rm.RequestCLIApproval(ctx, mirror.CLIApprovalAudience{WorkspaceID: task.WorkspaceID, RuntimeID: task.RuntimeID, UserID: task.InitiatorID}, "Codex operation approval", operation.Target, operation)
	}
}

func codexApprovalOperation(request agent.ApprovalRequest) *protocol.MirrorCLIOperation {
	operation := &protocol.MirrorCLIOperation{Kind: "unknown", Target: "Review the exact requested operation", Details: string(request.Params)}
	var params map[string]json.RawMessage
	if json.Unmarshal(request.Params, &params) != nil {
		return operation
	}
	read := func(key string) string {
		var value string
		if json.Unmarshal(params[key], &value) == nil {
			return value
		}
		return ""
	}
	operation.Location, operation.Reason = read("cwd"), read("reason")
	switch request.Method {
	case "item/commandExecution/requestApproval", "execCommandApproval":
		operation.Kind = "command"
		operation.Target = read("command")
		if operation.Target == "" {
			var args []string
			if json.Unmarshal(params["command"], &args) == nil {
				for index, arg := range args {
					args[index] = strconv.Quote(arg)
				}
				operation.Target = strings.Join(args, " ")
			}
		}
		if operation.Target == "" {
			operation.Kind = "unknown"
			operation.Target = "Review the exact command in details"
		}
	case "item/fileChange/requestApproval", "applyPatchApproval":
		operation.Kind, operation.Target = "files", "Review the requested file changes"
		for _, file := range request.FileChanges {
			operation.Files = append(operation.Files, protocol.MirrorCLIFileChange{Path: file.Path, Kind: file.Kind, MovePath: file.MovePath})
		}
		for _, key := range []string{"changes", "fileChanges", "file_changes"} {
			var changes map[string]json.RawMessage
			if json.Unmarshal(params[key], &changes) == nil && len(changes) > 0 {
				paths := make([]string, 0, len(changes))
				for path := range changes {
					paths = append(paths, strconv.Quote(path))
				}
				sort.Strings(paths)
				operation.Target = strings.Join(paths, "\n")
				break
			}
		}
	case "item/permissions/requestApproval":
		operation.Kind = "permissions"
		if raw := params["permissions"]; len(raw) > 0 {
			operation.Target = string(raw)
			operation.Permissions = codexPermissionPresentation(raw)
		} else {
			operation.Kind = "unknown"
		}
	}
	return operation
}

func codexPermissionPresentation(raw json.RawMessage) *protocol.MirrorCLIPermissions {
	var permissions map[string]json.RawMessage
	if json.Unmarshal(raw, &permissions) != nil {
		return nil
	}
	result := &protocol.MirrorCLIPermissions{}
	for key, value := range permissions {
		switch key {
		case "network":
			var network map[string]json.RawMessage
			if json.Unmarshal(value, &network) != nil || len(network) != 1 {
				return nil
			}
			var enabled bool
			if json.Unmarshal(network["enabled"], &enabled) != nil {
				return nil
			}
			result.NetworkEnabled = &enabled
		case "fileSystem":
			var files map[string]json.RawMessage
			if json.Unmarshal(value, &files) != nil {
				return nil
			}
			for mode, paths := range files {
				switch mode {
				case "read":
					if json.Unmarshal(paths, &result.ReadPaths) != nil {
						return nil
					}
				case "write":
					if json.Unmarshal(paths, &result.WritePaths) != nil {
						return nil
					}
				default:
					return nil
				}
			}
		default:
			return nil
		}
	}
	return result
}
