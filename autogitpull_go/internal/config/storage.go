package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
)

type StorageManager struct {
	configPath string
	config     *Config
	mu         sync.RWMutex
}

func NewStorageManager(configPath string) *StorageManager {
	return &StorageManager{
		configPath: configPath,
		config:     &Config{},
	}
}

func (sm *StorageManager) Load() error {
	data, err := os.ReadFile(sm.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return sm.createDefaultConfig()
		}
		return err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if cfg.Repositories == nil {
		cfg.Repositories = []RepoInfo{}
	}
	if cfg.PullIntervalMinutes <= 0 {
		cfg.PullIntervalMinutes = DefaultPullIntervalMinutes
	}
	if cfg.HistoryRetentionDays <= 0 {
		cfg.HistoryRetentionDays = DefaultHistoryRetentionDays
	}

	sm.mu.Lock()
	sm.config = &cfg
	sm.mu.Unlock()
	return nil
}

func (sm *StorageManager) Save() error {
	sm.mu.RLock()
	cfg := cloneConfig(sm.config)
	sm.mu.RUnlock()

	return sm.saveConfig(cfg)
}

func (sm *StorageManager) ConfigPath() string {
	return sm.configPath
}

func (sm *StorageManager) saveConfig(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(sm.configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(sm.configPath)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, sm.configPath)
}

func (sm *StorageManager) createDefaultConfig() error {
	cfg := &Config{
		Repositories:         []RepoInfo{},
		PullIntervalMinutes:  DefaultPullIntervalMinutes,
		HistoryRetentionDays: DefaultHistoryRetentionDays,
	}
	if err := sm.saveConfig(cfg); err != nil {
		return err
	}
	sm.mu.Lock()
	sm.config = cfg
	sm.mu.Unlock()
	return nil
}

func (sm *StorageManager) AddRepo(path string) error {
	name := filepath.Base(path)

	defaultBranch, err := git.GetRemoteDefaultBranch(path)
	if err != nil || defaultBranch == "" {
		return fmt.Errorf("remote default branch not detected: %s", path)
	}

	newRepo := RepoInfo{
		Path:          path,
		Name:          name,
		DefaultBranch: defaultBranch,
		AddedAt:       time.Now(),
		LastSync:      time.Now(),
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	cfg := cloneConfig(sm.config)
	for _, repo := range cfg.Repositories {
		if repo.Path == path {
			return fmt.Errorf("repo already added: %s", path)
		}
	}
	cfg.Repositories = append(cfg.Repositories, newRepo)
	if err := sm.saveConfig(cfg); err != nil {
		return err
	}
	sm.config = cfg
	return nil
}

func (sm *StorageManager) RemoveRepo(path string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cfg := cloneConfig(sm.config)

	for i, repo := range cfg.Repositories {
		if repo.Path == path {
			cfg.Repositories = append(cfg.Repositories[:i],
				cfg.Repositories[i+1:]...)
			if err := sm.saveConfig(cfg); err != nil {
				return err
			}
			sm.config = cfg
			return nil
		}
	}
	return fmt.Errorf("repository not found: %s", path)
}

func (sm *StorageManager) UpdateRepo(path string, updates map[string]interface{}) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cfg := cloneConfig(sm.config)

	for i, repo := range cfg.Repositories {
		if repo.Path == path {
			if name, ok := updates["name"].(string); ok {
				cfg.Repositories[i].Name = name
			}
			cfg.Repositories[i].LastSync = time.Now()
			if err := sm.saveConfig(cfg); err != nil {
				return err
			}
			sm.config = cfg
			return nil
		}
	}
	return fmt.Errorf("repository not found: %s", path)
}

func (sm *StorageManager) GetConfig() Config {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return *cloneConfig(sm.config)
}

func (sm *StorageManager) SetPullIntervalMinutes(minutes int) error {
	if minutes <= 0 {
		return fmt.Errorf("pull interval must be positive")
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	cfg := cloneConfig(sm.config)
	cfg.PullIntervalMinutes = minutes
	if err := sm.saveConfig(cfg); err != nil {
		return err
	}
	sm.config = cfg
	return nil
}

func (sm *StorageManager) SetHistoryRetentionDays(days int) error {
	if days <= 0 {
		return fmt.Errorf("history retention must be positive")
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	cfg := cloneConfig(sm.config)
	cfg.HistoryRetentionDays = days
	if err := sm.saveConfig(cfg); err != nil {
		return err
	}
	sm.config = cfg
	return nil
}

func (sm *StorageManager) SetRepoPaused(path string, paused bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cfg := cloneConfig(sm.config)
	for i, repo := range cfg.Repositories {
		if repo.Path == path {
			cfg.Repositories[i].Paused = paused
			if err := sm.saveConfig(cfg); err != nil {
				return err
			}
			sm.config = cfg
			return nil
		}
	}
	return fmt.Errorf("repository not found: %s", path)
}

func (sm *StorageManager) SetRepoNotify(path string, notify bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cfg := cloneConfig(sm.config)
	for i, repo := range cfg.Repositories {
		if repo.Path == path {
			cfg.Repositories[i].Notify = boolPtr(notify)
			if err := sm.saveConfig(cfg); err != nil {
				return err
			}
			sm.config = cfg
			return nil
		}
	}
	return fmt.Errorf("repository not found: %s", path)
}

func (sm *StorageManager) UpdateLastSync(path string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cfg := cloneConfig(sm.config)

	for i, repo := range cfg.Repositories {
		if repo.Path == path {
			cfg.Repositories[i].LastSync = time.Now()
			if err := sm.saveConfig(cfg); err != nil {
				return err
			}
			sm.config = cfg
			return nil
		}
	}
	return fmt.Errorf("repository not found: %s", path)
}

func (sm *StorageManager) GetRepo(path string) (*RepoInfo, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, repo := range sm.config.Repositories {
		if repo.Path == path {
			repoCopy := repo
			return &repoCopy, nil
		}
	}
	return nil, fmt.Errorf("repository not found: %s", path)
}

func (sm *StorageManager) GetAllRepos() []RepoInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	repos := make([]RepoInfo, len(sm.config.Repositories))
	copy(repos, sm.config.Repositories)
	return repos
}

func cloneConfig(cfg *Config) *Config {
	if cfg == nil {
		return &Config{Repositories: []RepoInfo{}, PullIntervalMinutes: DefaultPullIntervalMinutes, HistoryRetentionDays: DefaultHistoryRetentionDays}
	}
	repos := make([]RepoInfo, len(cfg.Repositories))
	copy(repos, cfg.Repositories)
	for i := range repos {
		if repos[i].Notify != nil {
			notify := *repos[i].Notify
			repos[i].Notify = &notify
		}
	}
	pullIntervalMinutes := cfg.PullIntervalMinutes
	if pullIntervalMinutes <= 0 {
		pullIntervalMinutes = DefaultPullIntervalMinutes
	}
	historyRetentionDays := cfg.HistoryRetentionDays
	if historyRetentionDays <= 0 {
		historyRetentionDays = DefaultHistoryRetentionDays
	}
	return &Config{Repositories: repos, PullIntervalMinutes: pullIntervalMinutes, HistoryRetentionDays: historyRetentionDays}
}

func boolPtr(v bool) *bool {
	return &v
}
