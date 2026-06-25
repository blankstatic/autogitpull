package logic

import (
	"fmt"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	versionpkg "github.com/blankstatic/autogitpull/autogitpull_go/internal/version"
	"github.com/spf13/cobra"
)

func VersionCommandHandler(cmd *cobra.Command, args []string) {
	fmt.Println(config.AppName, versionpkg.AppVersion)
}
