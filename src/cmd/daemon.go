package cmd

import (
	"github.com/blankstatic/autogitpull/src/internal/logic"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(daemonCmd)
}

var daemonCmd = &cobra.Command{
	Use: "daemon",
	Run: logic.DaemonCommandHandler,
}
