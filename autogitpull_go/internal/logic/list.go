package logic

import (
	"sync"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/lib"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/ui"
	"github.com/spf13/cobra"
)

func GetListFunc(cmd *cobra.Command, args []string) {
	isSilently := lib.GetIsSilentlyValue(cmd)

	configPath, err := lib.GetConfigPath()
	if err != nil {
		panic(err)
	}

	storage := lib.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		panic(err)
	}

	wg := &sync.WaitGroup{}

	repos := storage.GetAllRepos()

	ui.DrawListTable(wg, repos, isSilently)

	go ui.RunSpinner()

	wg.Wait()
}
