package web

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/plugins"
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
	if err := storage.SetPluginState(config.PluginState{
		ID:      plugins.NotificationsID,
		Enabled: true,
		Config: map[string]string{
			"title_prefix":                 "Pulled",
			plugins.RepoScopeConfigKey:     "selected",
			plugins.SelectedReposConfigKey: `["/Users/dmitry/proj/autogitpull_source"]`,
		},
	}); err != nil {
		t.Fatal(err)
	}

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
	if !strings.Contains(rec.Body.String(), `action="/plugins/test-ai-summary"`) || !strings.Contains(rec.Body.String(), ">Test</button>") {
		t.Fatalf("expected AI summary test button in response")
	}
	if !strings.Contains(rec.Body.String(), "plugin-repo-path") || !strings.Contains(rec.Body.String(), `title="/Users/dmitry/proj/autogitpull_source"`) {
		t.Fatalf("expected selected repo path to render in wrapping container")
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
		"config_repo_scope":   {"global"},
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
	if state.Config[plugins.RepoScopeConfigKey] != "global" {
		t.Fatalf("expected global repo scope, got %+v", state.Config)
	}
}

func TestSavePluginCanRestoreNotificationsGlobalScope(t *testing.T) {
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

	if err := storage.SetPluginState(config.PluginState{
		ID:      plugins.NotificationsID,
		Enabled: true,
		Config: map[string]string{
			"title_prefix":                 "Pulled",
			plugins.RepoScopeConfigKey:     "selected",
			plugins.SelectedReposConfigKey: `["/repo/a"]`,
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := New(store, storage)
	form := url.Values{
		"id":                  {plugins.NotificationsID},
		"enabled":             {"1"},
		"config_title_prefix": {"Pulled"},
		"config_repo_scope":   {"global"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/plugins/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	state := storage.GetPluginStates()[plugins.NotificationsID]
	if state.Config[plugins.RepoScopeConfigKey] != "global" {
		t.Fatalf("expected global scope, got %+v", state.Config)
	}
	if !plugins.EnabledForRepo(storage.GetPluginStates(), plugins.NotificationsID, "/repo/b") {
		t.Fatalf("expected notifications to apply globally after save")
	}
}

func TestSavePluginPreservesHiddenConfig(t *testing.T) {
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

	if err := storage.SetPluginState(config.PluginState{
		ID:      plugins.AISummaryID,
		Enabled: true,
		Config: map[string]string{
			plugins.SelectedReposConfigKey: `["/repo/a"]`,
			"provider":                     "Local proxy",
			"api_type":                     "chat_completions",
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := New(store, storage)
	form := url.Values{
		"id":                {plugins.AISummaryID},
		"enabled":           {"1"},
		"config_provider":   {"Local proxy"},
		"config_api_type":   {"chat_completions"},
		"config_url":        {"http://litellm.local"},
		"config_token":      {"secret"},
		"config_model":      {"gpt-test"},
		"config_repo_scope": {"selected"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/plugins/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	state := storage.GetPluginStates()[plugins.AISummaryID]
	if state.Config[plugins.SelectedReposConfigKey] != `["/repo/a"]` {
		t.Fatalf("expected hidden repo config to be preserved, got %+v", state.Config)
	}
	if state.Config["url"] != "http://litellm.local" {
		t.Fatalf("expected visible config to be updated, got %+v", state.Config)
	}
}

func TestToggleRepoPlugin(t *testing.T) {
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
	seedRepo(t, dir, "/repo/ai", "ai")

	server := New(store, storage)
	form := url.Values{"path": {"/repo/ai"}, "plugin_id": {plugins.AISummaryID}, "enabled": {"1"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/repo/plugin-toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	states := storage.GetPluginStates()
	if !plugins.EnabledForRepo(states, plugins.AISummaryID, "/repo/ai") {
		t.Fatalf("expected AI summary enabled for repo: %+v", states[plugins.AISummaryID])
	}

	form.Set("enabled", "0")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/repo/plugin-toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if plugins.EnabledForRepo(storage.GetPluginStates(), plugins.AISummaryID, "/repo/ai") {
		t.Fatalf("expected AI summary disabled for repo")
	}
}

func TestAISummaryTestEndpoint(t *testing.T) {
	oldClient := plugins.AISummaryHTTPClientForTest()
	defer plugins.SetAISummaryHTTPClientForTest(oldClient)

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output_text":"hello back"}`))
	}))
	defer provider.Close()
	plugins.SetAISummaryHTTPClientForTest(provider.Client())

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
	if err := storage.SetPluginState(config.PluginState{
		ID:      plugins.AISummaryID,
		Enabled: true,
		Config: map[string]string{
			"provider": "OpenAI account",
			"api_type": "responses",
			"url":      provider.URL + "/v1",
			"token":    "test-key",
			"model":    "gpt-test",
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := New(store, storage)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/plugins/test-ai-summary", nil)
	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "AI+summary+test%3A+hello+back") {
		t.Fatalf("expected test response in flash, got %q", location)
	}
}

func TestUpdatePageShowsChangeAndAISummaries(t *testing.T) {
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

	updateID, err := store.BeginUpdate("/repo/change", "change")
	if err != nil {
		t.Fatal(err)
	}
	beforeRev := "118e9c5b1a2572ac41acc8cf9a2c7dd65d0309a7"
	afterRev := "228e9c5b1a2572ac41acc8cf9a2c7dd65d0309a8"
	if err := store.FinishUpdateWithRevisions(updateID, "Fast-forward\n file.go | 2 +", nil, beforeRev, afterRev); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePluginResult(updateID, plugins.AISummaryID, "success", "first summary", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePluginResult(updateID, plugins.AISummaryID, "success", "second summary", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePluginResult(updateID, plugins.NotificationsID, "success", "http://localhost:9009/update?id=1", ""); err != nil {
		t.Fatal(err)
	}

	server := New(store, storage)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/update?id="+strconv.FormatInt(updateID, 10), nil)
	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Fast-forward", "first summary", "second summary", "Generate again", "Plugin results", "notifications"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected update page to contain %q", want)
		}
	}
	if !strings.Contains(body, "118e9c5b1a25") || !strings.Contains(body, `title="118e9c5b1a2572ac41acc8cf9a2c7dd65d0309a7"`) {
		t.Fatalf("expected compact revision hash with full hash title")
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

func seedRepo(t *testing.T, dir, repoPath, repoName string) {
	t.Helper()
	externalDB, err := sql.Open("sqlite3", filepath.Join(dir, config.UpdatesDBFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer externalDB.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := externalDB.Exec(`
		INSERT INTO repositories (path, name, default_branch, added_at, last_sync, paused, notify)
		VALUES (?, ?, ?, ?, ?, 0, NULL)
	`, repoPath, repoName, "main", now, now); err != nil {
		t.Fatal(err)
	}
}
