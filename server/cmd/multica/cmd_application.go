package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/spf13/cobra"
)

func applicationCommand() *cobra.Command {
	group := &cobra.Command{Use: "application", Short: "Manage project applications and compositions"}
	for _, action := range []string{"list", "get", "create", "update", "delete", "plan"} {
		action := action
		use, args := action+" <application-id>", exactArgs(1)
		if action == "list" || action == "create" {
			use, args = action, exactArgs(0)
		}
		cmd := &cobra.Command{Use: use, Short: map[string]string{
			"list":   "List applications, optionally filtered by project",
			"get":    "Read an application and its current revision",
			"create": "Create a service or composition from JSON",
			"update": "Update configuration or relationships with revision matching",
			"delete": "Delete a stopped and unreferenced application",
			"plan":   "Preview services, dependencies and startup waves",
		}[action], Args: args, RunE: func(cmd *cobra.Command, args []string) error { return runApplicationCatalog(cmd, args, action) }}
		cmd.Flags().String("output", "json", "Output format: json")
		if action == "create" || action == "update" {
			cmd.Flags().String("body", "", "JSON request body")
			cmd.Flags().String("body-file", "", "Read JSON from a file, or - for stdin")
		}
		if action == "list" {
			cmd.Flags().String("project", "", "Filter by project UUID or name")
		}
		if action == "delete" {
			cmd.Flags().Int64("revision", 0, "Current application revision (required)")
		}
		group.AddCommand(cmd)
	}
	group.AddCommand(applicationControlCommands()...)
	return group
}

func init() { rootCmd.AddCommand(applicationCommand()) }

func applicationBody(cmd *cobra.Command) (map[string]any, error) {
	body, err := cmd.Flags().GetString("body")
	if err != nil {
		return nil, err
	}
	file, err := cmd.Flags().GetString("body-file")
	if err != nil {
		return nil, err
	}
	if (body == "") == (file == "") {
		return nil, errors.New("provide exactly one of --body or --body-file")
	}
	raw := []byte(body)
	if file != "" {
		reader := cmd.InOrStdin()
		if file != "-" {
			stream, openErr := os.Open(file)
			if openErr != nil {
				return nil, fmt.Errorf("open application body: %w", openErr)
			}
			defer stream.Close()
			reader = stream
		}
		raw, err = io.ReadAll(io.LimitReader(reader, (64<<10)+1))
		if err != nil {
			return nil, fmt.Errorf("read application body: %w", err)
		}
	}
	if len(raw) > 64<<10 {
		return nil, errors.New("application body exceeds 64 KiB")
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil || result == nil {
		return nil, errors.New("application body must be a JSON object")
	}
	return result, nil
}

func runApplicationCatalog(cmd *cobra.Command, args []string, action string) error {
	format, err := cmd.Flags().GetString("output")
	if err != nil {
		return err
	}
	if format != "json" {
		return errors.New("application output must be json")
	}
	var body map[string]any
	if action == "create" || action == "update" {
		body, err = applicationBody(cmd)
		if err != nil {
			return err
		}
	}
	path := "/api/applications"
	if len(args) > 0 {
		id, parseErr := util.ParseUUID(args[0])
		if parseErr != nil {
			return errors.New("application-id must be a UUID")
		}
		path += "/" + util.UUIDToString(id)
	}
	if action == "delete" {
		revision, flagErr := cmd.Flags().GetInt64("revision")
		if flagErr != nil {
			return flagErr
		}
		if revision < 1 {
			return errors.New("--revision is required")
		}
		path += "?revision=" + strconv.FormatInt(revision, 10)
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(cmd.Context())
	defer cancel()
	if action == "list" {
		project, flagErr := cmd.Flags().GetString("project")
		if flagErr != nil {
			return flagErr
		}
		if project != "" {
			ref, resolveErr := resolveProjectID(ctx, client, project)
			if resolveErr != nil {
				return resolveErr
			}
			path += "?project_id=" + url.QueryEscape(ref.ID)
		}
	}
	var result map[string]any
	switch action {
	case "list", "get":
		err = client.GetJSON(ctx, path, &result)
	case "plan":
		err = client.GetJSON(ctx, path+"/plan", &result)
	case "create":
		err = client.PostJSON(ctx, path, body, &result)
	case "update":
		err = client.PatchJSON(ctx, path, body, &result)
	case "delete":
		err = client.DeleteJSON(ctx, path)
		result = map[string]any{"deleted": true}
	default:
		return errors.New("unknown application catalog action")
	}
	if err != nil {
		return fmt.Errorf("application %s: %w", action, err)
	}
	return cli.PrintJSON(cmd.OutOrStdout(), result)
}
