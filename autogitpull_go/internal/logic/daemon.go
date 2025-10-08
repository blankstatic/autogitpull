package logic

import (
	"fmt"
	"sync"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/lib"
)

type Daemon struct {
	interval    time.Duration
	storage     *lib.StorageManager
	isRunning   bool
	stopChan    chan struct{}
	wg          sync.WaitGroup
	mu          sync.RWMutex
	onPullStart func(repo *lib.RepoInfo)
	onPullDone  func(repo *lib.RepoInfo, result string, err error)
}

type Config struct {
	Interval    time.Duration
	ConfigPath  string
	OnPullStart func(repo *lib.RepoInfo)
	OnPullDone  func(repo *lib.RepoInfo, result string, err error)
}

func NewDaemon(config Config) (*Daemon, error) {
	storage := lib.NewStorageManager(config.ConfigPath)
	if err := storage.Load(); err != nil {
		return nil, err
	}

	return &Daemon{
		interval:    config.Interval,
		storage:     storage,
		stopChan:    make(chan struct{}),
		onPullStart: config.OnPullStart,
		onPullDone:  config.OnPullDone,
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
	defer d.mu.Unlock()

	if !d.isRunning {
		return
	}

	d.isRunning = false
	close(d.stopChan)
	d.wg.Wait()

	// Пересоздаем stopChan для возможного перезапуска
	d.stopChan = make(chan struct{})
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

	// Запускаем сразу при старте
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
		go func(repo *lib.RepoInfo) {
			defer wg.Done()
			d.pullRepo(repo)
		}(&repos[i])
	}
	wg.Wait()
}

func (d *Daemon) pullRepo(repo *lib.RepoInfo) {
	if d.onPullStart != nil {
		d.onPullStart(repo)
	}

	result, err := d.performPull(repo)

	if d.onPullDone != nil {
		d.onPullDone(repo, result, err)
	}

	// Обновляем LastSync если пул успешен
	if err == nil {
		d.storage.UpdateLastSync(repo.Path)
	}
}

func (d *Daemon) performPull(repo *lib.RepoInfo) (string, error) {
	// Проверяем текущую ветку
	currentBranch, err := lib.GetCurrentBranch(repo.Path)
	if err != nil {
		return "", fmt.Errorf("get current branch: %w", err)
	}

	if currentBranch != repo.DefaultBranch {
		return "", fmt.Errorf("current branch %s is not default branch %s", currentBranch, repo.DefaultBranch)
	}

	// Проверяем незакоммиченные изменения
	hasChanges, err := lib.GitHasUncommitedChanges(repo.Path)
	if err != nil {
		return "", fmt.Errorf("check changes: %w", err)
	}
	if hasChanges {
		return "", fmt.Errorf("repository has uncommitted changes")
	}

	// Выполняем git pull
	result, err := lib.GitPull(repo.Path)
	if err != nil {
		return result, fmt.Errorf("git pull: %w", err)
	}

	return result, nil
}

func (d *Daemon) UpdateInterval(newInterval time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isRunning {
		d.Stop()
		d.interval = newInterval
		d.Start()
	} else {
		d.interval = newInterval
	}
}
