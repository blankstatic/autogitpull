package logic

import (
	"fmt"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	versionpkg "github.com/blankstatic/autogitpull/autogitpull_go/internal/version"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/notifications"
	"github.com/spf13/cobra"
)

func VersionCommandHandler(cmd *cobra.Command, args []string) {
	isSilently := GetIsSilentlyValue(cmd)
	if !isSilently {
		notifications.OSNotify(config.AppName, config.AppName, versionpkg.AppVersion)
	}

	fmt.Println(config.AppName, versionpkg.AppVersion)
}
