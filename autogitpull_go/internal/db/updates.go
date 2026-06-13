package db

import (
	"database/sql"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

type Update struct {
	ID         int64
	RepoPath   string
	RepoName   string
	Status     string
	Result     string
	Error      string
	Changed    bool
	StartedAt  time.Time
	FinishedAt time.Time
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, err
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) BeginUpdate(repoPath, repoName string) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO updates (repo_path, repo_name, status, started_at)
		VALUES (?, ?, 'running', ?)
	`, repoPath, repoName, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishUpdate(id int64, result string, pullErr error) error {
	status := "success"
	errText := ""
	if pullErr != nil {
		status = "error"
		errText = pullErr.Error()
		if IsSkippedPullError(errText) {
			status = "skipped"
		}
	}

	changed := status == "success" && result != "" && !isUpToDate(result)
	_, err := s.db.Exec(`
		UPDATE updates
		SET status = ?, result = ?, error = ?, changed = ?, finished_at = ?
		WHERE id = ?
	`, status, result, errText, changed, time.Now().UTC(), id)
	return err
}

func (s *Store) RecentUpdates(limit int) ([]Update, error) {
	return s.RecentUpdatesPage(limit, 0)
}

func (s *Store) RecentUpdatesPage(limit, offset int) ([]Update, error) {
	rows, err := s.db.Query(`
		SELECT id, repo_path, repo_name, status, result, error, changed, started_at, finished_at
		FROM updates
		ORDER BY id DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanUpdates(rows)
}

func (s *Store) UpdatesSince(since time.Time) ([]Update, error) {
	rows, err := s.db.Query(`
		SELECT id, repo_path, repo_name, status, result, error, changed, started_at, finished_at
		FROM updates
		WHERE started_at >= ?
		ORDER BY started_at ASC
	`, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanUpdates(rows)
}

func (s *Store) RepoUpdates(repoPath string, limit int) ([]Update, error) {
	return s.RepoUpdatesPage(repoPath, limit, 0)
}

func (s *Store) RepoUpdatesPage(repoPath string, limit, offset int) ([]Update, error) {
	rows, err := s.db.Query(`
		SELECT id, repo_path, repo_name, status, result, error, changed, started_at, finished_at
		FROM updates
		WHERE repo_path = ?
		ORDER BY id DESC
		LIMIT ? OFFSET ?
	`, repoPath, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanUpdates(rows)
}

func (s *Store) RepoUpdatesSince(repoPath string, since time.Time) ([]Update, error) {
	rows, err := s.db.Query(`
		SELECT id, repo_path, repo_name, status, result, error, changed, started_at, finished_at
		FROM updates
		WHERE repo_path = ? AND started_at >= ?
		ORDER BY started_at ASC
	`, repoPath, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanUpdates(rows)
}

func (s *Store) CountUpdates() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM updates`).Scan(&count)
	return count, err
}

func (s *Store) CountRepoUpdates(repoPath string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM updates WHERE repo_path = ?`, repoPath).Scan(&count)
	return count, err
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS updates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_path TEXT NOT NULL,
			repo_name TEXT NOT NULL,
			status TEXT NOT NULL,
			result TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			changed INTEGER NOT NULL DEFAULT 0,
			started_at TIMESTAMP NOT NULL,
			finished_at TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_updates_repo_path_id ON updates(repo_path, id DESC);
		CREATE INDEX IF NOT EXISTS idx_updates_id ON updates(id DESC);
	`)
	return err
}

func scanUpdates(rows *sql.Rows) ([]Update, error) {
	var updates []Update
	for rows.Next() {
		var u Update
		var finishedAt sql.NullTime
		if err := rows.Scan(&u.ID, &u.RepoPath, &u.RepoName, &u.Status, &u.Result, &u.Error, &u.Changed, &u.StartedAt, &finishedAt); err != nil {
			return nil, err
		}
		if finishedAt.Valid {
			u.FinishedAt = finishedAt.Time
		}
		updates = append(updates, u)
	}
	return updates, rows.Err()
}

func isUpToDate(result string) bool {
	return result == "Already up to date.\n" || result == "Already up-to-date.\n" || result == "Already up to date." || result == "Already up-to-date."
}

func IsSkippedPullError(errText string) bool {
	errText = strings.ToLower(strings.TrimSpace(errText))
	return errText == "repository has changes" || errText == "repository has uncommitted changes"
}
