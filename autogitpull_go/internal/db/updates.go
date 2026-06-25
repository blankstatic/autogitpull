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
	SkipReason string
	Changed    bool
	StartedAt  time.Time
	FinishedAt time.Time
}

type UpdateFilter struct {
	ChangedOnly bool
	Status      string
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
	skipReason := ""
	if pullErr != nil {
		status = "error"
		errText = pullErr.Error()
		skipReason = SkipReasonFromPullError(errText)
		if skipReason != "" {
			status = "skipped"
		}
	}

	changed := status == "success" && IsChangedPullResult(result)
	_, err := s.db.Exec(`
		UPDATE updates
		SET status = ?, result = ?, error = ?, skip_reason = ?, changed = ?, finished_at = ?
		WHERE id = ?
	`, status, result, errText, skipReason, changed, time.Now().UTC(), id)
	return err
}

func (s *Store) GetUpdate(id int64) (Update, error) {
	row := s.db.QueryRow(`
		SELECT id, repo_path, repo_name, status, result, error, skip_reason, changed, started_at, finished_at
		FROM updates
		WHERE id = ?
	`, id)
	var update Update
	var finishedAt sql.NullTime
	if err := row.Scan(&update.ID, &update.RepoPath, &update.RepoName, &update.Status, &update.Result, &update.Error, &update.SkipReason, &update.Changed, &update.StartedAt, &finishedAt); err != nil {
		return Update{}, err
	}
	if finishedAt.Valid {
		update.FinishedAt = finishedAt.Time
	}
	return update, nil
}

func (s *Store) RecentUpdates(limit int) ([]Update, error) {
	return s.RecentUpdatesPage(limit, 0)
}

func (s *Store) RecentUpdatesPage(limit, offset int) ([]Update, error) {
	return s.RecentUpdatesPageFiltered(limit, offset, UpdateFilter{})
}

