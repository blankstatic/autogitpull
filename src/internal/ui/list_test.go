package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blankstatic/autogitpull/src/internal/config"
	"github.com/charmbracelet/lipgloss"
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

func TestTableRowsCompactHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	m := model{repos: []config.RepoInfo{{
		Name:          "repo",
		Path:          filepath.Join(home, "projects", "repo"),
		DefaultBranch: "main",
	}}}

	if got := m.tableRows()[0][3]; got != filepath.Join("~", "projects", "repo") {
		t.Fatalf("expected compact home path, got %q", got)
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
		if status != "Checking..." {
			t.Fatalf("expected status %d to be Checking..., got %q", i, status)
		}
	}
	if len(m.initialRepos) != len(repos) {
		t.Fatalf("expected %d initial repos, got %d", len(repos), len(m.initialRepos))
	}
}

func TestStatusTextForChanges(t *testing.T) {
	tests := []struct {
		name       string
		hasChanges bool
		err        error
		want       string
	}{
		{name: "clean", want: "Ready"},
		{name: "dirty", hasChanges: true, want: "Has uncommitted changes"},
		{name: "error", err: errors.New("git failed"), want: "FAIL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := statusTextForChanges(tt.hasChanges, tt.err)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
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

func TestNarrowTableKeepsStatusColumnVisible(t *testing.T) {
	m := newListModel(nil, []config.RepoInfo{{
		Name:          "autogitpull",
		Path:          "/Users/example/projects/autogitpull",
		DefaultBranch: "main",
	}})
	m.statuses[0] = readyStatusText
	m.table = m.createTable()
	m.table = updateTableWidth(m.table, 80)

	columns := m.table.Columns()
	if columns[4].Width < len("STATUS") {
		t.Fatalf("expected status column to fit its header, got width %d", columns[4].Width)
	}
	view := m.table.View()
	if !strings.Contains(view, readyStatusText) {
		t.Fatalf("expected narrow table to show %q:\n%s", readyStatusText, view)
	}
	tableLimit := m.windowWidth - baseStyle.GetHorizontalFrameSize() - terminalRightMargin
	for lineNumber, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > tableLimit {
			t.Fatalf("table line %d exceeds content width: got %d, want <= %d", lineNumber+1, width, tableLimit)
		}
	}
	for lineNumber, line := range strings.Split(m.View(), "\n") {
		if width := lipgloss.Width(line); width >= m.windowWidth {
			t.Fatalf("line %d reaches terminal wrap column: got %d, want < %d", lineNumber+1, width, m.windowWidth)
		}
	}
}

func TestTableNeverExceedsWindowWidth(t *testing.T) {
	for width := 30; width <= 140; width++ {
		m := newListModel(nil, []config.RepoInfo{{
			Name:          "autogitpull",
			Path:          "/Users/example/projects/autogitpull",
			DefaultBranch: "main",
		}})
		m.windowWidth = width
		m.table = m.createTable()

		for lineNumber, line := range strings.Split(m.View(), "\n") {
			if lineWidth := lipgloss.Width(line); lineWidth >= width {
				t.Fatalf("window width %d: line %d has width %d", width, lineNumber+1, lineWidth)
			}
		}
	}
}

func TestLongStatusStaysOnRepositoryRow(t *testing.T) {
	m := newListModel(nil, []config.RepoInfo{{
		Name:          "semantic-embedding-storage",
		Path:          "/Users/example/projects/semantic-embedding-storage",
		DefaultBranch: "main",
	}})
	m.windowWidth = 128
	m.statuses[0] = dirtyStatusText
	m.table = m.createTable()

	foundStatus := false
	for _, line := range strings.Split(m.table.View(), "\n") {
		if strings.Contains(line, "Has") {
			foundStatus = true
			if !strings.Contains(line, "semantic-") {
				t.Fatalf("status wrapped away from its repository row:\n%s", m.table.View())
			}
		}
	}
	if !foundStatus {
		t.Fatalf("expected dirty status in table:\n%s", m.table.View())
	}
}
