package plugins

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
	for _, field := range def.Fields {
		if field.Key == "provider" && field.Type == "text" {
			foundProviderName = true
		}
	}
	if !foundProviderName {
		t.Fatalf("expected editable provider name field: %+v", def.Fields)
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "Bearer test-key" {
			sawAuth = true
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
	}, "repo", "abc123 change")
	if err != nil {
		t.Fatal(err)
	}
	if !sawAuth {
		t.Fatalf("expected bearer authorization header")
	}
	if summary != "Updated auth flow and storage tests." {
		t.Fatalf("unexpected summary: %q", summary)
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
