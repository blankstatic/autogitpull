package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/blankstatic/autogitpull/src/pkg/git"
)

type StorageManager struct {
	dbPath           string
	legacyConfigPath string
	mu               sync.Mutex
}

func NewStorageManager(configPath string) *StorageManager {
	return &StorageManager{
		dbPath:           storageDBPath(configPath),
		legacyConfigPath: configPath,
	}
}

func storageDBPath(configPath string) string {
	if filepath.Base(configPath) == ConfigFilename {
		return filepath.Join(filepath.Dir(configPath), UpdatesDBFilename)
	}
	return configPath
}

func (sm *StorageManager) Load() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := sm.ensureDefaults(db); err != nil {
		return err
	}
	return sm.migrateLegacyConfig(db)
}

func (sm *StorageManager) Save() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return err
	}
	defer db.Close()

	cfg, err := sm.loadConfig(db)
	if err != nil {
		return err
	}
	return sm.saveConfig(db, cfg)
}

func (sm *StorageManager) AddRepo(path string) error {
	name := filepath.Base(path)

	defaultBranch, err := git.GetRemoteDefaultBranch(path)
	if err != nil || defaultBranch == "" {
		return fmt.Errorf("remote default branch not detected: %s", path)
	}

	repo := RepoInfo{
		Path:          path,
		Name:          name,
		DefaultBranch: defaultBranch,
		AddedAt:       time.Now(),
		LastSync:      time.Now(),
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return err
	}
	defer db.Close()

	res, err := db.Exec(`
		INSERT OR IGNORE INTO repositories (path, name, default_branch, added_at, last_sync, paused, notify)
		VALUES (?, ?, ?, ?, ?, 0, NULL)
	`, repo.Path, repo.Name, repo.DefaultBranch, formatDBTime(repo.AddedAt), formatDBTime(repo.LastSync))
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("repo already added: %s", path)
	}
	return nil
}

func (sm *StorageManager) RemoveRepo(path string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return err
	}
	defer db.Close()

	res, err := db.Exec(`DELETE FROM repositories WHERE path = ?`, path)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("repository not found: %s", path)
	}
	return nil
}

func (sm *StorageManager) UpdateRepo(path string, updates map[string]interface{}) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return err
	}
	defer db.Close()

	name, rename := updates["name"].(string)
	var res sql.Result
	if rename {
		res, err = db.Exec(`UPDATE repositories SET name = ?, last_sync = ? WHERE path = ?`, name, formatDBTime(time.Now()), path)
	} else {
		res, err = db.Exec(`UPDATE repositories SET last_sync = ? WHERE path = ?`, formatDBTime(time.Now()), path)
	}
	if err != nil {
		return err
	}
	return requireAffected(res, path)
}

func (sm *StorageManager) GetConfig() Config {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return defaultConfig()
	}
	defer db.Close()

	cfg, err := sm.loadConfig(db)
	if err != nil {
		return defaultConfig()
	}
	return *cfg
}

func (sm *StorageManager) SetPullIntervalMinutes(minutes int) error {
	if minutes <= 0 {
		return fmt.Errorf("pull interval must be positive")
	}
	return sm.setSetting("pull_interval_minutes", strconv.Itoa(minutes))
}

func (sm *StorageManager) SetHistoryRetentionDays(days int) error {
	if days <= 0 {
		return fmt.Errorf("history retention must be positive")
	}
	return sm.setSetting("history_retention_days", strconv.Itoa(days))
}

func (sm *StorageManager) SetRepoPaused(path string, paused bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return err
	}
	defer db.Close()

	res, err := db.Exec(`UPDATE repositories SET paused = ? WHERE path = ?`, boolToInt(paused), path)
	if err != nil {
		return err
	}
	return requireAffected(res, path)
}

func (sm *StorageManager) SetRepoNotify(path string, notify bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return err
	}
	defer db.Close()

	res, err := db.Exec(`UPDATE repositories SET notify = ? WHERE path = ?`, boolToInt(notify), path)
	if err != nil {
		return err
	}
	return requireAffected(res, path)
}

func (sm *StorageManager) UpdateLastSync(path string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return err
	}
	defer db.Close()

	res, err := db.Exec(`UPDATE repositories SET last_sync = ? WHERE path = ?`, formatDBTime(time.Now()), path)
	if err != nil {
		return err
	}
	return requireAffected(res, path)
}

func (sm *StorageManager) GetRepo(path string) (*RepoInfo, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	repo, err := scanRepo(db.QueryRow(`
		SELECT path, name, default_branch, added_at, last_sync, paused, notify
		FROM repositories
		WHERE path = ?
	`, path))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("repository not found: %s", path)
	}
	return repo, err
}

func (sm *StorageManager) GetAllRepos() []RepoInfo {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return []RepoInfo{}
	}
	defer db.Close()

	repos, err := sm.loadRepos(db)
	if err != nil {
		return []RepoInfo{}
	}
	return repos
}

func (sm *StorageManager) setSetting(key, value string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

func (sm *StorageManager) open() (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(sm.dbPath), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", sm.dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := sm.migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (sm *StorageManager) migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS repositories (
			path TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			default_branch TEXT NOT NULL,
			added_at TEXT NOT NULL,
			last_sync TEXT NOT NULL,
			paused INTEGER NOT NULL DEFAULT 0,
			notify INTEGER NULL
		);
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS plugin_settings (
			id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}'
		);
	`)
	return err
}

func (sm *StorageManager) GetPluginStates() map[string]PluginState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return map[string]PluginState{}
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, enabled, config_json FROM plugin_settings`)
	if err != nil {
		return map[string]PluginState{}
	}
	defer rows.Close()

	states := map[string]PluginState{}
	for rows.Next() {
		var state PluginState
		var enabled int
		var configJSON string
		if err := rows.Scan(&state.ID, &enabled, &configJSON); err != nil {
			continue
		}
		state.Enabled = enabled != 0
		if err := json.Unmarshal([]byte(configJSON), &state.Config); err != nil || state.Config == nil {
			state.Config = map[string]string{}
		}
		states[state.ID] = state
	}
	return states
}

