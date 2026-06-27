package plugins

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
)

func TestRunAfterChangeSkipsNoChangePlugins(t *testing.T) {
	called := 0
	oldRegistry := registry
	registry = []Definition{{
		ID:        "changed-only",
		Name:      "Changed only",
		DefaultOn: true,
		Run: func(Context) error {
			called++
			return nil
		},
	}}
	defer func() { registry = oldRegistry }()

	RunAfterChange(Context{Update: db.Update{Changed: false}}, map[string]config.PluginState{})

	if called != 0 {
		t.Fatalf("expected changed-only plugin to be skipped, called %d times", called)
	}
}

func TestRunAfterChangeAllowsNoChangePlugins(t *testing.T) {
	called := 0
	oldRegistry := registry
	registry = []Definition{{
		ID:            "manual",
		Name:          "Manual",
		DefaultOn:     true,
		RunOnNoChange: true,
		Run: func(Context) error {
			called++
			return nil
		},
	}}
	defer func() { registry = oldRegistry }()

	RunAfterChange(Context{Update: db.Update{Changed: false}}, map[string]config.PluginState{})

	if called != 1 {
		t.Fatalf("expected no-change plugin to run once, called %d times", called)
	}
}

func TestNotificationsPluginAllowsManualSourcesWithoutChanges(t *testing.T) {
	for _, source := range []string{"web_manual", "tui_manual"} {
		t.Run(source, func(t *testing.T) {
			def := notificationPlugin()
			if !def.RunOnNoChange {
				t.Fatalf("expected notification plugin to opt into no-change runs")
			}
			if def.DefaultConfig["title_prefix"] != "Pulled" {
				t.Fatalf("unexpected default config: %+v", def.DefaultConfig)
			}
		})
	}
}

