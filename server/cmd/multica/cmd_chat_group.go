package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// Group chat commands. A group chat is a kind=group chat session that hosts
// several human participants plus one session agent. A project's default group
// is created automatically; these commands create a STANDALONE group (not a
// project default, so it can be archived/deleted like an ordinary chat) and
// manage its members. project_id is optional, so a group need not be bound to a
// project.

var chatGroupCmd = &cobra.Command{
	Use:   "group",
	Short: "Create a group chat and manage its members",
}

var chatGroupCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a group chat (optionally without a project)",
	Long: `Create a group chat and print the resulting session.

The group is a standalone group (is_project_default=false), so it can be
archived or deleted like an ordinary chat — unlike a project's default group,
which only archives when the project reaches a terminal status. The project is
optional: with no --project the group is not bound to any project.

The calling user is always added as a member participant; add more members
with "multica chat group members add".`,
	Args: cobra.NoArgs,
	RunE: runChatGroupCreate,
}

var chatGroupMembersCmd = &cobra.Command{
	Use:   "members",
	Short: "List or change a group chat's participants",
}

var chatGroupMembersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List a group chat's participants",
	Args:  cobra.ExactArgs(1),
	RunE:  runChatGroupMembersList,
}

var chatGroupMembersAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a participant to a group chat",
	Args:  cobra.ExactArgs(1),
	RunE:  runChatGroupMembersAdd,
}

var chatGroupMembersRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a participant from a group chat",
	Args:  cobra.ExactArgs(3),
	RunE:  runChatGroupMembersRemove,
}

func init() {
	chatGroupCreateCmd.Flags().String("agent", "", "Session agent (name or id); required")
	chatGroupCreateCmd.Flags().String("title", "", "Group title")
	chatGroupCreateCmd.Flags().String("project", "", "Optional project (name or id) to bind the group to")
	chatGroupCreateCmd.Flags().String("output", "json", "Output format: json (table is not supported for create)")

	chatGroupMembersListCmd.Flags().String("output", "json", "Output format: table or json")
	chatGroupMembersAddCmd.Flags().String("member-type", "member", "Participant type: member or agent")
	chatGroupMembersAddCmd.Flags().String("member", "", "Participant (name or id)")
	chatGroupMembersAddCmd.Flags().String("output", "json", "Output format: json")

	chatGroupMembersCmd.AddCommand(chatGroupMembersListCmd)
	chatGroupMembersCmd.AddCommand(chatGroupMembersAddCmd)
	chatGroupMembersCmd.AddCommand(chatGroupMembersRemoveCmd)
	chatGroupCmd.AddCommand(chatGroupCreateCmd)
	chatGroupCmd.AddCommand(chatGroupMembersCmd)
	chatCmd.AddCommand(chatGroupCmd)
}

func runChatGroupCreate(cmd *cobra.Command, _ []string) error {
	agentNameOrID, _ := cmd.Flags().GetString("agent")
	if agentNameOrID == "" {
		return fmt.Errorf("--agent is required")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	agentID, err := resolveAgent(ctx, client, agentNameOrID)
	if err != nil {
		return fmt.Errorf("resolve agent: %w", err)
	}

	body := map[string]any{"agent_id": agentID}
	if title, _ := cmd.Flags().GetString("title"); title != "" {
		body["title"] = title
	}
	if projectRef, _ := cmd.Flags().GetString("project"); projectRef != "" {
		project, err := resolveProjectID(ctx, client, projectRef)
		if err != nil {
			return fmt.Errorf("resolve project: %w", err)
		}
		body["project_id"] = project.ID
	}

	var session map[string]any
	if err := client.PostJSON(ctx, "/api/chat/sessions/groups", body, &session); err != nil {
		return fmt.Errorf("create group chat: %w", err)
	}
	return cli.PrintJSON(os.Stdout, session)
}

func runChatGroupMembersList(cmd *cobra.Command, args []string) error {
	sessionID := args[0]
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var resp map[string]any
	if err := client.GetJSON(ctx, "/api/chat/sessions/"+sessionID+"/participants", &resp); err != nil {
		return fmt.Errorf("list group participants: %w", err)
	}
	if output, _ := cmd.Flags().GetString("output"); output != "table" {
		return cli.PrintJSON(os.Stdout, resp)
	}
	rows, _ := resp["participants"].([]any)
	headers := []string{"TYPE", "MEMBER_ID", "JOINED_AT"}
	table := make([][]string, 0, len(rows))
	for _, pi := range rows {
		p, ok := pi.(map[string]any)
		if !ok {
			continue
		}
		table = append(table, []string{strVal(p, "member_type"), strVal(p, "member_id"), strVal(p, "joined_at")})
	}
	cli.PrintTable(os.Stdout, headers, table)
	return nil
}

func runChatGroupMembersAdd(cmd *cobra.Command, args []string) error {
	sessionID := args[0]
	memberType, _ := cmd.Flags().GetString("member-type")
	if memberType != "member" && memberType != "agent" {
		return fmt.Errorf("--member-type must be member or agent")
	}
	memberRef, _ := cmd.Flags().GetString("member")
	if memberRef == "" {
		return fmt.Errorf("--member is required")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	memberID, err := resolveGroupMember(ctx, client, memberType, memberRef)
	if err != nil {
		return fmt.Errorf("resolve %s %q: %w", memberType, memberRef, err)
	}
	body := map[string]any{"member_type": memberType, "member_id": memberID}
	var member map[string]any
	if err := client.PostJSON(ctx, "/api/chat/sessions/"+sessionID+"/participants", body, &member); err != nil {
		return fmt.Errorf("add group participant: %w", err)
	}
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, member)
	}
	fmt.Fprintf(os.Stdout, "added %s %s\n", memberType, memberID)
	return nil
}

func runChatGroupMembersRemove(cmd *cobra.Command, args []string) error {
	sessionID := args[0]
	memberType := args[1]
	memberID := args[2]
	if memberType != "member" && memberType != "agent" {
		return fmt.Errorf("member type must be member or agent")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	if err := client.DeleteJSON(ctx, "/api/chat/sessions/"+sessionID+"/participants/"+memberType+"/"+memberID); err != nil {
		return fmt.Errorf("remove group participant: %w", err)
	}
	fmt.Fprintf(os.Stdout, "removed %s %s\n", memberType, memberID)
	return nil
}

// resolveGroupMember resolves a participant reference to a UUID. Agents resolve
// by name or id via resolveAgent; members are matched by their member id (a
// UUID) — the member directory is not loaded here to keep the command cheap.
func resolveGroupMember(ctx context.Context, client *cli.APIClient, memberType, ref string) (string, error) {
	if memberType == "agent" {
		return resolveAgent(ctx, client, ref)
	}
	if !uuidRegexp.MatchString(ref) {
		return "", fmt.Errorf("member must be a UUID, got %q", ref)
	}
	return ref, nil
}
