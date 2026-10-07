package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/spf13/cobra"
)

func applicationControlCommands() []*cobra.Command {
	commands := []*cobra.Command{}
	for _, action := range []string{"start", "stop", "restart", "publish", "unpublish", "status", "operations", "operation", "cancel", "logs", "launch", "service-access"} {
		use, args := action+" <application-id>", exactArgs(1)
		if action == "status" {
			use, args = action, exactArgs(0)
		}
		if action == "operation" || action == "cancel" {
			use, args = action+" <application-id> <operation-id>", exactArgs(2)
		}
		command := &cobra.Command{Use: use, Short: map[string]string{
			"start":          "Start an application with explicit revision, placement and idempotency key",
			"stop":           "Stop an application while protecting shared services",
			"restart":        "Apply the current configuration by restarting an application",
			"publish":        "Publish a selected running instance",
			"unpublish":      "Revoke application access without stopping its process",
			"status":         "Read workspace application, instance and endpoint status",
			"operations":     "List recent application operations",
			"operation":      "Read one operation and its execution steps",
			"cancel":         "Cancel unfinished work and reconcile the affected service processes",
			"logs":           "Read a bounded log page with a resumable cursor",
			"launch":         "Create a single-use browser launch URL for an endpoint",
			"service-access": "Issue an application-only service credential for an endpoint",
		}[action], Args: args, RunE: func(cmd *cobra.Command, args []string) error { return runApplicationControl(cmd, args, action) }}
		command.Flags().String("output", "json", "Output format: json")
		if applicationControlWritesOperation(action) {
			command.Flags().String("body", "", "JSON with revision, runtime_id, idempotency_key and optional placements/force")
			command.Flags().String("body-file", "", "Read JSON from a file, or - for stdin")
		}
		if applicationControlWritesOperation(action) || action == "operation" || action == "cancel" {
			command.Flags().Bool("wait", false, "Wait for a terminal operation result")
			command.Flags().Duration("timeout", 2*time.Minute, "Maximum wait for an operation")
		}
		if action == "logs" {
			command.Flags().String("instance", "", "Selected instance UUID (required)")
			command.Flags().String("cursor", "", "Continue from a previous log cursor")
			command.Flags().Int("limit", 65536, "Maximum bytes to read (1–65536)")
		}
		if action == "launch" || action == "service-access" {
			command.Flags().String("endpoint", "", "Selected endpoint UUID (required)")
		}
		commands = append(commands, command)
	}
	return commands
}

func applicationControlWritesOperation(action string) bool {
	switch action {
	case "start", "stop", "restart", "publish", "unpublish":
		return true
	default:
		return false
	}
}

func applicationControlUUID(raw, field string) (string, error) {
	id, err := util.ParseUUID(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be a UUID", field)
	}
	return util.UUIDToString(id), nil
}

func runApplicationControl(cmd *cobra.Command, args []string, action string) error {
	format, err := cmd.Flags().GetString("output")
	if err != nil {
		return err
	}
	if format != "json" {
		return errors.New("application output must be json")
	}
	path := "/api/applications"
	for _, raw := range args {
		id, err := applicationControlUUID(raw, "application/operation id")
		if err != nil {
			return err
		}
		if path == "/api/applications" {
			path += "/" + id
		} else {
			path += "/operations/" + id
		}
	}
	var body map[string]any
	if applicationControlWritesOperation(action) {
		body, err = applicationBody(cmd)
		if err != nil {
			return err
		}
		if value, exists := body["action"]; exists && value != action {
			return errors.New("body action must match the command")
		}
		body["action"] = action
		path += "/operations"
	}
	switch action {
	case "cancel":
		path += "/cancel"
	case "status":
		path += "/board"
	case "operations":
		path += "/operations"
	case "logs":
		instance, err := cmd.Flags().GetString("instance")
		if err != nil {
			return err
		}
		id, err := applicationControlUUID(instance, "--instance")
		if err != nil {
			return err
		}
		cursor, err := cmd.Flags().GetString("cursor")
		if err != nil {
			return err
		}
		limit, err := cmd.Flags().GetInt("limit")
		if err != nil {
			return err
		}
		if limit < 1 || limit > 65536 || len(cursor) > 512 {
			return errors.New("log limit must be 1–65536 and cursor at most 512 bytes")
		}
		path += "/instances/" + id + "/logs?" + url.Values{"cursor": {cursor}, "limit": {strconv.Itoa(limit)}}.Encode()
	case "launch", "service-access":
		endpoint, err := cmd.Flags().GetString("endpoint")
		if err != nil {
			return err
		}
		id, err := applicationControlUUID(endpoint, "--endpoint")
		if err != nil {
			return err
		}
		path += "/endpoints/" + id + "/" + action
	}
	wait := false
	if cmd.Flags().Lookup("wait") != nil {
		wait, err = cmd.Flags().GetBool("wait")
		if err != nil {
			return err
		}
	}
	ctx, cancel := cli.APIContext(cmd.Context())
	defer cancel()
	if wait {
		timeout, err := cmd.Flags().GetDuration("timeout")
		if err != nil {
			return err
		}
		if timeout <= 0 || timeout > 30*time.Minute {
			return errors.New("--timeout must be positive and at most 30m")
		}
		waitCtx, waitCancel := context.WithTimeout(cmd.Context(), timeout)
		defer waitCancel()
		ctx = waitCtx
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	var result map[string]any
	if applicationControlWritesOperation(action) || action == "launch" || action == "service-access" || action == "cancel" {
		err = client.PostJSON(ctx, path, body, &result)
	} else {
		err = client.GetJSON(ctx, path, &result)
	}
	if err != nil {
		return fmt.Errorf("application %s: %w", action, err)
	}
	if wait {
		operationID, ok := result["id"].(string)
		if !ok {
			return errors.New("operation response is missing its id")
		}
		operationID, err = applicationControlUUID(operationID, "operation response id")
		if err != nil {
			return err
		}
		appID, err := applicationControlUUID(args[0], "application id")
		if err != nil {
			return err
		}
		operationPath := "/api/applications/" + appID + "/operations/" + operationID
		for {
			state, _ := result["state"].(string)
			switch state {
			case "completed", "partial", "failed", "cancelled":
				if err := cli.PrintJSON(cmd.OutOrStdout(), result); err != nil {
					return err
				}
				if state != "completed" && !(action == "cancel" && state == "cancelled") {
					return fmt.Errorf("application operation %s ended as %s", operationID, state)
				}
				return nil
			case "queued", "running", "cancelling":
			default:
				return errors.New("operation response has an unknown state")
			}
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("waiting for operation %s: %w", operationID, ctx.Err())
			case <-timer.C:
			}
			if err := client.GetJSON(ctx, operationPath, &result); err != nil {
				return fmt.Errorf("read operation %s: %w", operationID, err)
			}
		}
	}
	return cli.PrintJSON(cmd.OutOrStdout(), result)
}
