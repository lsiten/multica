package main

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/spf13/cobra"
)

var agentCollaborationCmd = &cobra.Command{Use: "collaborate", Short: "Communicate with agents through their runs"}

func init() {
	agentCmd.AddCommand(agentCollaborationCmd)
	agentCollaborationCmd.AddCommand(newAgentReadCommand())
	agentCollaborationCmd.AddCommand(newAgentDelegateCommand(), newAgentAppendCommand(), newAgentNextCommand(), newAgentWatchCommand(), newAgentStopCommand())
}

func collaborationCommand(name, use, pathTemplate string, args int, needsContent bool) *cobra.Command {
	cmd := &cobra.Command{Use: use, Short: "Send an Agent collaboration command", Args: exactArgs(args), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := cli.APIContext(cmd.Context())
		defer cancel()
		requestID, err := cmd.Flags().GetString("request-id")
		if err != nil {
			return err
		}
		if strings.TrimSpace(requestID) == "" {
			requestID = uuid.NewString()
		}
		body := map[string]any{"client_request_id": requestID}
		if needsContent {
			content, e := cmd.Flags().GetString("content")
			if e != nil {
				return e
			}
			if strings.TrimSpace(content) == "" {
				return fmt.Errorf("--content is required")
			}
			body["content"] = content
		}
		path := pathTemplate
		if name == "delegate" {
			issue, e := resolveIssueRef(ctx, client, args[0])
			if e != nil {
				return e
			}
			agentID, e := resolveAgent(ctx, client, args[1])
			if e != nil {
				return e
			}
			path = "/api/issues/" + url.PathEscape(issue.ID) + "/collaboration/delegate"
			body["target_agent_id"] = agentID
		} else {
			run, e := resolveTaskRunID(ctx, client, "", args[0])
			if e != nil {
				return e
			}
			path = "/api/tasks/" + url.PathEscape(run.ID) + "/collaboration/" + name
		}
		var result protocol.AgentCollaborationOperation
		if err := client.PostJSON(ctx, path, body, &result); err != nil {
			return err
		}
		if result.Operation == "" || result.Status == "" {
			return fmt.Errorf("invalid collaboration operation response")
		}
		return cli.PrintJSON(cmd.OutOrStdout(), result)
	}}
	cmd.Flags().String("request-id", "", "Stable idempotency key reused when retrying")
	if needsContent {
		cmd.Flags().String("content", "", "Instruction or message to send")
	}
	return cmd
}

func newAgentDelegateCommand() *cobra.Command {
	return collaborationCommand("delegate", "delegate <issue> <agent>", "", 2, true)
}
func newAgentAppendCommand() *cobra.Command {
	return collaborationCommand("append", "append <run-id>", "", 1, true)
}
func newAgentNextCommand() *cobra.Command {
	return collaborationCommand("next", "next <run-id>", "", 1, true)
}
func newAgentWatchCommand() *cobra.Command {
	return collaborationCommand("watch", "watch <run-id>", "", 1, false)
}
func newAgentStopCommand() *cobra.Command {
	return collaborationCommand("stop", "stop <run-id>", "", 1, false)
}

func newAgentReadCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "read <run-id>", Short: "Read new answers without tool traces", Args: exactArgs(1), RunE: runAgentRead}
	cmd.Flags().String("cursor", "", "Continuation cursor returned by the previous answer page")
	cmd.Flags().Int("limit", 20, "Maximum answer chunks (1-50); long answers continue at a character offset")
	cmd.Flags().String("view", "", "Use final to read the authoritative terminal result before transcript delivery")
	cmd.Flags().String("issue", "", "Issue ID or key for resolving a short run ID")
	return cmd
}

func runAgentRead(cmd *cobra.Command, args []string) error {
	limit, err := cmd.Flags().GetInt("limit")
	if err != nil || limit < 1 || limit > 50 {
		return fmt.Errorf("limit must be between 1 and 50")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(cmd.Context())
	defer cancel()
	issueID, err := cmd.Flags().GetString("issue")
	if err != nil {
		return err
	}
	if issueID != "" {
		issue, err := resolveIssueRef(ctx, client, issueID)
		if err != nil {
			return err
		}
		issueID = issue.ID
	}
	run, err := resolveTaskRunID(ctx, client, issueID, args[0])
	if err != nil {
		return err
	}
	cursor, err := cmd.Flags().GetString("cursor")
	if err != nil {
		return err
	}
	query := url.Values{"limit": {fmt.Sprint(limit)}}
	view, err := cmd.Flags().GetString("view")
	if err != nil || (view != "" && view != "final") {
		return fmt.Errorf("view must be empty or final")
	}
	if view != "" {
		query.Set("view", view)
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var page protocol.AgentAnswerPage
	if err := client.GetJSON(ctx, "/api/tasks/"+url.PathEscape(run.ID)+"/answers?"+query.Encode(), &page); err != nil {
		return err
	}
	if page.TaskID != run.ID || page.Messages == nil || page.NextCursor == "" {
		return fmt.Errorf("invalid agent answer response; keep the previous cursor")
	}
	return cli.PrintJSON(cmd.OutOrStdout(), page)
}
