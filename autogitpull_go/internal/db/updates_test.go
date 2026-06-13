package db

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRecordsUpdate(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	id, err := store.BeginUpdate("/repo/path", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishUpdate(id, "pulled changes", nil); err != nil {
		t.Fatal(err)
	}

	updates, err := store.RecentUpdates(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].RepoName != "repo" || updates[0].Status != "success" || !updates[0].Changed {
		t.Fatalf("unexpected update: %+v", updates[0])
	}
}

func TestStoreRecordsError(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	id, err := store.BeginUpdate("/repo/path", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishUpdate(id, "", errors.New("dirty repo")); err != nil {
		t.Fatal(err)
	}

	updates, err := store.RepoUpdates("/repo/path", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].Status != "error" || updates[0].Error != "dirty repo" || updates[0].Changed {
		t.Fatalf("unexpected update: %+v", updates[0])
	}
}

func TestStoreRecordsSkippedDirtyRepo(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	id, err := store.BeginUpdate("/repo/path", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishUpdate(id, "", errors.New("repository has uncommitted changes")); err != nil {
		t.Fatal(err)
	}

	updates, err := store.RepoUpdates("/repo/path", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].Status != "skipped" || updates[0].Error != "repository has uncommitted changes" || updates[0].Changed {
		t.Fatalf("unexpected update: %+v", updates[0])
	}
}

func TestChangedUpdateTimesSinceFiltersChangedRows(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	oldChanged := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	recentChanged := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	recentUnchanged := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	insertUpdate(t, store, "/repo/a", "a", true, oldChanged)
	insertUpdate(t, store, "/repo/a", "a", true, recentChanged)
	insertUpdate(t, store, "/repo/a", "a", false, recentUnchanged)
	insertUpdate(t, store, "/repo/b", "b", true, recentChanged.Add(time.Hour))

	times, err := store.ChangedUpdateTimesSince(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(times) != 2 {
		t.Fatalf("expected 2 changed times, got %d: %+v", len(times), times)
	}

	repoTimes, err := store.RepoChangedUpdateTimesSince("/repo/a", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(repoTimes) != 1 || !repoTimes[0].Equal(recentChanged) {
		t.Fatalf("unexpected repo changed times: %+v", repoTimes)
	}
}

func insertUpdate(t *testing.T, store *Store, repoPath, repoName string, changed bool, startedAt time.Time) {
	t.Helper()
	_, err := store.db.Exec(`
		INSERT INTO updates (repo_path, repo_name, status, result, changed, started_at, finished_at)
		VALUES (?, ?, 'success', 'test', ?, ?, ?)
	`, repoPath, repoName, changed, startedAt, startedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
}
