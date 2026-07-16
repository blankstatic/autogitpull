package config

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestLoadCreatesDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	sm := NewStorageManager(configPath)
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
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected fresh install not to create legacy config file, stat err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, UpdatesDBFilename)); err != nil {
		t.Fatalf("expected storage database to exist: %v", err)
	}
}

func TestGetAllReposReturnsCopy(t *testing.T) {
	sm := NewStorageManager(filepath.Join(t.TempDir(), "config.json"))
	seedConfig(t, sm, Config{Repositories: []RepoInfo{{
		Path:          "/repo/a",
		Name:          "a",
		DefaultBranch: "main",
		AddedAt:       time.Now(),
		LastSync:      time.Now(),
	}}})

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
	seedConfig(t, sm, Config{Repositories: []RepoInfo{{
		Path:          "/repo/a",
		Name:          "a",
		DefaultBranch: "main",
	}}})

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

func TestPluginStateRoundTrip(t *testing.T) {
	sm := NewStorageManager(filepath.Join(t.TempDir(), "config.json"))
	if err := sm.Load(); err != nil {
		t.Fatal(err)
	}

	state := PluginState{
		ID:      "notifications",
		Enabled: false,
		Config:  map[string]string{"title_prefix": "Changed"},
	}
	if err := sm.SetPluginState(state); err != nil {
		t.Fatal(err)
	}

	states := sm.GetPluginStates()
	got := states["notifications"]
	if got.ID != state.ID || got.Enabled || got.Config["title_prefix"] != "Changed" {
		t.Fatalf("unexpected plugin state: %+v", got)
	}
}

func TestGetRepoReturnsCopy(t *testing.T) {
	sm := NewStorageManager(filepath.Join(t.TempDir(), "config.json"))
	seedConfig(t, sm, Config{Repositories: []RepoInfo{{
		Path:          "/repo/a",
		Name:          "a",
		DefaultBranch: "main",
	}}})

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

func TestLoadMigratesLegacyConfigJSON(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	legacy := Config{
		Repositories: []RepoInfo{{
			Path:          "/repo/legacy",
			Name:          "legacy",
			DefaultBranch: "main",
			AddedAt:       time.Now(),
			LastSync:      time.Now(),
		}},
		PullIntervalMinutes:  7,
		HistoryRetentionDays: 30,
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	sm := NewStorageManager(configPath)
	if err := sm.Load(); err != nil {
		t.Fatal(err)
	}

	repos := sm.GetAllRepos()
	if len(repos) != 1 || repos[0].Path != "/repo/legacy" {
		t.Fatalf("expected migrated repo, got %+v", repos)
	}
	if got := sm.GetConfig().PullIntervalMinutes; got != 7 {
		t.Fatalf("expected migrated pull interval 7, got %d", got)
	}
}

func TestLoadMigratesLegacyConfigIntoExistingUpdatesDatabase(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	dbPath := filepath.Join(dir, UpdatesDBFilename)

	oldDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldDB.Exec(`
		CREATE TABLE updates (id INTEGER PRIMARY KEY, repo_path TEXT NOT NULL);
		INSERT INTO updates (repo_path) VALUES ('/repo/history');
	`); err != nil {
		t.Fatal(err)
	}
	if err := oldDB.Close(); err != nil {
		t.Fatal(err)
	}

	notify := false
	legacy := Config{
		Repositories: []RepoInfo{{
			Path:          "/repo/legacy",
			Name:          "legacy",
			DefaultBranch: "main",
			AddedAt:       time.Now(),
			LastSync:      time.Now(),
			Paused:        true,
			Notify:        &notify,
		}},
		PullIntervalMinutes:  7,
		HistoryRetentionDays: 30,
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	sm := NewStorageManager(configPath)
	if err := sm.Load(); err != nil {
		t.Fatal(err)
	}

	repo, err := sm.GetRepo("/repo/legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Paused || repo.NotificationsEnabled() {
		t.Fatalf("expected paused repo with muted notifications, got %+v", repo)
	}
	if got := sm.GetConfig().HistoryRetentionDays; got != 30 {
		t.Fatalf("expected migrated retention 30, got %d", got)
	}

	checkDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer checkDB.Close()
	var historyCount int
	if err := checkDB.QueryRow(`SELECT COUNT(*) FROM updates WHERE repo_path = '/repo/history'`).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 1 {
		t.Fatalf("expected existing update history to survive migration, got %d rows", historyCount)
	}
}

func TestLegacyConfigMigratesOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	legacy := Config{Repositories: []RepoInfo{{
		Path:          "/repo/legacy",
		Name:          "legacy",
		DefaultBranch: "main",
		AddedAt:       time.Now(),
		LastSync:      time.Now(),
	}}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	sm := NewStorageManager(configPath)
	if err := sm.Load(); err != nil {
		t.Fatal(err)
	}
	if err := sm.RemoveRepo("/repo/legacy"); err != nil {
		t.Fatal(err)
	}
	if err := sm.Load(); err != nil {
		t.Fatal(err)
	}
	if repos := sm.GetAllRepos(); len(repos) != 0 {
		t.Fatalf("expected legacy config not to be imported twice, got %+v", repos)
	}
}

func TestStoragePersistsAcrossManagers(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	first := NewStorageManager(configPath)
	seedConfig(t, first, Config{Repositories: []RepoInfo{{
		Path:          "/repo/a",
		Name:          "a",
		DefaultBranch: "main",
	}}})
	if err := first.SetRepoPaused("/repo/a", true); err != nil {
		t.Fatal(err)
	}

	second := NewStorageManager(configPath)
	if err := second.Load(); err != nil {
		t.Fatal(err)
	}
	repo, err := second.GetRepo("/repo/a")
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Paused {
		t.Fatal("expected repo pause state to persist")
	}
}

func seedConfig(t *testing.T, sm *StorageManager, cfg Config) {
	t.Helper()
	db, err := sm.open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg = *cloneConfig(&cfg)
	if err := sm.saveConfig(db, &cfg); err != nil {
		t.Fatal(err)
	}
}
