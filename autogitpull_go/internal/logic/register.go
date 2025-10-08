package logic

import (
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/lib"
	"github.com/spf13/cobra"
)

func GetRegisterFunc(cmd *cobra.Command, args []string) {
	isSilently := lib.GetIsSilentlyValue(cmd)

	configPath, err := lib.GetConfigPath()
	if err != nil {
		panic(err)
	}

	storage := lib.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		panic(err)
	}

	innerFunc := func(path string) error {
		err := lib.DetectRepository(path)
		if err != nil {
			return err
		}

		err = storage.AddRepo(path)
		if err != nil {
			return err
		}

		if !isSilently {
			lib.ShowMessage(lib.AppName, "Register", path)
		}

		return nil
	}

	lib.ProcessArgsAsPaths(args, innerFunc)
}