func TestNotificationsPluginStoresResult(t *testing.T) {
	oldNotify := notifyURL
	done := make(chan string, 1)
	notifyURL = func(_, _, _, openURL string) error {
		done <- openURL
		return nil
	}
	defer func() { notifyURL = oldNotify }()

	store, err := db.Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	updateID, err := store.BeginUpdate("/repo/a", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishUpdate(updateID, "Already up to date.", nil); err != nil {
		t.Fatal(err)
	}
	update, err := store.GetUpdate(updateID)
	if err != nil {
		t.Fatal(err)
	}

	notificationPlugin().Run(Context{
		Repo:    &config.RepoInfo{Path: "/repo/a", Name: "a"},
		Update:  update,
		Store:   store,
		Notify:  true,
		Source:  "web_manual",
		OpenURL: "http://localhost:9009/update?id=1",
	})

	select {
	case got := <-done:
		if got != "http://localhost:9009/update?id=1" {
			t.Fatalf("unexpected notification URL: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("notification was not sent")
	}
	var results []db.PluginResult
	results, err = store.PluginResultsByUpdate(updateID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].PluginID != NotificationsID || results[0].Status != "success" {
		t.Fatalf("unexpected notification result: %+v", results)
	}
}

func TestNotificationsPluginSendsForRemoteRefUpdate(t *testing.T) {
	oldNotify := notifyURL
	done := make(chan string, 1)
	notifyURL = func(_, _, _, openURL string) error {
		done <- openURL
		return nil
	}
	defer func() { notifyURL = oldNotify }()

	store, err := db.Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	updateID, err := store.BeginUpdate("/repo/a", "a")
	if err != nil {
		t.Fatal(err)
	}
	result := "From github.com:blankstatic/autogitpull\n   e81aec2..86bf794  plugins    -> origin/plugins\nAlready up to date."
	if err := store.FinishUpdateWithRevisions(updateID, result, nil, "same", "same"); err != nil {
		t.Fatal(err)
	}
	update, err := store.GetUpdate(updateID)
	if err != nil {
		t.Fatal(err)
	}
	if update.Changed {
		t.Fatal("expected remote-ref-only update not to be marked changed")
	}

	if err := notificationPlugin().Run(Context{
		Repo:    &config.RepoInfo{Path: "/repo/a", Name: "a"},
		Update:  update,
		Store:   store,
		Notify:  true,
		Source:  "daemon",
		OpenURL: "http://localhost:9009/update?id=1",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("notification was not sent")
	}
}

func TestNotificationsPluginStoresMutedReason(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "updates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	updateID, err := store.BeginUpdate("/repo/a", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishUpdate(updateID, "pulled changes", nil); err != nil {
		t.Fatal(err)
	}
	update, err := store.GetUpdate(updateID)
	if err != nil {
		t.Fatal(err)
	}
	muted := false
	if err := notificationPlugin().Run(Context{
		Repo:   &config.RepoInfo{Path: "/repo/a", Name: "a", Notify: &muted},
		Update: update,
		Store:  store,
		Notify: true,
		Source: "daemon",
	}); err != nil {
		t.Fatal(err)
	}
	results, err := store.PluginResultsByUpdate(updateID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Error != "repo notifications muted" {
		t.Fatalf("unexpected muted notification result: %+v", results)
	}
}

func TestPluginViewsAddRepoScopeField(t *testing.T) {
	views := Views(map[string]config.PluginState{})
	var notifications View
	for _, view := range views {
		if view.ID == NotificationsID {
			notifications = view
			break
		}
	}
	if notifications.ID == "" {
		t.Fatal("expected notifications view")
	}
	if notifications.Config[RepoScopeConfigKey] != "global" {
		t.Fatalf("expected global repo scope by default, got %+v", notifications.Config)
	}
	found := false
	for _, field := range notifications.Fields {
		if field.Key == RepoScopeConfigKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected generic repo scope field: %+v", notifications.Fields)
	}
}

func TestRepoScopeCanReturnSelectedPluginToGlobal(t *testing.T) {
	states := map[string]config.PluginState{}
	state, err := SetRepoEnabled(states, NotificationsID, "/repo/a", true)
	if err != nil {
		t.Fatal(err)
	}
	state.Config[RepoScopeConfigKey] = "global"
	states[NotificationsID] = state

	if !EnabledForRepo(states, NotificationsID, "/repo/b") {
		t.Fatalf("expected global scope to apply to every repo: %+v", state.Config)
	}
}

func TestSetRepoEnabledDoesNotChangeExplicitGlobalScope(t *testing.T) {
	states := map[string]config.PluginState{
		NotificationsID: {
			ID:      NotificationsID,
			Enabled: true,
			Config: map[string]string{
				"title_prefix":         "Pulled",
				RepoScopeConfigKey:     "global",
				SelectedReposConfigKey: `["/repo/old"]`,
			},
		},
	}

	state, err := SetRepoEnabled(states, NotificationsID, "/repo/a", true)
	if err != nil {
		t.Fatal(err)
	}
	if state.Config[RepoScopeConfigKey] != "global" {
		t.Fatalf("expected repo toggle to preserve global scope, got %+v", state.Config)
	}
	repos := selectedRepoSet(state.Config)
	if !repos["/repo/a"] || !repos["/repo/old"] {
		t.Fatalf("expected selected repo list to be updated, got %+v", state.Config)
	}
}

func TestAISummaryPluginIsRegisteredDisabledByDefault(t *testing.T) {
	def, err := ValidateID(AISummaryID)
	if err != nil {
		t.Fatal(err)
	}
	if def.DefaultOn {
		t.Fatalf("expected AI summary plugin to be disabled by default")
	}
	if def.DefaultConfig["provider"] == "" {
		t.Fatalf("expected default provider config: %+v", def.DefaultConfig)
	}
	foundProviderName := false
	foundPrompt := false
	for _, field := range def.Fields {
		if field.Key == "provider" && field.Type == "text" {
			foundProviderName = true
		}
		if field.Key == "prompt" && field.Type == "textarea" {
			foundPrompt = true
		}
	}
	if !foundProviderName {
		t.Fatalf("expected editable provider name field: %+v", def.Fields)
	}
	if !foundPrompt || def.DefaultConfig["prompt"] == "" {
		t.Fatalf("expected editable prompt field: %+v", def.Fields)
	}
}

func TestAISummaryMissingProviderConfig(t *testing.T) {
	_, err := generateAISummary(map[string]string{
		"url":   "https://api.example.test/v1",
		"token": "test-key",
	}, "repo", "abc123 change")
	if !errors.Is(err, errAIProviderNotConfigured) {
		t.Fatalf("expected not configured error, got %v", err)
	}
}

func TestRunOneReturnsDisabledError(t *testing.T) {
	oldRegistry := registry
	registry = []Definition{{
		ID:        "manual",
		Name:      "Manual",
		DefaultOn: false,
		Run: func(Context) error {
			t.Fatal("disabled plugin should not run")
			return nil
		},
	}}
	defer func() { registry = oldRegistry }()

	err := RunOne("manual", Context{}, map[string]config.PluginState{})
	if !errors.Is(err, ErrPluginDisabled) {
		t.Fatalf("expected disabled error, got %v", err)
	}
}

func TestPluginRepoSelection(t *testing.T) {
	states := map[string]config.PluginState{}
	state, err := SetRepoEnabled(states, AISummaryID, "/repo/a", true)
	if err != nil {
		t.Fatal(err)
	}
	states[AISummaryID] = state

	if !state.Enabled {
		t.Fatalf("expected plugin to be enabled")
	}
	if state.Config[RepoScopeConfigKey] != "selected" {
		t.Fatalf("expected selected repo scope, got %+v", state.Config)
	}
	if !EnabledForRepo(states, AISummaryID, "/repo/a") {
		t.Fatalf("expected repo to be enabled")
	}
	if EnabledForRepo(states, AISummaryID, "/repo/b") {
		t.Fatalf("expected other repo to be disabled")
	}

	states[AISummaryID], err = SetRepoEnabled(states, AISummaryID, "/repo/a", false)
	if err != nil {
		t.Fatal(err)
	}
	if EnabledForRepo(states, AISummaryID, "/repo/a") {
		t.Fatalf("expected repo to be disabled")
	}
}

func TestRunAfterChangeHonorsGenericRepoSelection(t *testing.T) {
	called := 0
	oldRegistry := registry
	registry = []Definition{{
		ID:        "generic",
		Name:      "Generic",
		DefaultOn: true,
		Run: func(Context) error {
			called++
			return nil
		},
	}}
	defer func() { registry = oldRegistry }()

	states := map[string]config.PluginState{}
	state, err := SetRepoEnabled(states, "generic", "/repo/a", true)
	if err != nil {
		t.Fatal(err)
	}
	states["generic"] = state

	RunAfterChange(Context{Repo: &config.RepoInfo{Path: "/repo/b"}, Update: db.Update{Changed: true}}, states)
	if called != 0 {
		t.Fatalf("expected plugin to be skipped for unselected repo, called %d times", called)
	}
	RunAfterChange(Context{Repo: &config.RepoInfo{Path: "/repo/a"}, Update: db.Update{Changed: true}}, states)
	if called != 1 {
		t.Fatalf("expected plugin to run for selected repo, called %d times", called)
	}
}

func TestResponsesSummaryCall(t *testing.T) {
	oldClient := aiSummaryHTTPClient
	defer func() { aiSummaryHTTPClient = oldClient }()

	var sawAuth bool
	var sawPrompt bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "Bearer test-key" {
			sawAuth = true
		}
		var payload struct {
			Instructions string `json:"instructions"`
			Input        string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Instructions == "Custom prompt" && payload.Input == "Repository: repo\n\nChange context:\nabc123 change" {
			sawPrompt = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output_text":"Updated auth flow and storage tests."}`))
	}))
	defer server.Close()
	aiSummaryHTTPClient = server.Client()

	summary, err := generateAISummary(map[string]string{
		"provider": "Responses provider",
		"url":      server.URL + "/v1",
		"token":    "test-key",
		"model":    "gpt-test",
		"prompt":   "Custom prompt",
	}, "repo", "abc123 change")
	if err != nil {
		t.Fatal(err)
	}
	if !sawAuth {
		t.Fatalf("expected bearer authorization header")
	}
	if !sawPrompt {
		t.Fatalf("expected custom prompt and canonical input")
	}
	if summary != "Updated auth flow and storage tests." {
		t.Fatalf("unexpected summary: %q", summary)
	}
}

func TestAISummaryInputAndContextTruncation(t *testing.T) {
	input := AISummaryInput("repo", "Unified code diff:\n+new code")
	if !strings.Contains(input, "Repository: repo") || !strings.Contains(input, "Unified code diff") {
		t.Fatalf("unexpected input: %q", input)
	}

	large := strings.Repeat("x", maxAIChangeContextBytes+10)
	truncated := truncateAIChangeContext(large)
	if len(truncated) <= maxAIChangeContextBytes || !strings.Contains(truncated, "[truncated:") {
		t.Fatalf("expected truncation marker, got len=%d", len(truncated))
	}
}

func TestBuildAISummaryChangeContextSelectsFileDiffsWithinBudget(t *testing.T) {
	context, err := buildAISummaryChangeContext(
		"abc123 change",
		"small.go | 1 +\nlarge.go | 10000 +",
		[]string{"small.go", "large.go"},
		func(filePath string) (string, error) {
			if filePath == "large.go" {
				return strings.Repeat("+large change\n", maxAIChangeContextBytes), nil
			}
			return "diff --git a/small.go b/small.go\n+small change", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Commits and file stats", "Diff summary", "File diff: small.go", "+small change", "Omitted files", "large.go"} {
		if !strings.Contains(context, want) {
			t.Fatalf("expected context to contain %q:\n%s", want, context)
		}
	}
	if strings.Contains(context, "+large change") {
		t.Fatalf("expected large diff to be omitted")
	}
}

func TestChatCompletionsSummaryCall(t *testing.T) {
	oldClient := aiSummaryHTTPClient
	defer func() { aiSummaryHTTPClient = oldClient }()

	var sawPath bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			sawPath = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Chat completion summary."}}]}`))
	}))
	defer server.Close()
	aiSummaryHTTPClient = server.Client()

	summary, err := generateAISummary(map[string]string{
		"provider": "Chat provider",
		"api_type": "chat_completions",
		"url":      server.URL + "/v1",
		"token":    "test-key",
		"model":    "local-model",
	}, "repo", "abc123 change")
	if err != nil {
		t.Fatal(err)
	}
	if !sawPath {
		t.Fatalf("expected chat completions endpoint")
	}
	if summary != "Chat completion summary." {
		t.Fatalf("unexpected summary: %q", summary)
	}
}

func TestResponseOutputTextFallback(t *testing.T) {
	got := responseOutputText([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"fallback summary"}]}]}`))
	if got != "fallback summary" {
		t.Fatalf("unexpected output text: %q", got)
	}
}

func TestChatCompletionText(t *testing.T) {
	got := chatCompletionText([]byte(`{"choices":[{"message":{"content":"hello from chat"}}]}`))
	if got != "hello from chat" {
		t.Fatalf("unexpected chat completion text: %q", got)
	}
}
