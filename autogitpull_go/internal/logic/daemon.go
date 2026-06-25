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
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/plugins"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/web"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/fs"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
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
	onPullDone  func(repo *config.RepoInfo, result string, err error, notify bool)
	updateStore *db.Store
}

type Config struct {
	Interval    time.Duration
	Storage     *config.StorageManager
	OnPullStart func(repo *config.RepoInfo)
	OnPullDone  func(repo *config.RepoInfo, result string, err error, notify bool)
	UpdateStore *db.Store
}

func NewDaemon(cfg Config) (*Daemon, error) {
	if cfg.Interval <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}

	storage := cfg.Storage
	if storage == nil {
		configPath, err := config.GetConfigPath()
		if err != nil {
			return nil, err
		}
		storage = config.NewStorageManager(configPath)
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

	d.pullAllRepos(false)

	for {
		interval := d.currentInterval()
		web.SetDaemonNextRun(time.Now().Add(interval))
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
			d.pullAllRepos(true)
		case <-d.stopChan:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
}

func (d *Daemon) currentInterval() time.Duration {
	if d.storage != nil {
		return d.storage.GetConfig().PullInterval()
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.interval
}

func (d *Daemon) pullAllRepos(notify bool) {
	startedAt := time.Now()
	web.SetDaemonRunStarted(startedAt)
	defer func() {
		web.SetDaemonRunFinished(time.Since(startedAt))
	}()

	if d.updateStore != nil && d.storage != nil {
		retention := d.storage.GetConfig().HistoryRetention()
		if deleted, err := d.updateStore.DeleteUpdatesBefore(time.Now().Add(-retention)); err != nil {
			slog.Error("failed to prune update history", slog.String("err", err.Error()))
		} else if deleted > 0 {
			slog.Info("pruned update history", slog.Int64("deleted", deleted))
		}
	}

	repos := d.storage.GetAllRepos()

	var wg sync.WaitGroup
	for i := range repos {
		if repos[i].Paused {
			continue
		}
		wg.Add(1)
		go func(repo *config.RepoInfo) {
			defer wg.Done()
			d.pullRepo(repo, notify)
		}(&repos[i])
	}
	wg.Wait()
}

func (d *Daemon) pullRepo(repo *config.RepoInfo, notify bool) {
	web.SetDaemonRepoRunning(repo.Name, true)
	defer web.SetDaemonRepoRunning(repo.Name, false)

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
	web.AddDaemonRunResult(updateStatus(err))

	if d.updateStore != nil && updateID > 0 {
		if recordErr := d.updateStore.FinishUpdate(updateID, result, err); recordErr != nil {
			slog.Error("failed to record update result", slog.String("repo", repo.Name), slog.String("err", recordErr.Error()))
		} else if err == nil {
			d.runPluginsAfterChange(repo, updateID, notify)
		}
	}

	if d.onPullDone != nil {
		d.onPullDone(repo, result, err, notify)
	}

	if err == nil {
		if syncErr := d.storage.UpdateLastSync(repo.Path); syncErr != nil {
			slog.Error("failed to update last sync", slog.String("repo", repo.Name), slog.String("err", syncErr.Error()))
		}
	}
}

func (d *Daemon) runPluginsAfterChange(repo *config.RepoInfo, updateID int64, notify bool) {
	if d.updateStore == nil || d.storage == nil {
		return
	}
	update, err := d.updateStore.GetUpdate(updateID)
	if err != nil {
		slog.Error("failed to load update for plugins", slog.String("repo", repo.Name), slog.String("err", err.Error()))
		return
	}
	plugins.RunAfterChange(plugins.Context{
		Repo:      repo,
		Update:    update,
		Notify:    notify,
		Source:    "daemon",
		Dashboard: "http://localhost:9009",
		OpenURL:   "http://localhost:9009/repo?path=" + urlQueryEscape(repo.Path),
		AppName:   config.AppName,
		Logger:    slog.Default(),
	}, d.storage.GetPluginStates())
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "%2F", "/")
}

func updateStatus(err error) string {
	if err == nil {
		return "success"
	}
	if db.IsSkippedPullError(err.Error()) {
		return "skipped"
	}
	return "error"
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

	cfg := storage.GetConfig()
	interval := cfg.PullInterval()

	d, err := NewDaemon(Config{
		Interval:    interval,
		Storage:     storage,
		UpdateStore: updateStore,
		OnPullStart: func(repo *config.RepoInfo) {
			slog.Info("Pulling repository", slog.String("repo", repo.Name))
		},
		OnPullDone: func(repo *config.RepoInfo, result string, err error, notify bool) {
			if err != nil {
				slog.Warn("Failed to pull", slog.String("repo", repo.Name), slog.String("err", err.Error()))
			} else {
				slog.Info("Successfully pulled", slog.String("repo", repo.Name))
			}
		},
	})
	if err != nil {
		slog.Error("Error creating daemon", slog.String("err", err.Error()))
		panic(err)
	}

	slog.Info(fmt.Sprintf("Starting autogitpull daemon (interval: %v)", interval))

	repos := storage.GetAllRepos()
	for _, repo := range repos {
		if repo.Paused {
			slog.Info("Skipping paused repo", slog.String("repo", repo.Name), slog.String("path", repo.Path))
			continue
		}
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
