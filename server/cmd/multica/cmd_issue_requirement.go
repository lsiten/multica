package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// Requirement dimension: a kind=requirement issue carries a 1:1 requirement row
// (its description is the requirement document) with a confirm/change lifecycle.
// This command reads that lifecycle. The confirm/change flow itself is driven by
// human-request cards (see "multica human-request create" for the
// requirement-confirm / requirement-change-review keys); the human only answers,
// the agent initiates.

var issueRequirementCmd = &cobra.Command{
	Use:   "requirement <issue-id>",
	Short: "Show the requirement lifecycle of a requirement issue",
	Long: `Show the requirement lifecycle of a kind=requirement issue.

The response reports lifecycle_status (pending_confirm | confirmed), the
confirmed document revision, and whether there is an unconfirmed change (the
document was edited after the last confirmation). A kind=task issue has no
requirement row and returns 404.

The confirm/change lifecycle is driven by human-request cards, not this
command: the agent delivers a "requirement-confirm" card (kind=confirmation) to
confirm, and a "requirement-change-review" card (kind=choice) to decide an
unconfirmed change. Use "multica human-request create" for those; see the
requirements reference for the exact card shapes.`,
	Args: cobra.ExactArgs(1),
	RunE: runIssueRequirement,
}

func init() {
	issueRequirementCmd.Flags().String("output", "json", "Output format: table or json")
	issueCmd.AddCommand(issueRequirementCmd)
}

func runIssueRequirement(cmd *cobra.Command, args []string) error {
	issueID := args[0]
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var resp map[string]any
	if err := client.GetJSON(ctx, "/api/issues/"+issueID+"/requirement", &resp); err != nil {
		return fmt.Errorf("read requirement: %w", err)
	}
	if output, _ := cmd.Flags().GetString("output"); output != "table" {
		return cli.PrintJSON(os.Stdout, resp)
	}
	fmt.Fprintf(os.Stdout, "issue_id:          %s\n", strVal(resp, "issue_id"))
	fmt.Fprintf(os.Stdout, "lifecycle_status: %s\n", strVal(resp, "lifecycle_status"))
	if rev, ok := resp["confirmed_revision"]; ok && rev != nil {
		fmt.Fprintf(os.Stdout, "confirmed_revision: %v\n", rev)
	}
	if cb := strVal(resp, "confirmed_by"); cb != "" {
		fmt.Fprintf(os.Stdout, "confirmed_by:      %s\n", cb)
	}
	if when := strVal(resp, "confirmed_at"); when != "" {
		fmt.Fprintf(os.Stdout, "confirmed_at:      %s\n", when)
	}
	if unconfirmed, ok := resp["unconfirmed_change"]; ok {
		fmt.Fprintf(os.Stdout, "unconfirmed_change: %v\n", unconfirmed)
	}
	return nil
}
