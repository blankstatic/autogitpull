package config

import (
	"path/filepath"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/fs"
)

const (
	AppName        = "autogitpull"
	AppDataDir     = ".autogitpull"
	ConfigFilename = "config.json"
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

func GetConfigPath() (string, error) {
	homeDir, err := fs.GetUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, AppDataDir, ConfigFilename), nil
}
