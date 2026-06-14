package logic

import (
	"sync"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/ui"
	"github.com/spf13/cobra"
)

func StatusCommandHandler(cmd *cobra.Command, args []string) {
	isSilently := GetIsSilentlyValue(cmd)

	configPath, err := config.GetConfigPath()
	if err != nil {
		panic(err)
	}

	storage := config.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		panic(err)
	}

	wg := &sync.WaitGroup{}

	repos := storage.GetAllRepos()

	ui.DrawListTable(wg, repos, isSilently)

	wg.Wait()
}
