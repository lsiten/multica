package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/spf13/cobra"
)

func projectSupervisionCommand() *cobra.Command {
	group := &cobra.Command{Use: "supervision", Short: "Inspect and manage bounded project coordination"}
	for _, operation := range []string{"get", "configure", "check", "apply", "report"} {
		op := operation
		cmd := &cobra.Command{Use: op + " <project-id>", Short: map[string]string{"get": "Read classified backlog, checked version and policy", "configure": "Save enabled, revision and config from JSON", "check": "Request a check; active coordination absorbs new facts", "apply": "Apply bounded actions with the current coordination token", "report": "Report action, wait, blocked or needs_human"}[op], Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return runProjectSupervision(cmd, args, op) }}
		cmd.Flags().String("output", "json", "Output format: json")
		if op == "configure" || op == "apply" || op == "report" {
			cmd.Flags().String("body", "", "JSON request body")
			cmd.Flags().String("body-file", "", "Read JSON body from a file")
		}
		group.AddCommand(cmd)
	}
	return group
}
func init() { projectCmd.AddCommand(projectSupervisionCommand()) }
func supervisionBody(cmd *cobra.Command) (map[string]any, error) {
	body, _ := cmd.Flags().GetString("body")
	file, _ := cmd.Flags().GetString("body-file")
	if (body == "") == (file == "") {
		return nil, errors.New("provide exactly one of --body or --body-file")
	}
	raw := []byte(body)
	if file != "" {
		var err error
		raw, err = os.ReadFile(file)
		if err != nil {
			return nil, err
		}
	}
	var result map[string]any
	if len(raw) > 64<<10 {
		return nil, errors.New("supervision body exceeds 64 KiB")
	}
	if err := json.Unmarshal(raw, &result); err != nil || result == nil {
		return nil, errors.New("supervision body must be a JSON object")
	}
	return result, nil
}
func runProjectSupervision(cmd *cobra.Command, args []string, operation string) error {
	var body map[string]any
	if operation == "configure" || operation == "apply" || operation == "report" {
		var err error
		body, err = supervisionBody(cmd)
		if err != nil {
			return err
		}
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	ref, err := resolveProjectID(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}
	path := "/api/projects/" + ref.ID + "/supervision"
	var result map[string]any
	switch operation {
	case "get":
		err = client.GetJSON(ctx, path, &result)
	case "configure":
		err = client.PutJSON(ctx, path, body, &result)
	case "check":
		err = client.PostJSON(ctx, path+"/check", map[string]any{}, &result)
	case "apply":
		err = client.PostJSON(ctx, path+"/actions", body, &result)
	case "report":
		err = client.PostJSON(ctx, path+"/report", body, &result)
	default:
		return errors.New("unknown supervision operation")
	}
	if err != nil {
		return fmt.Errorf("project supervision %s: %w", operation, err)
	}
	return cli.PrintJSON(os.Stdout, result)
}
