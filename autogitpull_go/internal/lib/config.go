package lib

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Repositories []RepoInfo `json:"repositories"`
}

type RepoInfo struct {
	Path          string    `json:"path"`
	Name          string    `json:"name"`
	DefaultBranch string    `json:"default_branch"`
	AddedAt       time.Time `json:"added_at"`
	LastSync      time.Time `json:"last_sync"`
}

type StorageManager struct {
	configPath string
	config     *Config
}

func GetConfigPath() (string, error) {
	homeDir, err := GetUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, AppDataDir, ConfigFilename), nil
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

	if err := json.Unmarshal(data, &sm.config); err != nil {
		return err
	}

	return nil
}

func (sm *StorageManager) Save() error {
	data, err := json.MarshalIndent(sm.config, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(sm.configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(sm.configPath, data, 0644)
}

func (sm *StorageManager) createDefaultConfig() error {
	sm.config = &Config{
		Repositories: []RepoInfo{},
	}
	return sm.Save()
}

func (sm *StorageManager) AddRepo(path string) error {
	name := filepath.Base(path)
	for _, repo := range sm.config.Repositories {
		if repo.Path == path {
			return fmt.Errorf("repo already added: %s", path)
		}
	}

	defaultBranch, err := GetRemoteDefaultBranch(path)
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

	sm.config.Repositories = append(sm.config.Repositories, newRepo)
	return sm.Save()
}

func (sm *StorageManager) RemoveRepo(path string) error {
	for i, repo := range sm.config.Repositories {
		if repo.Path == path {
			sm.config.Repositories = append(sm.config.Repositories[:i],
				sm.config.Repositories[i+1:]...)
			return sm.Save()
		}
	}
	return fmt.Errorf("repository not found: %s", path)
}

func (sm *StorageManager) UpdateRepo(path string, updates map[string]interface{}) error {
	for i, repo := range sm.config.Repositories {
		if repo.Path == path {
			if name, ok := updates["name"].(string); ok {
				sm.config.Repositories[i].Name = name
			}
			sm.config.Repositories[i].LastSync = time.Now()
			return sm.Save()
		}
	}
	return fmt.Errorf("repository not found: %s", path)
}

func (sm *StorageManager) UpdateLastSync(path string) error {
	for i, repo := range sm.config.Repositories {
		if repo.Path == path {
			sm.config.Repositories[i].LastSync = time.Now()
			return sm.Save()
		}
	}
	return fmt.Errorf("repository not found: %s", path)
}

func (sm *StorageManager) GetRepo(path string) (*RepoInfo, error) {
	for _, repo := range sm.config.Repositories {
		if repo.Path == path {
			return &repo, nil
		}
	}
	return nil, fmt.Errorf("repository not found: %s", path)
}

func (sm *StorageManager) GetAllRepos() []RepoInfo {
	return sm.config.Repositories
}
