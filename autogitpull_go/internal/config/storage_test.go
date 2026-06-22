package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadCreatesDefaultConfig(t *testing.T) {
	sm := NewStorageManager(filepath.Join(t.TempDir(), "config.json"))
	if err := sm.Load(); err != nil {
		t.Fatal(err)
	}

	repos := sm.GetAllRepos()
	if repos == nil {
		t.Fatal("expected non-nil repositories slice")
	}
	if len(repos) != 0 {
		t.Fatalf("expected no repositories, got %d", len(repos))
	}
	if got := sm.GetConfig().PullIntervalMinutes; got != DefaultPullIntervalMinutes {
		t.Fatalf("expected default pull interval %d, got %d", DefaultPullIntervalMinutes, got)
	}
	if got := sm.GetConfig().HistoryRetentionDays; got != DefaultHistoryRetentionDays {
		t.Fatalf("expected default history retention %d, got %d", DefaultHistoryRetentionDays, got)
	}
}

func TestGetAllReposReturnsCopy(t *testing.T) {
	sm := NewStorageManager(filepath.Join(t.TempDir(), "config.json"))
	sm.config = &Config{Repositories: []RepoInfo{{
		Path:          "/repo/a",
		Name:          "a",
		DefaultBranch: "main",
		AddedAt:       time.Now(),
		LastSync:      time.Now(),
	}}}

	repos := sm.GetAllRepos()
	repos[0].Name = "mutated"
	repos = append(repos, RepoInfo{Name: "extra"})

	stored := sm.GetAllRepos()
	if len(stored) != 1 {
		t.Fatalf("expected stored repo count to remain 1, got %d", len(stored))
	}
	if stored[0].Name != "a" {
		t.Fatalf("expected stored repo name to remain unchanged, got %q", stored[0].Name)
	}
}

func TestSetRepoPausedAndPullInterval(t *testing.T) {
	sm := NewStorageManager(filepath.Join(t.TempDir(), "config.json"))
	sm.config = &Config{Repositories: []RepoInfo{{
		Path:          "/repo/a",
		Name:          "a",
		DefaultBranch: "main",
	}}}

	if err := sm.SetRepoPaused("/repo/a", true); err != nil {
		t.Fatal(err)
	}
	repo, err := sm.GetRepo("/repo/a")
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Paused {
		t.Fatal("expected repo to be paused")
	}

	if err := sm.SetPullIntervalMinutes(5); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetHistoryRetentionDays(90); err != nil {
		t.Fatal(err)
	}
	if got := sm.GetConfig().PullInterval(); got != 5*time.Minute {
		t.Fatalf("expected 5 minute interval, got %s", got)
	}
	if got := sm.GetConfig().HistoryRetention(); got != 90*24*time.Hour {
		t.Fatalf("expected 90 day retention, got %s", got)
	}
}

func TestGetRepoReturnsCopy(t *testing.T) {
	sm := NewStorageManager(filepath.Join(t.TempDir(), "config.json"))
	sm.config = &Config{Repositories: []RepoInfo{{
		Path:          "/repo/a",
		Name:          "a",
		DefaultBranch: "main",
	}}}

	repo, err := sm.GetRepo("/repo/a")
	if err != nil {
		t.Fatal(err)
	}
	repo.Name = "mutated"

	stored, err := sm.GetRepo("/repo/a")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "a" {
		t.Fatalf("expected stored repo name to remain unchanged, got %q", stored.Name)
	}
}
