package web

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
)

func TestNewActivitySummaryCountsOnlyProvidedChangedTimes(t *testing.T) {
	loc := time.FixedZone("MSK", 3*60*60)
	start := time.Date(2026, 6, 7, 0, 0, 0, 0, loc)
	end := time.Date(2026, 6, 13, 0, 0, 0, 0, loc)

	summary := newActivitySummary([]time.Time{
		time.Date(2026, 6, 8, 10, 0, 0, 0, loc),
		time.Date(2026, 6, 8, 12, 0, 0, 0, loc),
		time.Date(2026, 6, 13, 23, 59, 59, 0, loc),
		time.Date(2026, 6, 14, 0, 0, 0, 0, loc),
	}, start, end, loc)

	if summary.Total != 3 {
		t.Fatalf("expected 3 changed updates, got %d", summary.Total)
	}
	if len(summary.Cells) != 7 {
		t.Fatalf("expected 7 cells, got %d", len(summary.Cells))
	}
	if summary.Cells[1].Count != 2 || summary.Cells[1].Level != 2 {
		t.Fatalf("unexpected June 8 cell: %+v", summary.Cells[1])
	}
	if summary.Cells[6].Count != 1 || summary.Cells[6].Level != 1 {
		t.Fatalf("unexpected June 13 cell: %+v", summary.Cells[6])
	}
}

func TestActivityStartUsesMonday(t *testing.T) {
	loc := time.FixedZone("MSK", 3*60*60)
	startFromMonday := activityStart(time.Date(2026, 6, 22, 0, 0, 0, 0, loc))
	startFromSunday := activityStart(time.Date(2026, 6, 28, 0, 0, 0, 0, loc))
	want := time.Date(2025, 6, 23, 0, 0, 0, 0, loc)

	if !startFromMonday.Equal(want) {
		t.Fatalf("expected Monday start %s, got %s", want, startFromMonday)
	}
	if !startFromSunday.Equal(want) {
		t.Fatalf("expected Sunday to use previous Monday start %s, got %s", want, startFromSunday)
	}
	if startFromMonday.Weekday() != time.Monday || startFromSunday.Weekday() != time.Monday {
		t.Fatalf("expected activity starts to be Mondays: %s, %s", startFromMonday.Weekday(), startFromSunday.Weekday())
	}
}

func TestEventFilterDefaultsToChanges(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)

	if got := eventFilterFromRequest(req); got != eventFilterChanges {
		t.Fatalf("expected default filter %q, got %q", eventFilterChanges, got)
	}
}

