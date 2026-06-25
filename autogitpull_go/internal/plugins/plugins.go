package plugins

import (
	"fmt"
	"log/slog"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
)

type Field struct {
	Key   string
	Label string
	Type  string
}

type Definition struct {
	ID            string
	Name          string
	Description   string
	DefaultOn     bool
	DefaultConfig map[string]string
	RunOnNoChange bool
	Fields        []Field
	Run           func(Context) error
}

type View struct {
	Definition
	Enabled bool
	Config  map[string]string
}

type Context struct {
	Repo      *config.RepoInfo
	Update    db.Update
	Config    map[string]string
	Notify    bool
	Source    string
	Dashboard string
	OpenURL   string
	AppName   string
	Logger    *slog.Logger
}

var registry = []Definition{
	notificationPlugin(),
}

func Definitions() []Definition {
	out := make([]Definition, len(registry))
	copy(out, registry)
	return out
}

func Views(states map[string]config.PluginState) []View {
	defs := Definitions()
	views := make([]View, 0, len(defs))
	for _, def := range defs {
		state, ok := states[def.ID]
		enabled := def.DefaultOn
		cfg := cloneConfig(def.DefaultConfig)
		if ok {
			enabled = state.Enabled
			for k, v := range state.Config {
				cfg[k] = v
			}
		}
		views = append(views, View{Definition: def, Enabled: enabled, Config: cfg})
	}
	return views
}

func StateFromView(view View) config.PluginState {
	return config.PluginState{ID: view.ID, Enabled: view.Enabled, Config: view.Config}
}

func EnsureDefaults(storage *config.StorageManager) {
	if storage == nil {
		return
	}
	states := storage.GetPluginStates()
	for _, def := range Definitions() {
		if _, ok := states[def.ID]; ok {
			continue
		}
		if err := storage.SetPluginState(config.PluginState{
			ID:      def.ID,
			Enabled: def.DefaultOn,
			Config:  cloneConfig(def.DefaultConfig),
		}); err != nil {
			slog.Error("failed to save default plugin state", slog.String("plugin", def.ID), slog.String("err", err.Error()))
		}
	}
}

func RunAfterChange(ctx Context, states map[string]config.PluginState) {
	if ctx.Logger == nil {
		ctx.Logger = slog.Default()
	}
	for _, view := range Views(states) {
		if !ctx.Update.Changed && !view.RunOnNoChange {
			continue
		}
		if !view.Enabled || view.Run == nil {
			continue
		}
		next := ctx
		next.Config = view.Config
		if err := view.Run(next); err != nil {
			repoName := ""
			if ctx.Repo != nil {
				repoName = ctx.Repo.Name
			}
			ctx.Logger.Error("plugin failed", slog.String("plugin", view.ID), slog.String("repo", repoName), slog.String("err", err.Error()))
		}
	}
}

func cloneConfig(values map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range values {
		out[k] = v
	}
	return out
}

func ValidateID(id string) (Definition, error) {
	for _, def := range registry {
		if def.ID == id {
			return def, nil
		}
	}
	return Definition{}, fmt.Errorf("unknown plugin: %s", id)
}
