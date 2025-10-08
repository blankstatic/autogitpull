package cmd

import (
	"github.com/blankstatic/autogitpull/internal/logic"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(registerCmd)
}

var registerCmd = &cobra.Command{
	Use: "register",
	Run: logic.GetRegisterFunc,
}
