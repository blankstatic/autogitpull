package config

import (
	"path/filepath"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/fs"
)

const (
	AppName                     = "autogitpull"
	AppDataDir                  = ".autogitpull"
	ConfigFilename              = "config.json"
	DefaultPullIntervalMinutes  = 30
	DefaultHistoryRetentionDays = 365
)

type Config struct {
	Repositories         []RepoInfo `json:"repositories"`
	PullIntervalMinutes  int        `json:"pull_interval_minutes,omitempty"`
	HistoryRetentionDays int        `json:"history_retention_days,omitempty"`
}

type RepoInfo struct {
	Path          string    `json:"path"`
	Name          string    `json:"name"`
	DefaultBranch string    `json:"default_branch"`
	AddedAt       time.Time `json:"added_at"`
	LastSync      time.Time `json:"last_sync"`
	Paused        bool      `json:"paused,omitempty"`
}

func (c Config) HistoryRetention() time.Duration {
	if c.HistoryRetentionDays <= 0 {
		return time.Duration(DefaultHistoryRetentionDays) * 24 * time.Hour
	}
	return time.Duration(c.HistoryRetentionDays) * 24 * time.Hour
}

func (c Config) PullInterval() time.Duration {
	if c.PullIntervalMinutes <= 0 {
		return time.Duration(DefaultPullIntervalMinutes) * time.Minute
	}
	return time.Duration(c.PullIntervalMinutes) * time.Minute
}

func GetConfigPath() (string, error) {
	homeDir, err := fs.GetUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, AppDataDir, ConfigFilename), nil
}

func GetUpdatesDBPath() (string, error) {
	homeDir, err := fs.GetUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, AppDataDir, "updates.sqlite"), nil
}
