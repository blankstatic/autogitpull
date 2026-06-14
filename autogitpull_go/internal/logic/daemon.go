package logic

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/web"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/fs"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/notifications"
	"github.com/spf13/cobra"
)

type Daemon struct {
	interval    time.Duration
	storage     *config.StorageManager
	isRunning   bool
	stopChan    chan struct{}
	wg          sync.WaitGroup
	mu          sync.RWMutex
	onPullStart func(repo *config.RepoInfo)
	onPullDone  func(repo *config.RepoInfo, result string, err error)
	updateStore *db.Store
}

type Config struct {
	Interval    time.Duration
	ConfigPath  string
	Storage     *config.StorageManager
	OnPullStart func(repo *config.RepoInfo)
	OnPullDone  func(repo *config.RepoInfo, result string, err error)
	UpdateStore *db.Store
}

func NewDaemon(cfg Config) (*Daemon, error) {
	if cfg.Interval <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}

	storage := cfg.Storage
	if storage == nil {
		storage = config.NewStorageManager(cfg.ConfigPath)
		if err := storage.Load(); err != nil {
			return nil, err
		}
	}

	return &Daemon{
		interval:    cfg.Interval,
		storage:     storage,
		stopChan:    make(chan struct{}),
		onPullStart: cfg.OnPullStart,
		onPullDone:  cfg.OnPullDone,
		updateStore: cfg.UpdateStore,
	}, nil
}

func (d *Daemon) Start() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isRunning {
		return
	}

	d.isRunning = true
	d.wg.Add(1)

	go d.run()
}

func (d *Daemon) Stop() {
	d.mu.Lock()
	if !d.isRunning {
		d.mu.Unlock()
		return
	}

	d.isRunning = false
	stopChan := d.stopChan
	close(stopChan)
	d.mu.Unlock()

	d.wg.Wait()

	d.mu.Lock()
	if d.stopChan == stopChan {
		d.stopChan = make(chan struct{})
	}
	d.mu.Unlock()
}

func (d *Daemon) IsRunning() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.isRunning
}

func (d *Daemon) run() {
	defer d.wg.Done()

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	d.pullAllRepos()

	for {
		select {
		case <-ticker.C:
			d.pullAllRepos()
		case <-d.stopChan:
			return
		}
	}
}

func (d *Daemon) pullAllRepos() {
	repos := d.storage.GetAllRepos()

	var wg sync.WaitGroup
	for i := range repos {
		wg.Add(1)
		go func(repo *config.RepoInfo) {
			defer wg.Done()
			d.pullRepo(repo)
		}(&repos[i])
	}
	wg.Wait()
}

func (d *Daemon) pullRepo(repo *config.RepoInfo) {
	var updateID int64
	if d.updateStore != nil {
		var err error
		updateID, err = d.updateStore.BeginUpdate(repo.Path, repo.Name)
		if err != nil {
			slog.Error("failed to record update start", slog.String("repo", repo.Name), slog.String("err", err.Error()))
		}
	}

	if d.onPullStart != nil {
		d.onPullStart(repo)
	}

	result, err := d.performPull(repo)

	if d.updateStore != nil && updateID > 0 {
		if recordErr := d.updateStore.FinishUpdate(updateID, result, err); recordErr != nil {
			slog.Error("failed to record update result", slog.String("repo", repo.Name), slog.String("err", recordErr.Error()))
		}
	}

	if d.onPullDone != nil {
		d.onPullDone(repo, result, err)
	}

	if err == nil {
		if syncErr := d.storage.UpdateLastSync(repo.Path); syncErr != nil {
			slog.Error("failed to update last sync", slog.String("repo", repo.Name), slog.String("err", syncErr.Error()))
		}
	}
}

func (d *Daemon) performPull(repo *config.RepoInfo) (string, error) {
	currentBranch, err := git.GetCurrentBranch(repo.Path)
	if err != nil {
		return "", fmt.Errorf("get current branch: %w", err)
	}

	if currentBranch != repo.DefaultBranch {
		return "", fmt.Errorf("current branch %s is not default branch %s", currentBranch, repo.DefaultBranch)
	}

	hasChanges, err := git.GitHasUncommitedChanges(repo.Path)
	if err != nil {
		return "", fmt.Errorf("check changes: %w", err)
	}
	if hasChanges {
		return "", fmt.Errorf("repository has uncommitted changes")
	}

	result, err := git.GitPull(repo.Path)
	if err != nil {
		return result, fmt.Errorf("git pull: %w", err)
	}

	return result, nil
}

func (d *Daemon) UpdateInterval(newInterval time.Duration) {
	if newInterval <= 0 {
		return
	}

	d.mu.Lock()
	wasRunning := d.isRunning
	d.mu.Unlock()

	if wasRunning {
		d.Stop()
	}

	d.mu.Lock()
	d.interval = newInterval
	d.mu.Unlock()

	if wasRunning {
		d.Start()
	}
}

func DaemonCommandHandler(cmd *cobra.Command, args []string) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	appDataDir, err := fs.GetAppDataDir(config.AppDataDir)
	if err != nil {
		panic(err)
	}
	lock, err := AcquireLock(appDataDir)
	if err != nil {
		logger.Info("Another daemon is running. Run locked.")
		os.Exit(1)
	}
	defer lock.Release()

	configPath, err := config.GetConfigPath()
	if err != nil {
		slog.Error("Error getting config path", slog.String("err", err.Error()))
		panic(err)
	}

	updatesDBPath, err := config.GetUpdatesDBPath()
	if err != nil {
		slog.Error("Error getting updates database path", slog.String("err", err.Error()))
		panic(err)
	}

	updateStore, err := db.Open(updatesDBPath)
	if err != nil {
		slog.Error("Error opening updates database", slog.String("err", err.Error()))
		panic(err)
	}
	defer updateStore.Close()

	storage := config.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		panic(err)
	}
	web.New(updateStore, storage).Start()

	d, err := NewDaemon(Config{
		Interval:    30 * time.Minute,
		ConfigPath:  configPath,
		Storage:     storage,
		UpdateStore: updateStore,
		OnPullStart: func(repo *config.RepoInfo) {
			slog.Info("Pulling repository", slog.String("repo", repo.Name))
		},
		OnPullDone: func(repo *config.RepoInfo, result string, err error) {
			if err != nil {
				slog.Warn("Failed to pull", slog.String("repo", repo.Name), slog.String("err", err.Error()))
			} else {
				slog.Info("Successfully pulled", slog.String("repo", repo.Name))
				if !strings.Contains(result, "up to date") {
					notifyURL := "http://localhost:9009/repo?path=" + url.QueryEscape(repo.Path)
					go notifications.OSNotifyURL(config.AppName, fmt.Sprintf("Pulled: %s", repo.Name), result, notifyURL)
				}
			}
		},
	})
	if err != nil {
		slog.Error("Error creating daemon", slog.String("err", err.Error()))
		panic(err)
	}

	slog.Info(fmt.Sprintf("Starting autogitpull daemon (interval: %v)", 30*time.Minute))

	repos := storage.GetAllRepos()
	for _, repo := range repos {
		slog.Info("Monitoring repo", slog.String("repo", repo.Name), slog.String("path", repo.Path))
	}

	d.Start()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Warn("Stopping daemon...")
	d.Stop()
	slog.Warn("Daemon stopped")

}
