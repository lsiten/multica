package main

import (
	"os"

	"github.com/multica-ai/multica/server/internal/agentdesktop"
	"github.com/spf13/cobra"
)

var agentDesktopBridgeCmd = &cobra.Command{
	Use:    agentdesktop.BridgeCommand,
	Short:  "Serve the task-scoped agent-desktop MCP bridge",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		executable, _ := cmd.Flags().GetString("agent-desktop")
		taskID, _ := cmd.Flags().GetString("task-id")
		runtimeID, _ := cmd.Flags().GetString("runtime-id")
		headed, _ := cmd.Flags().GetBool("headed")
		return agentdesktop.RunBridge(cmd.Context(), agentdesktop.BridgeConfig{
			TaskID:     taskID,
			RuntimeID:  runtimeID,
			Executable: executable,
			Headed:     headed,
		}, os.Stdin, os.Stdout, os.Stderr)
	},
}

func init() {
	agentDesktopBridgeCmd.Flags().String("task-id", "", "Multica task ID")
	agentDesktopBridgeCmd.Flags().String("runtime-id", "", "Multica runtime ID")
	agentDesktopBridgeCmd.Flags().String("agent-desktop", "", "Pinned agent-desktop executable")
	agentDesktopBridgeCmd.Flags().Bool("headed", false, "Allow physical pointer and keyboard input")
	_ = agentDesktopBridgeCmd.MarkFlagRequired("task-id")
	_ = agentDesktopBridgeCmd.MarkFlagRequired("runtime-id")
	_ = agentDesktopBridgeCmd.MarkFlagRequired("agent-desktop")
	rootCmd.AddCommand(agentDesktopBridgeCmd)
}