func TestIndexReadsReposAddedToDatabaseByAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	storage := config.NewStorageManager(configPath)
	if err := storage.Load(); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(dir, "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	externalDB, err := sql.Open("sqlite3", filepath.Join(dir, config.UpdatesDBFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer externalDB.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := externalDB.Exec(`
		INSERT INTO repositories (path, name, default_branch, added_at, last_sync, paused, notify)
		VALUES (?, ?, ?, ?, ?, 0, NULL)
	`, "/repo/from-cli", "from-cli", "main", now, now); err != nil {
		t.Fatal(err)
	}

	server := New(store, storage)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	server.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "from-cli") {
		t.Fatalf("expected externally added repo in index response")
	}
	for _, unwanted := range []string{"Service", "Database", `id="daemon"`} {
		if strings.Contains(rec.Body.String(), unwanted) {
			t.Fatalf("expected %q to live off the main dashboard", unwanted)
		}
	}
	if !strings.Contains(rec.Body.String(), "Configure plugins") {
		t.Fatalf("expected plugin summary on main dashboard")
	}
}

func TestStatusPageRendersServiceDatabaseAndDaemon(t *testing.T) {
	dir := t.TempDir()
	storage := config.NewStorageManager(filepath.Join(dir, "config.json"))
	if err := storage.Load(); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(dir, "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server := New(store, storage)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/status", nil)
	server.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Service", "Database", "Plugins", "Daemon"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected status page to contain %q", want)
		}
	}
}

func TestPluginsPageRendersBuiltInPlugins(t *testing.T) {
	dir := t.TempDir()
	storage := config.NewStorageManager(filepath.Join(dir, "config.json"))
	if err := storage.Load(); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(dir, "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server := New(store, storage)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/plugins", nil)
	server.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Notifications") {
		t.Fatalf("expected notifications plugin in response")
	}
}

func TestSettingsPageRendersDaemonSettings(t *testing.T) {
	dir := t.TempDir()
	storage := config.NewStorageManager(filepath.Join(dir, "config.json"))
	if err := storage.Load(); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(dir, "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server := New(store, storage)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings", nil)
	server.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `name="pull_interval_minutes"`) || !strings.Contains(rec.Body.String(), `name="history_retention_days"`) {
		t.Fatalf("expected settings form in response")
	}
}

func TestSettingsPostSavesDaemonSettings(t *testing.T) {
	dir := t.TempDir()
	storage := config.NewStorageManager(filepath.Join(dir, "config.json"))
	if err := storage.Load(); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(dir, "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server := New(store, storage)
	form := url.Values{
		"pull_interval_minutes":  {"11"},
		"history_retention_days": {"22"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); !strings.HasPrefix(location, "/settings?") {
		t.Fatalf("expected redirect to settings, got %q", location)
	}
	cfg := storage.GetConfig()
	if cfg.PullIntervalMinutes != 11 || cfg.HistoryRetentionDays != 22 {
		t.Fatalf("unexpected saved settings: %+v", cfg)
	}
}

func TestNewStoresDefaultNotificationPluginEnabled(t *testing.T) {
	dir := t.TempDir()
	storage := config.NewStorageManager(filepath.Join(dir, "config.json"))
	if err := storage.Load(); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(dir, "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_ = New(store, storage)

	state := storage.GetPluginStates()["notifications"]
	if state.ID != "notifications" || !state.Enabled {
		t.Fatalf("expected enabled notifications default, got %+v", state)
	}
	if state.Config["title_prefix"] != "Pulled" {
		t.Fatalf("expected default title prefix, got %+v", state.Config)
	}
}

func TestSavePluginUpdatesPluginState(t *testing.T) {
	dir := t.TempDir()
	storage := config.NewStorageManager(filepath.Join(dir, "config.json"))
	if err := storage.Load(); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(dir, "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server := New(store, storage)
	form := url.Values{
		"id":                  {"notifications"},
		"enabled":             {"1"},
		"config_title_prefix": {"Changed"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/plugins/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	state := storage.GetPluginStates()["notifications"]
	if !state.Enabled || state.Config["title_prefix"] != "Changed" {
		t.Fatalf("unexpected plugin state: %+v", state)
	}
}

func TestEventFilterURLs(t *testing.T) {
	filter := newEventFilter("/repo", url.Values{"path": []string{"/repo/a"}}, eventFilterAll)

	options := map[string]eventFilterOption{}
	for _, option := range filter.Options {
		options[option.Label] = option
	}

	if options["Changes"].URL != "/repo?path=/repo/a#updates" {
		t.Fatalf("unexpected changes url: %q", options["Changes"].URL)
	}
	if options["Error"].URL != "/repo?filter=error&path=/repo/a#updates" {
		t.Fatalf("unexpected error url: %q", options["Error"].URL)
	}
	if options["All"].URL != "/repo?filter=all&path=/repo/a#updates" {
		t.Fatalf("unexpected all url: %q", options["All"].URL)
	}
	if options["All"].Class != "filter-link active" || options["Changes"].Class != "filter-link" {
		t.Fatalf("unexpected filter options: %+v", options)
	}
}

func TestHumanizeNumber(t *testing.T) {
	tests := map[int]string{
		999:     "999",
		1000:    "1k",
		1500:    "1.5k",
		100000:  "100k",
		1250000: "1.2m",
		-1200:   "-1.2k",
	}
	for input, want := range tests {
		if got := humanizeNumber(input); got != want {
			t.Fatalf("humanizeNumber(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestRepoURLCanIncludeFilter(t *testing.T) {
	if got := repoURL("/repo/a", eventFilterAll); got != "/repo?filter=all&path=/repo/a" {
		t.Fatalf("unexpected repo url: %q", got)
	}
}

func TestFlashFromRequestUsesStatusClass(t *testing.T) {
	req := httptest.NewRequest("GET", "/?flash=Pull+failed&flash_type=error", nil)

	flash := flashFromRequest(req)
	if flash.Text != "Pull failed" || flash.Class != "error" {
		t.Fatalf("unexpected flash: %+v", flash)
	}
}

func TestPullFlashUsesSkippedForDirtyWorktree(t *testing.T) {
	err := errors.New("repository has uncommitted changes")

	if got := pullFlashText(err); got != "Pull skipped: repository has uncommitted changes" {
		t.Fatalf("unexpected flash text: %q", got)
	}
	if got := pullFlashType(err); got != "skipped" {
		t.Fatalf("unexpected flash type: %q", got)
	}
}

func TestRepoCardsAttachLatestUpdate(t *testing.T) {
	repos := []config.RepoInfo{{Path: "/repo/a", Name: "a"}, {Path: "/repo/b", Name: "b"}}
	latest := map[string]db.Update{"/repo/a": {RepoPath: "/repo/a", Status: "error"}}

	cards := repoCards(repos, latest)
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(cards))
	}
	if cards[0].LastUpdate == nil || cards[0].LastUpdate.Status != "error" {
		t.Fatalf("expected latest update on first card: %+v", cards[0])
	}
	if cards[1].LastUpdate != nil {
		t.Fatalf("expected no latest update on second card: %+v", cards[1])
	}
}
