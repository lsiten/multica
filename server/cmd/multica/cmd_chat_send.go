package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// Send a message to a chat session. In a group chat the server acts on the
// @-mentions in the content (an @all mention notifies every human participant,
// an @<member> mention notifies that member); a 1:1 chat ignores mentions
// because there is no one else to address. The session's agent is woken on
// every send regardless of mentions.
var chatSendCmd = &cobra.Command{
	Use:   "send <session-id>",
	Short: "Send a message to a chat session (a group chat honors @-mentions)",
	Long: `Send a message to a chat session and print the resulting turn.

The content may carry @-mentions. In a group chat the server acts on them:
` + "`@all`" + ` notifies every human participant, ` + "`@<member>`" + ` notifies that
member, and the session's agent is always woken. A 1:1 chat ignores
@-mentions (there is no one else to address).

Provide the content inline with --content, from a file with --content-file
(long / multi-line messages with mention markdown), or on stdin with
--content-stdin.

A mention is written as [Label](mention://type/id): for example
[@all](mention://all/all) or [@Bob](mention://member/<member-uuid>).

This is a write: it starts an agent run (real budget) and may notify members.
Never retry a send that already returned a message.`,
	Args: cobra.ExactArgs(1),
	RunE: runChatSend,
}

func init() {
	chatSendCmd.Flags().String("content", "", "Message content (inline)")
	chatSendCmd.Flags().String("content-file", "", "Read message content from a UTF-8 file (preserves multi-line content verbatim)")
	chatSendCmd.Flags().Bool("content-stdin", false, "Read message content from stdin")
	chatSendCmd.Flags().Bool("allow-external-file", false, "Allow --content-file to read a path outside the current working directory")
	chatSendCmd.Flags().String("output", "json", "Output format: json")
	chatCmd.AddCommand(chatSendCmd)
}

func runChatSend(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	content, ok, err := resolveTextFlag(cmd, "content")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("provide message content via --content, --content-file, or --content-stdin")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var resp map[string]any
	if err := client.PostJSON(ctx, "/api/chat/sessions/"+sessionID+"/messages", map[string]any{"content": content}, &resp); err != nil {
		return fmt.Errorf("send chat message: %w", err)
	}
	if output, _ := cmd.Flags().GetString("output"); output != "json" {
		if msgID, _ := resp["message_id"].(string); msgID != "" {
			fmt.Fprintf(os.Stdout, "sent message %s\n", msgID)
		}
		return nil
	}
	return cli.PrintJSON(os.Stdout, resp)
}
