package logic

import (
	"fmt"
	"time"

	"github.com/blankstatic/autogitpull/internal/lib"
	"github.com/blankstatic/autogitpull/internal/ui"
	"github.com/spf13/cobra"
)

func GetDiscoverFunc(cmd *cobra.Command, args []string) {
	isSilently := lib.GetIsSilentlyValue(cmd)

	configPath, err := lib.GetConfigPath()
	if err != nil {
		panic(err)
	}

	storage := lib.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		panic(err)
	}

	var countRepos uint

	// Создаем канал для обновления текста
	updateChan := make(chan string)

	// Запускаем спиннер с поддержкой обновлений
	go func() {
		ui.RunSpinnerWithUpdates(updateChan)
	}()

	// Функция для безопасной отправки сообщений
	updateText := func(text string) {
		select {
		case updateChan <- text:
		case <-time.After(100 * time.Millisecond):
			// Таймаут чтобы не блокировать если спиннер уже завершился
		}
	}

	defer close(updateChan) // Закрываем канал при завершении

	innerFunc := func(path string) error {
		// Обновляем текст спиннера с текущей директорией
		updateText(fmt.Sprintf("Scanning: %s", path))

		// Callback для обновления текущей директории
		progressCallback := func(currentPath string) {
			updateText(fmt.Sprintf("Scanning: %s", currentPath))
		}

		repos, err := lib.FindDirectories(path, lib.DetectRepository, progressCallback)
		if err != nil {
			return err
		}
		for _, repo := range repos {
			err = storage.AddRepo(repo)
			if err != nil {
				// fmt.Println(err)
				continue
			}
			countRepos += 1
		}

		// Небольшая задержка для лучшей видимости спиннера
		time.Sleep(100 * time.Millisecond)

		return nil
	}

	lib.ProcessArgsAsPaths(args, innerFunc)

	// Финальное сообщение
	if !isSilently && countRepos > 0 {
		updateText(fmt.Sprintf("Completed! Added %d repositories", countRepos))
		time.Sleep(1 * time.Second) // Даем время увидеть финальное сообщение
	} else if countRepos == 0 {
		updateText("No repositories found")
		time.Sleep(1 * time.Second)
	}
}
