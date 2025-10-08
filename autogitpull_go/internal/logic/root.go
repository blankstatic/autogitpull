package logic

import (
	"fmt"

	"github.com/spf13/cobra"
)

func GetRootFunc(cmd *cobra.Command, args []string) {
	fmt.Println("run")
}
