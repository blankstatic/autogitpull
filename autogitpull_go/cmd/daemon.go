package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/lib"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/logic"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(daemonCmd)
}

var daemonCmd = &cobra.Command{
	Use: "daemon",
	Run: func(cmd *cobra.Command, args []string) {
		appDataDir, err := lib.GetAppDataDir()
		if err != nil {
			panic(err)
		}
		lock, err := lib.AcquireLock(appDataDir)
		if err != nil {
			fmt.Println("Another daemon is running. Run locked.")
			os.Exit(1)
		}
		defer lock.Release()

		configPath, err := lib.GetConfigPath()
		if err != nil {
			log.Fatal("Error getting config path:", err)
		}

		d, err := logic.NewDaemon(logic.Config{
			Interval:   30 * time.Minute, // Каждые 30 минут
			ConfigPath: configPath,
			OnPullStart: func(repo *lib.RepoInfo) {
				fmt.Printf("🔄 Pulling repository: %s\n", repo.Name)
			},
			OnPullDone: func(repo *lib.RepoInfo, result string, err error) {
				if err != nil {
					fmt.Printf("❌ Failed to pull %s: %v\n", repo.Name, err)
					// Показываем уведомление об ошибке
					// lib.ShowMessage(lib.AppName, fmt.Sprintf("Pull failed: %s", repo.Name), err.Error())
				} else {
					fmt.Printf("✅ Successfully pulled: %s\n", repo.Name)
					// Показываем уведомление об успехе
					if !strings.Contains(result, "up to date") {
						go lib.ShowMessage(lib.AppName, fmt.Sprintf("Pulled: %s", repo.Name), result)
					}
				}
			},
		})
		if err != nil {
			log.Fatal("Error creating daemon:", err)
		}

		fmt.Printf("🚀 Starting autogitpull daemon (interval: %v)\n", 30*time.Minute)
		fmt.Println("📁 Monitoring repositories:")

		storage := lib.NewStorageManager(configPath)
		if err := storage.Load(); err != nil {
			panic(err)
		}
		repos := storage.GetAllRepos()
		for _, repo := range repos {
			fmt.Printf("   - %s (%s)\n", repo.Name, repo.Path)
		}
		fmt.Println("Press Ctrl+C to stop")

		d.Start()

		// Ожидаем сигнал завершения
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		fmt.Println("\n🛑 Stopping daemon...")
		d.Stop()
		fmt.Println("👋 Daemon stopped")
	},
}
