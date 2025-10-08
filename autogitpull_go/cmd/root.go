package cmd

import (
	"fmt"
	"os"

	"github.com/blankstatic/autogitpull/internal/lib"
	"github.com/blankstatic/autogitpull/internal/logic"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use: lib.AppName,
	Run: logic.GetRootFunc,
}

var isSilently bool

func init() {
	rootCmd.PersistentFlags().BoolVarP(&isSilently, "silently", "s", false, "silently")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
