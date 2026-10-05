package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/spf13/cobra"
)

func init() {
	command := &cobra.Command{Use: "next-step <issue>", Args: cobra.ExactArgs(1), Short: "Report this run's exact remaining work, actor and evidence", RunE: func(cmd *cobra.Command, args []string) error {
		path, _ := cmd.Flags().GetString("body-file")
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read next step: %w", err)
		}
		var step service.IssueNextStep
		if err = json.Unmarshal(raw, &step); err != nil {
			return err
		}
		if err = step.Validate(); err != nil {
			return err
		}
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		var response json.RawMessage
		if err = client.PutJSON(cmd.Context(), "/api/issues/"+url.PathEscape(args[0])+"/next-step", step, &response); err != nil {
			return err
		}
		return cli.PrintJSON(os.Stdout, response)
	}}
	command.Flags().String("body-file", "", "UTF-8 JSON handoff with kind, summary, actor_type, actor_id, missing, evidence, issue_revision and optional request_id")
	command.MarkFlagRequired("body-file")
	issueCmd.AddCommand(command)
}
