package logic

import (
	"fmt"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/lib"
	"github.com/spf13/cobra"
)

func GetVersionFunc(cmd *cobra.Command, args []string) {
	isSilently := lib.GetIsSilentlyValue(cmd)
	if !isSilently {
		lib.ShowMessage(lib.AppName, lib.AppName, lib.AppVersion)
	}

	fmt.Println(lib.AppName, lib.AppVersion)
}
