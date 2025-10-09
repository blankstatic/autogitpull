package logic

import (
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/notifications"
	"github.com/spf13/cobra"
)

func RegisterCommandHandler(cmd *cobra.Command, args []string) {
	isSilently := GetIsSilentlyValue(cmd)

	configPath, err := config.GetConfigPath()
	if err != nil {
		panic(err)
	}

	storage := config.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		panic(err)
	}

	innerFunc := func(path string) error {
		err := git.DetectRepository(path)
		if err != nil {
			return err
		}

		err = storage.AddRepo(path)
		if err != nil {
			return err
		}

		if !isSilently {
			notifications.OSNotify(config.AppName, "Register", path)
		}

		return nil
	}

	ProcessArgsAsPaths(args, innerFunc)
}
