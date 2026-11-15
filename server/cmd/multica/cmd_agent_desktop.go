package main

import (
	"os"

	"github.com/multica-ai/multica/server/internal/agentdesktop"
	"github.com/spf13/cobra"
)

func init() {
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the pinned agent-desktop release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			baseURL, _ := cmd.Flags().GetString("release-base-url")
			_, err := agentdesktop.Install(cmd.Context(), agentdesktop.InstallOptions{
				ReleaseBaseURL: baseURL,
			}, os.Stdout)
			return err
		},
	}
	install.Flags().String("release-base-url", "", "Override the managed release asset base URL")
	_ = install.Flags().MarkHidden("release-base-url")

	command := &cobra.Command{
		Use:    "agent-desktop",
		Short:  "Manage the pinned desktop automation tool",
		Hidden: true,
	}
	command.AddCommand(install)
	rootCmd.AddCommand(command)
}
