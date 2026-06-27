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
	if err := store.FinishUpdateWithRevisions(id, "pulled changes", nil, "abc", "def"); err != nil {
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
	if updates[0].BeforeRev != "abc" || updates[0].AfterRev != "def" {
		t.Fatalf("unexpected update revisions: %+v", updates[0])
	}
}

func TestIsChangedPullResult(t *testing.T) {
	if !IsChangedPullResult("Updating abc..def\nFast-forward") {
		t.Fatal("expected pull result with output to be changed")
	}
	if IsChangedPullResult("Already up to date.") {
		t.Fatal("expected up-to-date result not to be changed")
	}
	if IsChangedPullResult("   ") {
		t.Fatal("expected blank result not to be changed")
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
	if updates[0].Status != "skipped" || updates[0].Error != "repository has uncommitted changes" || updates[0].SkipReason != "dirty_worktree" || updates[0].Changed {
		t.Fatalf("unexpected update: %+v", updates[0])
	}
}

func TestStoreRecordsSkippedDefaultBranchReason(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	id, err := store.BeginUpdate("/repo/path", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishUpdate(id, "", errors.New("current branch feature is not default branch main")); err != nil {
		t.Fatal(err)
	}

	updates, err := store.RepoUpdates("/repo/path", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].Status != "skipped" || updates[0].SkipReason != "not_default_branch" {
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

func TestFilteredUpdatePagesAndCounts(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	insertUpdate(t, store, "/repo/a", "a", true, now)
	insertUpdateWithStatus(t, store, "/repo/a", "a", "error", false, now.Add(time.Second))
	insertUpdate(t, store, "/repo/b", "b", true, now.Add(2*time.Second))

	total, err := store.CountUpdatesFiltered(UpdateFilter{ChangedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("expected 2 changed updates, got %d", total)
	}

	updates, err := store.RecentUpdatesPageFiltered(10, 0, UpdateFilter{ChangedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 || !updates[0].Changed || !updates[1].Changed {
		t.Fatalf("unexpected filtered updates: %+v", updates)
	}

	repoTotal, err := store.CountRepoUpdatesFiltered("/repo/a", UpdateFilter{ChangedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if repoTotal != 1 {
		t.Fatalf("expected 1 changed repo update, got %d", repoTotal)
	}

	repoUpdates, err := store.RepoUpdatesPageFiltered("/repo/a", 10, 0, UpdateFilter{ChangedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(repoUpdates) != 1 || !repoUpdates[0].Changed || repoUpdates[0].RepoPath != "/repo/a" {
		t.Fatalf("unexpected filtered repo updates: %+v", repoUpdates)
	}

	errorUpdates, err := store.RecentUpdatesPageFiltered(10, 0, UpdateFilter{Status: "error"})
	if err != nil {
		t.Fatal(err)
	}
	if len(errorUpdates) != 1 || errorUpdates[0].Status != "error" {
		t.Fatalf("unexpected status filtered updates: %+v", errorUpdates)
	}
}

func TestLatestUpdatesByRepoAndDeleteBefore(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	old := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	recent := old.Add(24 * time.Hour)
	insertUpdateWithStatus(t, store, "/repo/a", "a", "success", false, old)
	insertUpdateWithStatus(t, store, "/repo/a", "a", "error", false, recent)
	insertUpdateWithStatus(t, store, "/repo/b", "b", "skipped", false, recent)

	latest, err := store.LatestUpdatesByRepo()
	if err != nil {
		t.Fatal(err)
	}
	if latest["/repo/a"].Status != "error" || latest["/repo/b"].Status != "skipped" {
		t.Fatalf("unexpected latest updates: %+v", latest)
	}

	deleted, err := store.DeleteUpdatesBefore(recent)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted row, got %d", deleted)
	}
	total, err := store.CountUpdates()
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("expected 2 remaining rows, got %d", total)
	}
}

func TestPluginResultRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	id, err := store.BeginUpdate("/repo/path", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SavePluginResult(id, "ai_summary", "pending", "context", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePluginResult(id, "ai_summary", "success", "summary", ""); err != nil {
		t.Fatal(err)
	}

	results, err := store.PluginResultsByUpdate(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].PluginID != "ai_summary" || results[0].Status != "success" || results[0].Result != "summary" {
		t.Fatalf("unexpected plugin results: %+v", results)
	}
}

func insertUpdate(t *testing.T, store *Store, repoPath, repoName string, changed bool, startedAt time.Time) {
	t.Helper()
	insertUpdateWithStatus(t, store, repoPath, repoName, "success", changed, startedAt)
}

func insertUpdateWithStatus(t *testing.T, store *Store, repoPath, repoName, status string, changed bool, startedAt time.Time) {
	t.Helper()
	_, err := store.db.Exec(`
		INSERT INTO updates (repo_path, repo_name, status, result, changed, started_at, finished_at)
		VALUES (?, ?, ?, 'test', ?, ?, ?)
	`, repoPath, repoName, status, changed, startedAt, startedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
}
