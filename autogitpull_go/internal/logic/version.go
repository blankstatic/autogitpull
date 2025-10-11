package logic

import (
	"fmt"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/notifications"
	"github.com/spf13/cobra"
)

const AppVersion = "v0.0.10"

func VersionCommandHandler(cmd *cobra.Command, args []string) {
	isSilently := GetIsSilentlyValue(cmd)
	if !isSilently {
		notifications.OSNotify(config.AppName, config.AppName, AppVersion)
	}

	fmt.Println(config.AppName, AppVersion)
}
