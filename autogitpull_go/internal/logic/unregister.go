package logic

import (
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/notifications"
	"github.com/spf13/cobra"
)

func UnregisterCommandHandler(cmd *cobra.Command, args []string) {
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
		err = storage.RemoveRepo(path)
		if err != nil {
			return err
		}

		if !isSilently {
			notifications.OSNotify(config.AppName, "Unregister", path)
		}

		return nil
	}

	ProcessArgsAsPaths(args, innerFunc)
}
