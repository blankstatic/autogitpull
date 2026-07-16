package cmd

import (
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/logic"
	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service [install|start|stop|uninstall|status]",
	Short: "Manage the auto-pull background service",
	Long:  `Install, start, stop, uninstall or check status of the background service on macOS launchd or Linux systemd.`,
	Args:  cobra.ExactArgs(1),
	Run:   logic.ServiceCommandHandler,
}

func init() {
	rootCmd.AddCommand(serviceCmd)
}
