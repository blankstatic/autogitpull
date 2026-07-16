package logic

import (
	"fmt"
	"time"

	"github.com/blankstatic/autogitpull/src/internal/config"
	"github.com/blankstatic/autogitpull/src/pkg/fs"
	"github.com/blankstatic/autogitpull/src/pkg/git"
	"github.com/blankstatic/autogitpull/src/pkg/tui/spinner"
	"github.com/spf13/cobra"
)

func DiscoverCommandHandler(cmd *cobra.Command, args []string) {
	isSilently := GetIsSilentlyValue(cmd)

	configPath, err := config.GetConfigPath()
	if err != nil {
		panic(err)
	}

	storage := config.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		panic(err)
	}

	var countRepos uint

	var updateChan chan string
	var spinnerDone chan struct{}

	if !isSilently {
		updateChan = make(chan string)
		spinnerDone = make(chan struct{})
		go func() {
			defer close(spinnerDone)
			spinner.RunWithUpdates(updateChan)
		}()
	}

	updateText := func(text string) {
		if updateChan == nil {
			return
		}
		select {
		case updateChan <- text:
		case <-time.After(100 * time.Millisecond):
			// Таймаут чтобы не блокировать если спиннер уже завершился
		}
	}

	defer func() {
		if updateChan != nil {
			close(updateChan)
			<-spinnerDone
		}
	}()

	innerFunc := func(path string) error {
		updateText(fmt.Sprintf("Scanning: %s", path))

		progressCallback := func(currentPath string) {
			updateText(fmt.Sprintf("Scanning: %s", currentPath))
		}

		repos, err := fs.FindDirectories(
			path,
			git.DetectRepository,
			progressCallback,
			fs.DefaultSkipDirs,
		)
		if err != nil {
			return err
		}
		for _, repo := range repos {
			err = storage.AddRepo(repo)
			if err != nil {
				continue
			}
			countRepos += 1
		}

		time.Sleep(100 * time.Millisecond)

		return nil
	}

	ProcessArgsAsPaths(args, innerFunc)

	if !isSilently && countRepos > 0 {
		updateText(fmt.Sprintf("Completed! Added %d repositories", countRepos))
		time.Sleep(1 * time.Second)
	} else if countRepos == 0 {
		updateText("No repositories found")
		time.Sleep(1 * time.Second)
	}
}
