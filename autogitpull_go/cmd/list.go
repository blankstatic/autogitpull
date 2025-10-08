package cmd

import (
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/logic"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use: "list",
	Run: logic.GetListFunc,
}
