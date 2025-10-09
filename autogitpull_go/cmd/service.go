package cmd

import (
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/logic"
	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service [install|start|stop|uninstall|status]",
	Short: "Manage the auto-pull macOS launchd service",
	Long:  `Install, start, stop, uninstall or check status of the background launchd service on macOS`,
	Args:  cobra.ExactArgs(1),
	Run:   logic.ServiceCommandHandler,
}

func init() {
	rootCmd.AddCommand(serviceCmd)
}
