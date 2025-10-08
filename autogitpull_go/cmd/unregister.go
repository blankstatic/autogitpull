package cmd

import (
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/logic"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(unregisterCmd)
}

var unregisterCmd = &cobra.Command{
	Use: "unregister",
	Run: logic.GetUnregisterFunc,
}
