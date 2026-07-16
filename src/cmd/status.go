package cmd

import (
	"github.com/blankstatic/autogitpull/src/internal/logic"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use: "status",
	Run: logic.StatusCommandHandler,
}
