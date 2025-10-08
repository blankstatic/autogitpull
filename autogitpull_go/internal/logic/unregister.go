package logic

import (
	"github.com/blankstatic/autogitpull/internal/lib"
	"github.com/spf13/cobra"
)

func GetUnregisterFunc(cmd *cobra.Command, args []string) {
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
		err = storage.RemoveRepo(path)
		if err != nil {
			return err
		}

		if !isSilently {
			lib.ShowMessage(lib.AppName, "Unregister", path)
		}

		return nil
	}

	lib.ProcessArgsAsPaths(args, innerFunc)
}
