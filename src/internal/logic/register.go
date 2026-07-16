package logic

import (
	"github.com/blankstatic/autogitpull/src/internal/config"
	"github.com/blankstatic/autogitpull/src/pkg/git"
	"github.com/spf13/cobra"
)

func RegisterCommandHandler(cmd *cobra.Command, args []string) {
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

		return nil
	}

	ProcessArgsAsPaths(args, innerFunc)
}