func (s *Store) RecentUpdatesPageFiltered(limit, offset int, filter UpdateFilter) ([]Update, error) {
	where, args := updateFilterWhere(filter)
	args = append(args, limit, offset)
	rows, err := s.db.Query(`
		SELECT id, repo_path, repo_name, status, result, error, skip_reason, changed, started_at, finished_at
		FROM updates
		`+where+`
		ORDER BY id DESC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanUpdates(rows)
}

func (s *Store) ChangedUpdateTimesSince(since time.Time) ([]time.Time, error) {
	rows, err := s.db.Query(`
		SELECT started_at
		FROM updates
		WHERE changed = 1 AND started_at >= ?
		ORDER BY started_at ASC
	`, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanTimes(rows)
}

func (s *Store) RepoUpdates(repoPath string, limit int) ([]Update, error) {
	return s.RepoUpdatesPage(repoPath, limit, 0)
}

func (s *Store) RepoUpdatesPage(repoPath string, limit, offset int) ([]Update, error) {
	return s.RepoUpdatesPageFiltered(repoPath, limit, offset, UpdateFilter{})
}

func (s *Store) RepoUpdatesPageFiltered(repoPath string, limit, offset int, filter UpdateFilter) ([]Update, error) {
	where, args := updateFilterWhere(filter)
	if where == "" {
		where = "WHERE repo_path = ?"
	} else {
		where += " AND repo_path = ?"
	}
	args = append(args, repoPath, limit, offset)
	rows, err := s.db.Query(`
		SELECT id, repo_path, repo_name, status, result, error, skip_reason, changed, started_at, finished_at
		FROM updates
		`+where+`
		ORDER BY id DESC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanUpdates(rows)
}

func (s *Store) RepoChangedUpdateTimesSince(repoPath string, since time.Time) ([]time.Time, error) {
	rows, err := s.db.Query(`
		SELECT started_at
		FROM updates
		WHERE repo_path = ? AND changed = 1 AND started_at >= ?
		ORDER BY started_at ASC
	`, repoPath, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanTimes(rows)
}

func (s *Store) LatestUpdatesByRepo() (map[string]Update, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.repo_path, u.repo_name, u.status, u.result, u.error, u.skip_reason, u.changed, u.started_at, u.finished_at
		FROM updates u
		INNER JOIN (
			SELECT repo_path, MAX(id) AS id
			FROM updates
			GROUP BY repo_path
		) latest ON latest.id = u.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	updates, err := scanUpdates(rows)
	if err != nil {
		return nil, err
	}
	byRepo := make(map[string]Update, len(updates))
	for _, update := range updates {
		byRepo[update.RepoPath] = update
	}
	return byRepo, nil
}

func (s *Store) DeleteUpdatesBefore(before time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM updates WHERE started_at < ?`, before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) CountUpdates() (int, error) {
	return s.CountUpdatesFiltered(UpdateFilter{})
}

func (s *Store) CountUpdatesFiltered(filter UpdateFilter) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM updates`
	where, args := updateFilterWhere(filter)
	query += " " + where
	err := s.db.QueryRow(query, args...).Scan(&count)
	return count, err
}

func (s *Store) CountRepoUpdates(repoPath string) (int, error) {
	return s.CountRepoUpdatesFiltered(repoPath, UpdateFilter{})
}

func (s *Store) CountRepoUpdatesFiltered(repoPath string, filter UpdateFilter) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM updates WHERE repo_path = ?`
	args := []any{repoPath}
	if filter.ChangedOnly {
		query += ` AND changed = 1`
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	err := s.db.QueryRow(query, args...).Scan(&count)
	return count, err
}

func updateFilterWhere(filter UpdateFilter) (string, []any) {
	var conditions []string
	var args []any
	if filter.ChangedOnly {
		conditions = append(conditions, "changed = 1")
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, filter.Status)
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(conditions, " AND "), args
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
			skip_reason TEXT NOT NULL DEFAULT '',
			changed INTEGER NOT NULL DEFAULT 0,
			started_at TIMESTAMP NOT NULL,
			finished_at TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_updates_repo_path_id ON updates(repo_path, id DESC);
		CREATE INDEX IF NOT EXISTS idx_updates_id ON updates(id DESC);
		CREATE INDEX IF NOT EXISTS idx_updates_changed_started_at ON updates(changed, started_at);
	`)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE updates ADD COLUMN skip_reason TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return err
	}
	_, err = s.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_updates_repo_changed_started_at ON updates(repo_path, changed, started_at);
	`)
	return err
}

func scanUpdates(rows *sql.Rows) ([]Update, error) {
	var updates []Update
	for rows.Next() {
		var u Update
		var finishedAt sql.NullTime
		if err := rows.Scan(&u.ID, &u.RepoPath, &u.RepoName, &u.Status, &u.Result, &u.Error, &u.SkipReason, &u.Changed, &u.StartedAt, &finishedAt); err != nil {
			return nil, err
		}
		if finishedAt.Valid {
			u.FinishedAt = finishedAt.Time
		}
		updates = append(updates, u)
	}
	return updates, rows.Err()
}

func scanTimes(rows *sql.Rows) ([]time.Time, error) {
	var times []time.Time
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		times = append(times, t)
	}
	return times, rows.Err()
}

func isUpToDate(result string) bool {
	return result == "Already up to date.\n" || result == "Already up-to-date.\n" || result == "Already up to date." || result == "Already up-to-date."
}

func IsSkippedPullError(errText string) bool {
	return SkipReasonFromPullError(errText) != ""
}

func IsChangedPullResult(result string) bool {
	return strings.TrimSpace(result) != "" && !isUpToDate(result)
}

func SkipReasonFromPullError(errText string) string {
	errText = strings.ToLower(strings.TrimSpace(errText))
	switch {
	case errText == "repository has changes", errText == "repository has uncommitted changes":
		return "dirty_worktree"
	case strings.HasPrefix(errText, "current branch ") && strings.Contains(errText, " is not default branch "):
		return "not_default_branch"
	case errText == "repository paused":
		return "paused"
	default:
		return ""
	}
}
