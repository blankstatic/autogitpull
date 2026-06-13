package db

import (
	"errors"
	"path/filepath"
	"testing"
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
