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

func init() { rootCmd.AddCommand(newHumanRequestCommand()) }

func newHumanRequestCommand() *cobra.Command {
	command := &cobra.Command{Use: "human-request", Short: "Deliver actionable requests and read their exact member responses"}
	create := &cobra.Command{Use: "create", Args: cobra.NoArgs, Short: "Create or revise this run's request using a stable key", Long: `Deliver a versioned request to the run's designated member. Required fields: key, kind (confirmation|choice|input|manual), title, action_label, next. Manual requests also need steps and verification; choices need two to four {id,label,recommended} entries; input needs input_label. Optional: impact, details, expires_in_seconds, response_mode. Ordinary choice/input requests may set response_mode=chat_or_card; authorization/manual requests require card_only. Never put credentials or executable button actions in this document. Reusing a key with unchanged content is idempotent; changing it invalidates old confirmations. Example:
{"key":"screen-recording","kind":"manual","title":"Enable screen recording for Multica","steps":["Open System Settings > Privacy & Security > Screen Recording and enable Multica"],"action_label":"Check permission","verification":"Read screen recording permission from the runtime","next":"I will check the permission before continuing"}`, RunE: func(cmd *cobra.Command, args []string) error {
		path, _ := cmd.Flags().GetString("body-file")
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read request: %w", err)
		}
		var input service.HumanRequestInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return err
		}
		if err := input.Validate(); err != nil {
			return err
		}
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		var response json.RawMessage
		if err := client.PostJSON(cmd.Context(), "/api/human-requests/", input, &response); err != nil {
			return err
		}
		return cli.PrintJSON(os.Stdout, response)
	}}
	create.Flags().String("body-file", "", "UTF-8 JSON request file")
	create.MarkFlagRequired("body-file")
	command.AddCommand(create)
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "Read requests visible to the authenticated member or requesting agent", RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		query := url.Values{}
		for _, flag := range []string{"issue-id", "chat-session-id", "project-id"} {
			value, _ := cmd.Flags().GetString(flag)
			if value != "" {
				key := map[string]string{"issue-id": "issue_id", "chat-session-id": "chat_session_id", "project-id": "project_id"}[flag]
				query.Set(key, value)
			}
		}
		var response json.RawMessage
		if err := client.GetJSON(cmd.Context(), "/api/human-requests/?"+query.Encode(), &response); err != nil {
			return err
		}
		return cli.PrintJSON(os.Stdout, response)
	}}
	list.Flags().String("output", "json", "Output format (json)")
	for _, flag := range []string{"issue-id", "chat-session-id", "project-id"} {
		list.Flags().String(flag, "", "Limit to this resource UUID")
	}
	command.AddCommand(list)
	return command
}
