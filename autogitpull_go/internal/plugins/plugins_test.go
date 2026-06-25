package plugins

import (
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
