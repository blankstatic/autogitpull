package cmd

import (
	"github.com/blankstatic/autogitpull/src/internal/logic"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(discoverCmd)
}

var discoverCmd = &cobra.Command{
	Use: "discover",
	Run: logic.DiscoverCommandHandler,
}
