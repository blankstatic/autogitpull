package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
)

var ErrPluginDisabled = errors.New("plugin disabled")

const (
	RepoScopeConfigKey     = "repo_scope"
	SelectedReposConfigKey = "selected_repos"
	repoScopeGlobal        = "global"
	repoScopeSelected      = "selected"
)

var repoScopeField = Field{Key: RepoScopeConfigKey, Label: "Repos", Type: "select", Options: []FieldOption{
	{Value: repoScopeGlobal, Label: "All repos"},
	{Value: repoScopeSelected, Label: "Selected repos"},
}}

type Field struct {
	Key     string
	Label   string
	Type    string
	Options []FieldOption
}

type FieldOption struct {
	Value string
	Label string
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
	Enabled       bool
	Config        map[string]string
	SelectedRepos []string
}

type Context struct {
	Repo      *config.RepoInfo
	Update    db.Update
	Store     *db.Store
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
	aiSummaryPlugin(),
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
		if cfg[RepoScopeConfigKey] == "" {
			cfg[RepoScopeConfigKey] = defaultRepoScope(cfg)
		}
		def.Fields = ConfigFields(def)
		views = append(views, View{Definition: def, Enabled: enabled, Config: cfg, SelectedRepos: selectedRepoPaths(cfg)})
	}
	return views
}

func ConfigFields(def Definition) []Field {
	fields := make([]Field, 0, len(def.Fields)+1)
	hasRepoScope := false
	for _, field := range def.Fields {
		if field.Key == RepoScopeConfigKey {
			hasRepoScope = true
		}
		fields = append(fields, field)
	}
	if !hasRepoScope {
		fields = append(fields, repoScopeField)
	}
	return fields
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
		if !viewAppliesToRepo(view, ctx.Repo) {
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

func RunOne(id string, ctx Context, states map[string]config.PluginState) error {
	if ctx.Logger == nil {
		ctx.Logger = slog.Default()
	}
	for _, view := range Views(states) {
		if view.ID != id {
			continue
		}
		if !view.Enabled || view.Run == nil {
			return ErrPluginDisabled
		}
		ctx.Config = view.Config
		return view.Run(ctx)
	}
	return fmt.Errorf("unknown plugin: %s", id)
}

func EnabledForRepo(states map[string]config.PluginState, pluginID, repoPath string) bool {
	for _, view := range Views(states) {
		if view.ID != pluginID || !view.Enabled {
			continue
		}
		if view.Config["run_mode"] == repoScopeGlobal || view.Config[RepoScopeConfigKey] == repoScopeGlobal {
			return true
		}
		return selectedRepoSet(view.Config)[repoPath]
	}
	return false
}

func SetRepoEnabled(states map[string]config.PluginState, pluginID, repoPath string, enabled bool) (config.PluginState, error) {
	def, err := ValidateID(pluginID)
	if err != nil {
		return config.PluginState{}, err
	}
	state, ok := states[pluginID]
	if !ok {
		state = config.PluginState{ID: pluginID, Config: cloneConfig(def.DefaultConfig)}
	}
	if state.Config == nil {
		state.Config = map[string]string{}
	}
	for key, value := range def.DefaultConfig {
		if _, ok := state.Config[key]; !ok {
			state.Config[key] = value
		}
	}
	hadScope := state.Config[RepoScopeConfigKey] != ""
	if state.Config[RepoScopeConfigKey] == "" {
		state.Config[RepoScopeConfigKey] = defaultRepoScope(state.Config)
	}
	repos := selectedRepoSet(state.Config)
	if enabled {
		repos[repoPath] = true
		state.Enabled = true
		if !hadScope || state.Config[RepoScopeConfigKey] == "manual" {
			state.Config[RepoScopeConfigKey] = repoScopeSelected
		}
		if state.Config[RepoScopeConfigKey] == repoScopeSelected && (state.Config["run_mode"] == "" || state.Config["run_mode"] == "manual") {
			state.Config["run_mode"] = repoScopeSelected
		}
	} else {
		delete(repos, repoPath)
	}
	state.Config[SelectedReposConfigKey] = encodeSelectedRepos(repos)
	return state, nil
}

func viewAppliesToRepo(view View, repo *config.RepoInfo) bool {
	if repo == nil {
		return true
	}
	if view.Config["run_mode"] == repoScopeSelected || view.Config[RepoScopeConfigKey] == repoScopeSelected {
		return selectedRepoSet(view.Config)[repo.Path]
	}
	return true
}

func defaultRepoScope(cfg map[string]string) string {
	if cfg["run_mode"] == repoScopeSelected || cfg["run_mode"] == "manual" {
		return repoScopeSelected
	}
	return repoScopeGlobal
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

func selectedRepoPaths(cfg map[string]string) []string {
	repos := selectedRepoSet(cfg)
	values := make([]string, 0, len(repos))
	for repo := range repos {
		values = append(values, repo)
	}
	sort.Strings(values)
	return values
}

func selectedRepoSet(cfg map[string]string) map[string]bool {
	repos := map[string]bool{}
	if cfg == nil || strings.TrimSpace(cfg[SelectedReposConfigKey]) == "" {
		return repos
	}
	var values []string
	if err := json.Unmarshal([]byte(cfg[SelectedReposConfigKey]), &values); err != nil {
		return repos
	}
	for _, value := range values {
		if value != "" {
			repos[value] = true
		}
	}
	return repos
}

func encodeSelectedRepos(repos map[string]bool) string {
	values := make([]string, 0, len(repos))
	for repo := range repos {
		values = append(values, repo)
	}
	sort.Strings(values)
	data, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(data)
}