func (sm *StorageManager) SetPluginState(state PluginState) error {
	if state.ID == "" {
		return fmt.Errorf("plugin id is required")
	}
	configJSON, err := json.Marshal(state.Config)
	if err != nil {
		return err
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	db, err := sm.open()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`
		INSERT INTO plugin_settings (id, enabled, config_json)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled, config_json = excluded.config_json
	`, state.ID, boolToInt(state.Enabled), string(configJSON))
	return err
}

func (sm *StorageManager) ensureDefaults(db *sql.DB) error {
	if err := sm.ensureSetting(db, "pull_interval_minutes", strconv.Itoa(DefaultPullIntervalMinutes)); err != nil {
		return err
	}
	return sm.ensureSetting(db, "history_retention_days", strconv.Itoa(DefaultHistoryRetentionDays))
}

func (sm *StorageManager) ensureSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`, key, value)
	return err
}

func (sm *StorageManager) migrateLegacyConfig(db *sql.DB) error {
	if filepath.Base(sm.legacyConfigPath) != ConfigFilename {
		return nil
	}
	migrated, err := sm.hasSetting(db, "legacy_config_migrated")
	if err != nil {
		return err
	}
	if migrated {
		return nil
	}
	data, err := os.ReadFile(sm.legacyConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return sm.markLegacyConfigMigrated(db)
		}
		return err
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM repositories`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return sm.markLegacyConfigMigrated(db)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	cfg = *cloneConfig(&cfg)
	if err := sm.saveConfig(db, &cfg); err != nil {
		return err
	}
	return sm.markLegacyConfigMigrated(db)
}

func (sm *StorageManager) hasSetting(db *sql.DB, key string) (bool, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (sm *StorageManager) markLegacyConfigMigrated(db *sql.DB) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO settings (key, value) VALUES ('legacy_config_migrated', '1')`)
	return err
}

func (sm *StorageManager) saveConfig(db *sql.DB, cfg *Config) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM repositories`); err != nil {
		return err
	}
	for _, repo := range cfg.Repositories {
		notify := any(nil)
		if repo.Notify != nil {
			notify = boolToInt(*repo.Notify)
		}
		if _, err := tx.Exec(`
			INSERT INTO repositories (path, name, default_branch, added_at, last_sync, paused, notify)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, repo.Path, repo.Name, repo.DefaultBranch, formatDBTime(repo.AddedAt), formatDBTime(repo.LastSync), boolToInt(repo.Paused), notify); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`
		INSERT INTO settings (key, value) VALUES ('pull_interval_minutes', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, strconv.Itoa(cfg.PullIntervalMinutes)); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO settings (key, value) VALUES ('history_retention_days', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, strconv.Itoa(cfg.HistoryRetentionDays)); err != nil {
		return err
	}
	return tx.Commit()
}

func (sm *StorageManager) loadConfig(db *sql.DB) (*Config, error) {
	if err := sm.ensureDefaults(db); err != nil {
		return nil, err
	}
	repos, err := sm.loadRepos(db)
	if err != nil {
		return nil, err
	}
	pullInterval, err := sm.intSetting(db, "pull_interval_minutes", DefaultPullIntervalMinutes)
	if err != nil {
		return nil, err
	}
	retention, err := sm.intSetting(db, "history_retention_days", DefaultHistoryRetentionDays)
	if err != nil {
		return nil, err
	}
	return &Config{Repositories: repos, PullIntervalMinutes: pullInterval, HistoryRetentionDays: retention}, nil
}

func (sm *StorageManager) loadRepos(db *sql.DB) ([]RepoInfo, error) {
	rows, err := db.Query(`
		SELECT path, name, default_branch, added_at, last_sync, paused, notify
		FROM repositories
		ORDER BY lower(name), path
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []RepoInfo
	for rows.Next() {
		repo, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, *repo)
	}
	if repos == nil {
		repos = []RepoInfo{}
	}
	return repos, rows.Err()
}

func (sm *StorageManager) intSetting(db *sql.DB, key string, fallback int) (int, error) {
	var value string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return fallback, nil
		}
		return 0, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback, nil
	}
	return parsed, nil
}

type repoScanner interface {
	Scan(dest ...any) error
}

func scanRepo(scanner repoScanner) (*RepoInfo, error) {
	var repo RepoInfo
	var addedAt, lastSync string
	var paused int
	var notify sql.NullInt64
	if err := scanner.Scan(&repo.Path, &repo.Name, &repo.DefaultBranch, &addedAt, &lastSync, &paused, &notify); err != nil {
		return nil, err
	}
	repo.AddedAt = parseDBTime(addedAt)
	repo.LastSync = parseDBTime(lastSync)
	repo.Paused = paused != 0
	if notify.Valid {
		notifyValue := notify.Int64 != 0
		repo.Notify = &notifyValue
	}
	return &repo, nil
}

func requireAffected(res sql.Result, path string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("repository not found: %s", path)
	}
	return nil
}

func defaultConfig() Config {
	return Config{Repositories: []RepoInfo{}, PullIntervalMinutes: DefaultPullIntervalMinutes, HistoryRetentionDays: DefaultHistoryRetentionDays}
}

func cloneConfig(cfg *Config) *Config {
	if cfg == nil {
		defaultCfg := defaultConfig()
		return &defaultCfg
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

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func formatDBTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseDBTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return t
	}
	return time.Time{}
}

func boolPtr(v bool) *bool {
	return &v
}
