package ui

import (
	"testing"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
)

func TestTableRowsPreserveFallbackText(t *testing.T) {
	m := model{
		repos: []config.RepoInfo{{
			Name:          "repo",
			Path:          "/repo",
			DefaultBranch: "main",
		}},
	}

	rows := m.tableRows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0][1] != "..." {
		t.Fatalf("expected loading branch text, got %q", rows[0][1])
	}
	if rows[0][4] != "Ready" {
		t.Fatalf("expected ready status text, got %q", rows[0][4])
	}
}

func TestNewListModelInitializesParallelSlices(t *testing.T) {
	repos := []config.RepoInfo{
		{Name: "a", Path: "/a", DefaultBranch: "main"},
		{Name: "b", Path: "/b", DefaultBranch: "main"},
	}

	m := newListModel(nil, repos)

	if len(m.branches) != len(repos) {
		t.Fatalf("expected %d branches, got %d", len(repos), len(m.branches))
	}
	if len(m.statuses) != len(repos) {
		t.Fatalf("expected %d statuses, got %d", len(repos), len(m.statuses))
	}
	for i, status := range m.statuses {
		if status != "Ready" {
			t.Fatalf("expected status %d to be Ready, got %q", i, status)
		}
	}
	if len(m.initialRepos) != len(repos) {
		t.Fatalf("expected %d initial repos, got %d", len(repos), len(m.initialRepos))
	}
}

func TestTableColumnsPreserveOrder(t *testing.T) {
	columns := tableColumns()
	titles := []string{"NAME", "CURRENT", "BRANCH", "PATH", "STATUS"}

	if len(columns) != len(titles) {
		t.Fatalf("expected %d columns, got %d", len(titles), len(columns))
	}
	for i, title := range titles {
		if columns[i].Title != title {
			t.Fatalf("expected column %d title %q, got %q", i, title, columns[i].Title)
		}
	}
}
