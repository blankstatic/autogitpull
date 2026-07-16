package web

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/plugins"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/pulllock"
	servicepkg "github.com/blankstatic/autogitpull/autogitpull_go/internal/service"
	versionpkg "github.com/blankstatic/autogitpull/autogitpull_go/internal/version"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
)

const Addr = ":9009"
const serviceInterval = 30 * time.Minute
const serviceLabel = "com.blankstatic.autogitpull"
const updatesPerPage = 50
const activityWeeks = 53
const maxConcurrentPulls = 4
const eventFilterChanges = "changes"
const eventFilterAll = "all"

//go:embed assets/featurehub.png
var appIcon []byte

type Server struct {
	store          *db.Store
	storage        *config.StorageManager
	mux            *http.ServeMux
	httpServer     *http.Server
	requestWG      sync.WaitGroup
	bulkWG         sync.WaitGroup
	pluginTasks    chan func()
	pluginWG       sync.WaitGroup
	pluginCtx      context.Context
	cancelPlugins  context.CancelFunc
	lifecycleMu    sync.Mutex
	acceptTasks    bool
	acceptRequests bool
	shutdownOnce   sync.Once
}

const webPluginWorkers = 4
const webPluginQueueSize = 64

type RepoCard struct {
	Repo       config.RepoInfo
	LastUpdate *db.Update
}

type PluginSummary struct {
	Total   int
	Enabled int
}

type PluginRepoControl struct {
	ID          string
	Name        string
	Status      string
	StatusClass string
	Action      string
	NextEnabled bool
}

type ChangedFile struct {
	Status string
	Path   string
}

type AISummaryInputPreview struct {
	Prompt   string
	Input    string
	Error    string
	Provider string
	APIType  string
	Model    string
}

type flashMessage struct {
	Text  string
	Class string
}

type DaemonStatus struct {
	NextRunAt       time.Time
	LastRunStarted  time.Time
	LastRunDuration time.Duration
	RunningRepos    []string
	Checked         int
	Success         int
	Skipped         int
	Error           int
}

var daemonStatus = struct {
	sync.RWMutex
	status DaemonStatus
}{}

func SetDaemonNextRun(t time.Time) {
	daemonStatus.Lock()
	daemonStatus.status.NextRunAt = t
	daemonStatus.Unlock()
}

func SetDaemonRunStarted(t time.Time) {
	daemonStatus.Lock()
	daemonStatus.status.LastRunStarted = t
	daemonStatus.status.RunningRepos = nil
	daemonStatus.status.Checked = 0
	daemonStatus.status.Success = 0
	daemonStatus.status.Skipped = 0
	daemonStatus.status.Error = 0
	daemonStatus.Unlock()
}

func SetDaemonRunFinished(duration time.Duration) {
	daemonStatus.Lock()
	daemonStatus.status.LastRunDuration = duration
	daemonStatus.status.RunningRepos = nil
	daemonStatus.Unlock()
}

func AddDaemonRunResult(status string) {
	daemonStatus.Lock()
	defer daemonStatus.Unlock()
	daemonStatus.status.Checked++
	switch status {
	case "success":
		daemonStatus.status.Success++
	case "skipped":
		daemonStatus.status.Skipped++
	default:
		daemonStatus.status.Error++
	}
}

func SetDaemonRepoRunning(repoName string, running bool) {
	daemonStatus.Lock()
	defer daemonStatus.Unlock()
	if running {
		for _, name := range daemonStatus.status.RunningRepos {
			if name == repoName {
				return
			}
		}
		daemonStatus.status.RunningRepos = append(daemonStatus.status.RunningRepos, repoName)
		return
	}
	for i, name := range daemonStatus.status.RunningRepos {
		if name == repoName {
			daemonStatus.status.RunningRepos = append(daemonStatus.status.RunningRepos[:i], daemonStatus.status.RunningRepos[i+1:]...)
			return
		}
	}
}

func GetDaemonStatus() DaemonStatus {
	daemonStatus.RLock()
	defer daemonStatus.RUnlock()
	status := daemonStatus.status
	status.RunningRepos = append([]string(nil), status.RunningRepos...)
	return status
}

func New(store *db.Store, storage *config.StorageManager) *Server {
	plugins.EnsureDefaults(storage)
	s := &Server{
		store:          store,
		storage:        storage,
		mux:            http.NewServeMux(),
		pluginTasks:    make(chan func(), webPluginQueueSize),
		acceptTasks:    true,
		acceptRequests: true,
	}
	s.pluginCtx, s.cancelPlugins = context.WithCancel(context.Background())
	s.routes()
	for range webPluginWorkers {
		s.pluginWG.Add(1)
		go func() {
			defer s.pluginWG.Done()
			for task := range s.pluginTasks {
				task()
			}
		}()
	}
	return s
}

func (s *Server) Start() {
	s.lifecycleMu.Lock()
	if s.httpServer != nil {
		s.lifecycleMu.Unlock()
		return
	}
	s.httpServer = &http.Server{Addr: Addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lifecycleMu.Lock()
		if !s.acceptRequests {
			s.lifecycleMu.Unlock()
			http.Error(w, "server shutting down", http.StatusServiceUnavailable)
			return
		}
		s.requestWG.Add(1)
		s.lifecycleMu.Unlock()
		defer s.requestWG.Done()
		s.mux.ServeHTTP(w, r)
	})}
	httpServer := s.httpServer
	s.lifecycleMu.Unlock()
	go func() {
		slog.Info("web dashboard started", slog.String("addr", Addr))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("web dashboard failed", slog.String("err", err.Error()))
		}
	}()
}

func (s *Server) Shutdown(ctx context.Context) error {
	var shutdownErr error
	s.shutdownOnce.Do(func() {
		stopCancelWatch := context.AfterFunc(ctx, s.cancelPlugins)
		defer stopCancelWatch()
		s.lifecycleMu.Lock()
		httpServer := s.httpServer
		s.lifecycleMu.Unlock()
		if httpServer != nil {
			shutdownErr = httpServer.Shutdown(ctx)
		}
		s.lifecycleMu.Lock()
		s.acceptRequests = false
		s.lifecycleMu.Unlock()
		s.requestWG.Wait()
		s.bulkWG.Wait()
		s.lifecycleMu.Lock()
		s.acceptTasks = false
		close(s.pluginTasks)
		s.lifecycleMu.Unlock()
		if !waitGroupWithContext(ctx, &s.pluginWG) {
			s.cancelPlugins()
			s.pluginWG.Wait()
		} else {
			s.cancelPlugins()
		}
		if shutdownErr == nil && ctx.Err() != nil {
			shutdownErr = ctx.Err()
		}
	})
	return shutdownErr
}

func waitGroupWithContext(ctx context.Context, wg *sync.WaitGroup) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.index)
	s.mux.HandleFunc("/repo", s.repo)
	s.mux.HandleFunc("/repo/pull", s.pullRepo)
	s.mux.HandleFunc("/repo/ai-summary", s.runRepoAISummary)
	s.mux.HandleFunc("/repo/plugin-toggle", s.toggleRepoPlugin)
	s.mux.HandleFunc("/update", s.update)
	s.mux.HandleFunc("/update/ai-summary", s.runUpdateAISummary)
	s.mux.HandleFunc("/repo/pause", s.pauseRepo)
	s.mux.HandleFunc("/repo/notify", s.notifyRepo)
	s.mux.HandleFunc("/repo/open", s.openRepo)
	s.mux.HandleFunc("/repo/unregister", s.unregisterRepo)
	s.mux.HandleFunc("/repos/bulk", s.bulkRepos)
	s.mux.HandleFunc("/settings", s.settings)
	s.mux.HandleFunc("/status", s.status)
	s.mux.HandleFunc("/plugins", s.plugins)
	s.mux.HandleFunc("/plugins/save", s.savePlugin)
	s.mux.HandleFunc("/plugins/remove-repo", s.removePluginRepo)
	s.mux.HandleFunc("/plugins/test-ai-summary", s.testAISummaryPlugin)
	s.mux.HandleFunc("/plugins/test-notifications", s.testNotificationsPlugin)
	s.mux.HandleFunc("/favicon.ico", s.icon)
	s.mux.HandleFunc("/assets/app-icon.png", s.icon)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	page := pageFromRequest(r)
	filter := eventFilterFromRequest(r)
	updateFilter := updateFilterFromEventFilter(filter)
	totalUpdates, err := s.store.CountUpdatesFiltered(updateFilter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	page = clampPage(page, totalUpdates)

	updates, err := s.store.RecentUpdatesPageFiltered(updatesPerPage, pageOffset(page), updateFilter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	repos := s.storage.GetAllRepos()
	latestUpdates, err := s.store.LatestUpdatesByRepo()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	activity, err := s.activity("")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	renderTemplate(w, indexTemplate, map[string]any{
		"Repos":         repos,
		"RepoCards":     repoCards(repos, latestUpdates),
		"Updates":       updates,
		"Activity":      activity,
		"RepoCount":     len(repos),
		"UpdateCount":   len(updates),
		"TotalUpdates":  totalUpdates,
		"Pagination":    newPagination(r.URL.Path, filterQueryValues(filter), page, totalUpdates),
		"EventFilter":   newEventFilter(r.URL.Path, nil, filter),
		"PluginSummary": newPluginSummary(s.storage.GetPluginStates()),
		"AppVersion":    versionpkg.AppVersion,
		"Flash":         flashFromRequest(r),
	})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	dbPath, _ := config.GetUpdatesDBPath()
	renderTemplate(w, statusTemplate, map[string]any{
		"DBPath":        dbPath,
		"ServiceStatus": getServiceStatus(),
		"ServiceLabel":  serviceLabel,
		"DaemonStatus":  GetDaemonStatus(),
		"PluginSummary": newPluginSummary(s.storage.GetPluginStates()),
		"Flash":         flashFromRequest(r),
	})
}

func (s *Server) repo(w http.ResponseWriter, r *http.Request) {
	repoPath := r.URL.Query().Get("path")
	if repoPath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}

	repo, err := s.storage.GetRepo(repoPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	page := pageFromRequest(r)
	filter := eventFilterFromRequest(r)
	updateFilter := updateFilterFromEventFilter(filter)
	totalUpdates, err := s.store.CountRepoUpdatesFiltered(repoPath, updateFilter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	page = clampPage(page, totalUpdates)

	updates, err := s.store.RepoUpdatesPageFiltered(repoPath, updatesPerPage, pageOffset(page), updateFilter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	changes, err := git.GitGetUncommitedChanges(repoPath)
	if err != nil {
		changes = err.Error()
	}
	changedFiles := parseChangedFiles(changes)

	activity, err := s.activity(repoPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pluginStates := s.storage.GetPluginStates()
	remoteURL, _ := git.GetRemoteWebURL(repoPath)
	renderTemplate(w, repoTemplate, map[string]any{
		"Repo":           repo,
		"RemoteURL":      remoteURL,
		"PluginControls": pluginRepoControls(pluginStates, repo.Path),
		"Updates":        updates,
		"Activity":       activity,
		"Changes":        changes,
		"ChangedFiles":   changedFiles,
		"TotalUpdates":   totalUpdates,
		"Pagination":     newPagination(r.URL.Path, repoQueryValues(repoPath, filter), page, totalUpdates),
		"EventFilter":    newEventFilter(r.URL.Path, url.Values{"path": []string{repoPath}}, filter),
		"Flash":          flashFromRequest(r),
	})
}

func (s *Server) toggleRepoPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pluginID := r.FormValue("plugin_id")
	if pluginID == "" {
		http.Error(w, "missing plugin_id", http.StatusBadRequest)
		return
	}
	def, err := plugins.ValidateID(pluginID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	repoPath := r.FormValue("path")
	if repoPath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	repo, err := s.storage.GetRepo(repoPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	enabled := r.FormValue("enabled") == "1"
	state, err := plugins.SetRepoEnabled(s.storage.GetPluginStates(), pluginID, repo.Path, enabled)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.storage.SetPluginState(state); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if enabled {
		redirectRepoFlash(w, r, repo.Path, def.Name+" enabled for this repo", "success")
		return
	}
	redirectRepoFlash(w, r, repo.Path, def.Name+" disabled for this repo", "info")
}

func (s *Server) pullRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repoPath := r.FormValue("path")
	if repoPath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	repo, err := s.storage.GetRepo(repoPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !pulllock.TryLock(repo.Path) {
		redirectRepoFlash(w, r, repo.Path, "Pull already running", "skipped")
		return
	}
	defer pulllock.Unlock(repo.Path)

	SetDaemonRepoRunning(repo.Name, true)
	defer SetDaemonRepoRunning(repo.Name, false)
	updateID, err := s.store.BeginUpdate(repo.Path, repo.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	result, beforeRev, afterRev, pullErr := performPull(repo)
	if err := s.store.FinishUpdateWithRevisions(updateID, result, pullErr, beforeRev, afterRev); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if pullErr != nil {
		http.Redirect(w, r, repoURLWithFlash(repoPath, eventFilterAll, pullFlashText(pullErr), pullFlashType(pullErr)), http.StatusSeeOther)
		return
	}
	if pullErr == nil {
		_ = s.storage.UpdateLastSync(repo.Path)
		s.runPluginsAfterChangeAsync(repo, updateID, true, "web_manual")
	}
	http.Redirect(w, r, repoURLWithFlash(repoPath, eventFilterAll, "Pulled successfully", "success"), http.StatusSeeOther)
}

func (s *Server) runRepoAISummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repoPath := r.FormValue("path")
	if repoPath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	repo, err := s.storage.GetRepo(repoPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	updates, err := s.store.RepoUpdatesPageFiltered(repo.Path, 1, 0, db.UpdateFilter{ChangedOnly: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(updates) == 0 {
		redirectRepoFlash(w, r, repo.Path, "No changed update to summarize", "skipped")
		return
	}
	err = plugins.RunOne(plugins.AISummaryID, plugins.Context{
		Repo:      repo,
		Update:    updates[0],
		Store:     s.store,
		Source:    "web_repo_manual_ai",
		Dashboard: "http://localhost" + Addr,
		OpenURL:   "http://localhost" + Addr + updateURL(updates[0].ID),
		AppName:   config.AppName,
		Logger:    slog.Default(),
	}, s.storage.GetPluginStates())
	if errors.Is(err, plugins.ErrPluginDisabled) {
		redirectRepoFlash(w, r, repo.Path, "AI summary plugin is disabled", "skipped")
		return
	}
	if err != nil {
		http.Redirect(w, r, updateURLWithFlashFragment(updates[0].ID, "AI summary failed; see plugin results below", "skipped", "plugin-results"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, updateURLWithFlashFragment(updates[0].ID, "AI summary generated", "success", "ai-summary"), http.StatusSeeOther)
}

func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid update id", http.StatusBadRequest)
		return
	}
	update, err := s.store.GetUpdate(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	results, err := s.store.PluginResultsByUpdate(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	renderTemplate(w, updateTemplate, map[string]any{
		"Update":        update,
		"AISummaries":   aiSummaryResults(results),
		"AIInput":       s.aiSummaryInputPreview(update),
		"PluginResults": results,
		"Flash":         flashFromRequest(r),
	})
}

func (s *Server) runUpdateAISummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid update id", http.StatusBadRequest)
		return
	}
	update, err := s.store.GetUpdate(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	repo, err := s.storage.GetRepo(update.RepoPath)
	if err != nil {
		repo = &config.RepoInfo{Path: update.RepoPath, Name: update.RepoName}
	}
	err = plugins.RunOne(plugins.AISummaryID, plugins.Context{
		Repo:      repo,
		Update:    update,
		Store:     s.store,
		Source:    "web_update_manual_ai",
		Dashboard: "http://localhost" + Addr,
		OpenURL:   "http://localhost" + Addr + updateURL(update.ID),
		AppName:   config.AppName,
		Logger:    slog.Default(),
	}, s.storage.GetPluginStates())
	if errors.Is(err, plugins.ErrPluginDisabled) {
		http.Redirect(w, r, updateURLWithFlashFragment(update.ID, "AI summary plugin is disabled", "skipped", "ai-summary"), http.StatusSeeOther)
		return
	}
	if err != nil {
		http.Redirect(w, r, updateURLWithFlashFragment(update.ID, "AI summary failed; see plugin results below", "skipped", "plugin-results"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, updateURLWithFlashFragment(update.ID, "AI summary generated", "success", "ai-summary"), http.StatusSeeOther)
}

func (s *Server) pauseRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repoPath := r.FormValue("path")
	if repoPath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	paused := r.FormValue("paused") == "1"
	if err := s.storage.SetRepoPaused(repoPath, paused); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if paused {
		redirectRepoFlash(w, r, repoPath, "Repo paused", "info")
		return
	}
	redirectRepoFlash(w, r, repoPath, "Repo resumed", "success")
}

func (s *Server) notifyRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repoPath := r.FormValue("path")
	notify := r.FormValue("notify") == "1"
	if err := s.storage.SetRepoNotify(repoPath, notify); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if notify {
		redirectRepoFlash(w, r, repoPath, "Notifications enabled", "success")
		return
	}
	redirectRepoFlash(w, r, repoPath, "Notifications muted", "info")
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := s.storage.GetConfig()
		renderTemplate(w, settingsTemplate, map[string]any{
			"PullInterval":  cfg.PullIntervalMinutes,
			"RetentionDays": cfg.HistoryRetentionDays,
			"Flash":         flashFromRequest(r),
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	minutes, err := strconv.Atoi(r.FormValue("pull_interval_minutes"))
	if err != nil || minutes <= 0 {
		http.Error(w, "pull interval must be positive", http.StatusBadRequest)
		return
	}
	retentionDays, err := strconv.Atoi(r.FormValue("history_retention_days"))
	if err != nil || retentionDays <= 0 {
		http.Error(w, "history retention must be positive", http.StatusBadRequest)
		return
	}
	if err := s.storage.SetPullIntervalMinutes(minutes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.storage.SetHistoryRetentionDays(retentionDays); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.store.DeleteUpdatesBefore(time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, flashURL("/settings", "Settings saved", "success"), http.StatusSeeOther)
}

func (s *Server) plugins(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, pluginsTemplate, map[string]any{
		"Plugins": plugins.Views(s.storage.GetPluginStates()),
		"Flash":   flashFromRequest(r),
	})
}

func (s *Server) savePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.FormValue("id")
	def, err := plugins.ValidateID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	state := config.PluginState{ID: id, Enabled: r.FormValue("enabled") == "1", Config: map[string]string{}}
	if existing, ok := s.storage.GetPluginStates()[id]; ok {
		for key, value := range existing.Config {
			state.Config[key] = value
		}
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, field := range plugins.ConfigFields(def) {
		formKey := "config_" + field.Key
		if _, ok := r.Form[formKey]; !ok {
			continue
		}
		state.Config[field.Key] = strings.TrimSpace(r.FormValue(formKey))
	}
	if def.ValidateConfig != nil {
		if err := def.ValidateConfig(state.Config); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := s.storage.SetPluginState(state); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, flashURL("/plugins", "Plugin saved", "success"), http.StatusSeeOther)
}

func (s *Server) removePluginRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.FormValue("id")
	repoPath := r.FormValue("repo")
	if repoPath == "" {
		http.Error(w, "missing repo", http.StatusBadRequest)
		return
	}
	state, err := plugins.SetRepoEnabled(s.storage.GetPluginStates(), id, repoPath, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.storage.SetPluginState(state); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, flashURL("/plugins", "Selected repo removed", "success"), http.StatusSeeOther)
}

func (s *Server) testAISummaryPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	states := s.storage.GetPluginStates()
	state, ok := states[plugins.AISummaryID]
	if !ok {
		http.Redirect(w, r, flashURL("/plugins", "AI summary plugin settings not found", "skipped"), http.StatusSeeOther)
		return
	}
	view := plugins.Views(states)
	cfg := state.Config
	for _, item := range view {
		if item.ID == plugins.AISummaryID {
			cfg = item.Config
			break
		}
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	def, err := plugins.ValidateID(plugins.AISummaryID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, field := range plugins.ConfigFields(def) {
		formKey := "config_" + field.Key
		if _, ok := r.Form[formKey]; ok {
			cfg[field.Key] = strings.TrimSpace(r.FormValue(formKey))
		}
	}
	if def.ValidateConfig != nil {
		if err := def.ValidateConfig(cfg); err != nil {
			http.Redirect(w, r, flashURL("/plugins", "AI summary test failed: "+err.Error(), "skipped"), http.StatusSeeOther)
			return
		}
	}
	result, err := plugins.TestAISummary(cfg)
	if err != nil {
		http.Redirect(w, r, flashURL("/plugins", "AI summary test failed: "+err.Error(), "skipped"), http.StatusSeeOther)
		return
	}
	if result == "" {
		result = "(empty response)"
	}
	http.Redirect(w, r, flashURL("/plugins", "AI summary test: "+firstLine(result), "success"), http.StatusSeeOther)
}

func (s *Server) testNotificationsPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := map[string]string{
		"title_prefix": strings.TrimSpace(r.FormValue("config_title_prefix")),
	}
	if err := plugins.TestNotifications(config.AppName, cfg); err != nil {
		http.Redirect(w, r, flashURL("/plugins", "Notification test failed: "+err.Error(), "error"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, flashURL("/plugins", "Test notification sent", "success"), http.StatusSeeOther)
}

func (s *Server) bulkRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	action := r.FormValue("action")
	switch action {
	case "pull_all":
		repos := s.storage.GetAllRepos()
		queued := s.pullReposAsync(repos)
		if queued == 0 {
			http.Redirect(w, r, flashURL("/", "No repositories to pull", "skipped"), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, flashURL("/", fmt.Sprintf("Bulk pull started for %d repos", queued), "running"), http.StatusSeeOther)
	case "pause_errors":
		latest, err := s.store.LatestUpdatesByRepo()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		paused := 0
		for _, repo := range s.storage.GetAllRepos() {
			if update, ok := latest[repo.Path]; ok && update.Status == "error" {
				if err := s.storage.SetRepoPaused(repo.Path, true); err == nil {
					paused++
				}
			}
		}
		http.Redirect(w, r, flashURL("/", fmt.Sprintf("Paused %d error repos", paused), "info"), http.StatusSeeOther)
	case "resume_selected":
		if len(r.Form["repo"]) == 0 {
			http.Redirect(w, r, flashURL("/", "Select at least one repository", "skipped"), http.StatusSeeOther)
			return
		}
		resumed := 0
		for _, repoPath := range r.Form["repo"] {
			if err := s.storage.SetRepoPaused(repoPath, false); err == nil {
				resumed++
			}
		}
		http.Redirect(w, r, flashURL("/", fmt.Sprintf("Resumed %d selected repos", resumed), "success"), http.StatusSeeOther)
	case "pause_selected":
		if len(r.Form["repo"]) == 0 {
			http.Redirect(w, r, flashURL("/", "Select at least one repository", "skipped"), http.StatusSeeOther)
			return
		}
		paused := 0
		for _, repoPath := range r.Form["repo"] {
			if err := s.storage.SetRepoPaused(repoPath, true); err == nil {
				paused++
			}
		}
		http.Redirect(w, r, flashURL("/", fmt.Sprintf("Paused %d selected repos", paused), "info"), http.StatusSeeOther)
	default:
		http.Error(w, "unknown bulk action", http.StatusBadRequest)
	}
}

func (s *Server) pullReposAsync(repos []config.RepoInfo) int {
	pending := make([]config.RepoInfo, 0, len(repos))
	for _, repo := range repos {
		if !repo.Paused {
			pending = append(pending, repo)
		}
	}
	if len(pending) == 0 {
		return 0
	}
	s.bulkWG.Add(1)
	go func() {
		defer s.bulkWG.Done()
		sem := make(chan struct{}, maxConcurrentPulls)
		var wg sync.WaitGroup
		for i := range pending {
			repo := pending[i]
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				s.pullRepoRecord(&repo)
			}()
		}
		wg.Wait()
	}()
	return len(pending)
}

func (s *Server) pullRepoRecord(repo *config.RepoInfo) {
	if !pulllock.TryLock(repo.Path) {
		slog.Info("skipping bulk pull because repository is already being pulled", slog.String("repo", repo.Name), slog.String("path", repo.Path))
		return
	}
	defer pulllock.Unlock(repo.Path)

	SetDaemonRepoRunning(repo.Name, true)
	defer SetDaemonRepoRunning(repo.Name, false)

	updateID, err := s.store.BeginUpdate(repo.Path, repo.Name)
	if err != nil {
		slog.Error("failed to record bulk pull start", slog.String("repo", repo.Name), slog.String("err", err.Error()))
		return
	}
	result, beforeRev, afterRev, pullErr := performPull(repo)
	if err := s.store.FinishUpdateWithRevisions(updateID, result, pullErr, beforeRev, afterRev); err != nil {
		slog.Error("failed to record bulk pull result", slog.String("repo", repo.Name), slog.String("err", err.Error()))
		return
	}
	if pullErr == nil {
		_ = s.storage.UpdateLastSync(repo.Path)
		s.runPluginsAfterChangeAsync(repo, updateID, true, "web_bulk")
	}
}

func (s *Server) openRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repoPath := r.FormValue("path")
	if _, err := s.storage.GetRepo(repoPath); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	target := r.FormValue("target")
	if err := openRepoTarget(repoPath, target); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectRepoFlash(w, r, repoPath, "Opened in "+openTargetLabel(target), "info")
}

func (s *Server) unregisterRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		repoPath := r.URL.Query().Get("path")
		repo, err := s.storage.GetRepo(repoPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		renderTemplate(w, unregisterTemplate, map[string]any{"Repo": repo})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repoPath := r.FormValue("path")
	if repoPath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	repo, err := s.storage.GetRepo(repoPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if r.FormValue("confirm_name") != repo.Name {
		http.Error(w, "confirmation does not match repository name", http.StatusBadRequest)
		return
	}
	if err := s.storage.RemoveRepo(repoPath); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Redirect(w, r, flashURL("/", "Repo unregistered: "+repo.Name, "success"), http.StatusSeeOther)
}

func repoCards(repos []config.RepoInfo, latest map[string]db.Update) []RepoCard {
	cards := make([]RepoCard, 0, len(repos))
	for _, repo := range repos {
		card := RepoCard{Repo: repo}
		if update, ok := latest[repo.Path]; ok {
			updateCopy := update
			card.LastUpdate = &updateCopy
		}
		cards = append(cards, card)
	}
	return cards
}

func newPluginSummary(states map[string]config.PluginState) PluginSummary {
	views := plugins.Views(states)
	summary := PluginSummary{Total: len(views)}
	for _, view := range views {
		if view.Enabled {
			summary.Enabled++
		}
	}
	return summary
}

func pluginRepoControls(states map[string]config.PluginState, repoPath string) []PluginRepoControl {
	views := plugins.Views(states)
	controls := make([]PluginRepoControl, 0, len(views))
	for _, view := range views {
		control := PluginRepoControl{
			ID:     view.ID,
			Name:   view.Name,
			Status: "disabled", StatusClass: "paused",
			Action: "Enable for repo", NextEnabled: true,
		}
		scope := view.Config[plugins.RepoScopeConfigKey]
		if scope == "" {
			scope = view.Config["run_mode"]
		}
		if view.Enabled && scope != "selected" && scope != "manual" {
			control.Status = "global"
			control.StatusClass = "success"
			control.Action = "Limit to repo"
			control.NextEnabled = true
		} else if plugins.EnabledForRepo(states, view.ID, repoPath) {
			control.Status = "enabled"
			control.StatusClass = "success"
			control.Action = "Disable for repo"
			control.NextEnabled = false
		}
		controls = append(controls, PluginRepoControl{
			ID:          control.ID,
			Name:        control.Name,
			Status:      control.Status,
			StatusClass: control.StatusClass,
			Action:      control.Action,
			NextEnabled: control.NextEnabled,
		})
	}
	return controls
}

func aiSummaryResults(results []db.PluginResult) []db.PluginResult {
	out := make([]db.PluginResult, 0, len(results))
	for _, result := range results {
		if result.PluginID == plugins.AISummaryID {
			out = append(out, result)
		}
	}
	return out
}

func pluginResultMessage(result db.PluginResult) string {
	message := result.Result
	if result.Error != "" {
		message = result.Error
	}
	if result.PluginID == plugins.NotificationsID && message == "notifications disabled" {
		return "notification dispatch disabled (legacy result; restart daemon/web if this repeats)"
	}
	return message
}

func (s *Server) aiSummaryInputPreview(update db.Update) AISummaryInputPreview {
	cfg := aiSummaryConfig(s.storage.GetPluginStates())
	preview := AISummaryInputPreview{
		Prompt:   plugins.AISummaryPrompt(cfg),
		Provider: cfg["provider"],
		APIType:  cfg["api_type"],
		Model:    cfg["model"],
	}
	if update.BeforeRev == "" || update.AfterRev == "" || update.BeforeRev == update.AfterRev {
		preview.Error = "missing revision range"
		return preview
	}
	context, err := plugins.BuildAISummaryChangeContext(update.RepoPath, update.BeforeRev, update.AfterRev, cfg)
	if err != nil {
		preview.Error = err.Error()
		return preview
	}
	if context == "" {
		preview.Error = "empty change context"
		return preview
	}
	preview.Input = plugins.AISummaryInput(update.RepoName, context)
	return preview
}

func aiSummaryConfig(states map[string]config.PluginState) map[string]string {
	for _, view := range plugins.Views(states) {
		if view.ID == plugins.AISummaryID {
			return view.Config
		}
	}
	return map[string]string{}
}

func redirectRepo(w http.ResponseWriter, r *http.Request, repoPath string) {
	http.Redirect(w, r, repoURL(repoPath, ""), http.StatusSeeOther)
}

func redirectRepoFlash(w http.ResponseWriter, r *http.Request, repoPath, flash, flashType string) {
	http.Redirect(w, r, repoURLWithFlash(repoPath, "", flash, flashType), http.StatusSeeOther)
}

func repoURL(repoPath, filter string) string {
	values := url.Values{}
	if filter != "" {
		values.Set("filter", filter)
	}
	return "/repo?" + queryWithPath(values, repoPath)
}

func repoURLWithFlash(repoPath, filter, flash, flashType string) string {
	values := url.Values{}
	if filter != "" {
		values.Set("filter", filter)
	}
	if flash != "" {
		values.Set("flash", flash)
		if flashType != "" {
			values.Set("flash_type", flashType)
		}
	}
	return "/repo?" + queryWithPath(values, repoPath)
}

func updateURL(id int64) string {
	return "/update?id=" + strconv.FormatInt(id, 10)
}

func updateURLWithFlash(id int64, flash, flashType string) string {
	return updateURLWithFlashFragment(id, flash, flashType, "")
}

func updateURLWithFlashFragment(id int64, flash, flashType, fragment string) string {
	values := url.Values{}
	values.Set("id", strconv.FormatInt(id, 10))
	if flash != "" {
		values.Set("flash", flash)
		if flashType != "" {
			values.Set("flash_type", flashType)
		}
	}
	out := "/update?" + queryValues(values)
	if fragment != "" {
		out += "#" + url.QueryEscape(fragment)
	}
	return out
}

func queryWithPath(values url.Values, repoPath string) string {
	query := values.Encode()
	pathQuery := "path=" + strings.ReplaceAll(url.QueryEscape(repoPath), "%2F", "/")
	if query == "" {
		return pathQuery
	}
	return query + "&" + pathQuery
}

func queryValues(values url.Values) string {
	repoPath := values.Get("path")
	if repoPath == "" {
		return values.Encode()
	}
	next := cloneValues(values)
	next.Del("path")
	return queryWithPath(next, repoPath)
}

func flashURL(path, flash, flashType string) string {
	values := url.Values{}
	if flash != "" {
		values.Set("flash", flash)
	}
	if flashType != "" {
		values.Set("flash_type", flashType)
	}
	query := values.Encode()
	if query == "" {
		return path
	}
	return path + "?" + query
}

func flashFromRequest(r *http.Request) flashMessage {
	text := strings.TrimSpace(r.URL.Query().Get("flash"))
	if text == "" {
		return flashMessage{}
	}
	class := "info"
	switch r.URL.Query().Get("flash_type") {
	case "success", "error", "skipped", "running":
		class = r.URL.Query().Get("flash_type")
	}
	return flashMessage{Text: text, Class: class}
}

func openTargetLabel(target string) string {
	switch target {
	case "code":
		return "VS Code"
	case "terminal":
		return "Terminal"
	default:
		return "Files"
	}
}

func pullFlashText(pullErr error) string {
	if db.IsSkippedPullError(pullErr.Error()) {
		return "Pull skipped: " + pullErr.Error()
	}
	return "Pull failed: " + pullErr.Error()
}

func (s *Server) runPluginsAfterChange(repo *config.RepoInfo, updateID int64, notify bool, source string) {
	update, err := s.store.GetUpdate(updateID)
	if err != nil {
		slog.Error("failed to load update for plugins", slog.String("repo", repo.Name), slog.String("err", err.Error()))
		return
	}
	openURL := "http://localhost" + Addr + updateURL(update.ID)
	plugins.RunAfterChange(plugins.Context{
		Ctx:       s.pluginCtx,
		Repo:      repo,
		Update:    update,
		Store:     s.store,
		Notify:    notify,
		Source:    source,
		Dashboard: "http://localhost" + Addr,
		OpenURL:   openURL,
		AppName:   config.AppName,
		Logger:    slog.Default(),
	}, s.storage.GetPluginStates())
}

func (s *Server) runPluginsAfterChangeAsync(repo *config.RepoInfo, updateID int64, notify bool, source string) {
	if repo == nil {
		return
	}
	repoCopy := *repo
	if !s.enqueuePluginTask(func() { s.runPluginsAfterChange(&repoCopy, updateID, notify, source) }) {
		slog.Warn("skipping plugin task during web shutdown", slog.String("repo", repo.Name), slog.Int64("update_id", updateID))
	}
}

func (s *Server) enqueuePluginTask(task func()) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if !s.acceptTasks {
		return false
	}
	s.pluginTasks <- task
	return true
}

func pullFlashType(pullErr error) string {
	if db.IsSkippedPullError(pullErr.Error()) {
		return "skipped"
	}
	return "error"
}

func parseChangedFiles(changes string) []ChangedFile {
	var files []ChangedFile
	for _, line := range strings.Split(changes, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || len(line) < 4 {
			continue
		}
		files = append(files, ChangedFile{
			Status: strings.TrimSpace(line[:2]),
			Path:   strings.TrimSpace(line[3:]),
		})
	}
	return files
}

func performPull(repo *config.RepoInfo) (result, beforeRev, afterRev string, err error) {
	currentBranch, err := git.GetCurrentBranch(repo.Path)
	if err != nil {
		return "", "", "", fmt.Errorf("get current branch: %w", err)
	}
	if currentBranch != repo.DefaultBranch {
		return "", "", "", fmt.Errorf("current branch %s is not default branch %s", currentBranch, repo.DefaultBranch)
	}
	hasChanges, err := git.GitHasUncommitedChanges(repo.Path)
	if err != nil {
		return "", "", "", fmt.Errorf("check changes: %w", err)
	}
	if hasChanges {
		return "", "", "", fmt.Errorf("repository has uncommitted changes")
	}
	beforeRev, _ = git.GitHead(repo.Path)
	result, err = git.GitPull(repo.Path)
	afterRev, _ = git.GitHead(repo.Path)
	return result, beforeRev, afterRev, err
}

func renderTemplate(w http.ResponseWriter, tmpl *template.Template, data any) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		slog.Error("failed to render web template", slog.String("template", tmpl.Name()), slog.String("err", err.Error()))
		http.Error(w, "failed to render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := buf.WriteTo(w); err != nil {
		slog.Error("failed to write web response", slog.String("template", tmpl.Name()), slog.String("err", err.Error()))
	}
}

type activityCell struct {
	Date  string
	Title string
	Count int
	Level int
}

type activitySummary struct {
	Cells       []activityCell
	Total       int
	Start       string
	End         string
	HasActivity bool
}

func (s *Server) activity(repoPath string) (activitySummary, error) {
	loc := moscowLocation()
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := activityStart(today)

	var (
		times []time.Time
		err   error
	)
	if repoPath == "" {
		times, err = s.store.ChangedUpdateTimesSince(start)
	} else {
		times, err = s.store.RepoChangedUpdateTimesSince(repoPath, start)
	}
	if err != nil {
		return activitySummary{}, err
	}
	return newActivitySummary(times, start, today, loc), nil
}

func activityStart(today time.Time) time.Time {
	daysSinceMonday := (int(today.Weekday()) + 6) % 7
	return today.AddDate(0, 0, -((activityWeeks-1)*7 + daysSinceMonday))
}

func newActivitySummary(changedTimes []time.Time, start, end time.Time, loc *time.Location) activitySummary {
	byDate := map[string]activityCell{}
	summary := activitySummary{
		Start: start.Format("Jan 2, 2006"),
		End:   end.Format("Jan 2, 2006"),
	}

	for _, changedAt := range changedTimes {
		day := changedAt.In(loc)
		if day.Before(start) || !day.Before(end.AddDate(0, 0, 1)) {
			continue
		}
		date := day.Format("2006-01-02")
		cell := byDate[date]
		cell.Date = date
		cell.Count++
		byDate[date] = cell

		summary.Total++
	}

	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		cell := byDate[date]
		cell.Date = date
		cell.Level = activityLevel(cell.Count)
		cell.Title = activityTitle(cell)
		summary.Cells = append(summary.Cells, cell)
	}
	summary.HasActivity = summary.Total > 0
	return summary
}

func activityLevel(count int) int {
	switch {
	case count == 0:
		return 0
	case count == 1:
		return 1
	case count <= 3:
		return 2
	case count <= 6:
		return 3
	default:
		return 4
	}
}

func activityTitle(cell activityCell) string {
	if cell.Count == 0 {
		return "No new changes on " + cell.Date
	}
	return plural(cell.Count, "changed update") + " on " + cell.Date
}

type pagination struct {
	Page       int
	TotalPages int
	Total      int
	HasPrev    bool
	HasNext    bool
	PrevURL    string
	NextURL    string
	From       int
	To         int
}

type eventFilter struct {
	Current string
	Options []eventFilterOption
}

type eventFilterOption struct {
	Label string
	URL   string
	Class string
}

func eventFilterFromRequest(r *http.Request) string {
	filter := r.URL.Query().Get("filter")
	switch filter {
	case eventFilterAll, "success", "error", "skipped", "running":
		return filter
	default:
		return eventFilterChanges
	}
}

func updateFilterFromEventFilter(filter string) db.UpdateFilter {
	if filter == eventFilterChanges {
		return db.UpdateFilter{ChangedOnly: true}
	}
	if filter != "" && filter != eventFilterAll {
		return db.UpdateFilter{Status: filter}
	}
	return db.UpdateFilter{}
}

func filterLabel(filter string) string {
	switch filter {
	case eventFilterAll:
		return "All"
	case "success":
		return "Success"
	case "error":
		return "Error"
	case "skipped":
		return "Skipped"
	case "running":
		return "Running"
	default:
		return "Changes"
	}
}

func eventFilterOptions() []string {
	return []string{eventFilterChanges, "success", "error", "skipped", "running", eventFilterAll}
}

func filterQueryValue(filter string) string {
	if filter == eventFilterChanges {
		return ""
	}
	return filter
}

func newEventFilter(path string, values url.Values, current string) eventFilter {
	filter := eventFilter{
		Current: current,
	}
	for _, option := range eventFilterOptions() {
		class := "filter-link"
		if option == current {
			class += " active"
		}
		filter.Options = append(filter.Options, eventFilterOption{
			Label: filterLabel(option),
			URL:   filterURL(path, values, option),
			Class: class,
		})
	}
	return filter
}

func filterURL(path string, values url.Values, filter string) string {
	next := cloneValues(values)
	queryValue := filterQueryValue(filter)
	if queryValue == "" {
		next.Del("filter")
	} else {
		next.Set("filter", queryValue)
	}
	query := queryValues(next)
	if query == "" {
		return path + "#updates"
	}
	return path + "?" + query + "#updates"
}

func filterQueryValues(filter string) url.Values {
	return repoQueryValues("", filter)
}

func repoQueryValues(repoPath, filter string) url.Values {
	values := url.Values{}
	if repoPath != "" {
		values.Set("path", repoPath)
	}
	if queryValue := filterQueryValue(filter); queryValue != "" {
		values.Set("filter", queryValue)
	}
	return values
}

func pageFromRequest(r *http.Request) int {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

func pageOffset(page int) int {
	return (page - 1) * updatesPerPage
}

func clampPage(page, total int) int {
	totalPages := 1
	if total > 0 {
		totalPages = (total + updatesPerPage - 1) / updatesPerPage
	}
	if page > totalPages {
		return totalPages
	}
	return page
}

func newPagination(path string, values url.Values, page, total int) pagination {
	totalPages := 1
	if total > 0 {
		totalPages = (total + updatesPerPage - 1) / updatesPerPage
	}
	if page > totalPages {
		page = totalPages
	}

	p := pagination{
		Page:       page,
		TotalPages: totalPages,
		Total:      total,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}
	if total > 0 {
		p.From = pageOffset(page) + 1
		p.To = pageOffset(page) + updatesPerPage
		if p.To > total {
			p.To = total
		}
	}
	if p.HasPrev {
		p.PrevURL = pageURL(path, values, page-1)
	}
	if p.HasNext {
		p.NextURL = pageURL(path, values, page+1)
	}
	return p
}

func pageURL(path string, values url.Values, page int) string {
	next := cloneValues(values)
	if page > 1 {
		next.Set("page", strconv.Itoa(page))
	}
	query := queryValues(next)
	if query == "" {
		return path + "#updates"
	}
	return path + "?" + query + "#updates"
}

func cloneValues(values url.Values) url.Values {
	next := url.Values{}
	for key, vals := range values {
		for _, val := range vals {
			next.Add(key, val)
		}
	}
	return next
}

func (s *Server) icon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(appIcon)
}

func getServiceStatus() string {
	configPath, err := config.GetConfigPath()
	if err != nil {
		return "unknown"
	}

	status, err := servicepkg.New(configPath, serviceInterval).Status()
	if err != nil {
		return "unknown"
	}
	return status
}

func formatMoscowTime(t time.Time) string {
	return t.In(moscowLocation()).Format("2006-01-02 15:04:05 MSK")
}

func moscowLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err == nil {
		return loc
	}
	return time.FixedZone("MSK", 3*60*60)
}

func humanizeDuration(d time.Duration) string {
	if d < 0 {
		d = -d
		unit := humanDurationUnit(d)
		if unit == "just now" {
			return unit
		}
		return "in " + unit
	}
	unit := humanDurationUnit(d)
	if unit == "just now" {
		return unit
	}
	return unit + " ago"
}

func humanDurationUnit(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 30*24*time.Hour:
		return plural(int(d/(24*time.Hour)), "day")
	case d < 365*24*time.Hour:
		return plural(int(d/(30*24*time.Hour)), "month")
	default:
		return plural(int(d/(365*24*time.Hour)), "year")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func humanizeNumber(n int) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	switch {
	case n >= 1_000_000:
		return sign + compactNumber(n, 1_000_000, "m")
	case n >= 1_000:
		return sign + compactNumber(n, 1_000, "k")
	default:
		return sign + strconv.Itoa(n)
	}
}

func compactNumber(n, unit int, suffix string) string {
	whole := n / unit
	decimal := (n % unit) / (unit / 10)
	if decimal == 0 || whole >= 100 {
		return fmt.Sprintf("%d%s", whole, suffix)
	}
	return fmt.Sprintf("%d.%d%s", whole, decimal, suffix)
}

func compactPath(path string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return path
	}
	homePrefix := strings.TrimRight(homeDir, "/") + "/"
	if strings.HasPrefix(path, homePrefix) {
		return strings.TrimPrefix(path, homePrefix)
	}
	return path
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

var baseCSS = template.CSS(`
	:root {
		color-scheme: light;
		--bg: #ffffff;
		--panel: #ffffff;
		--canvas: #f4f6f8;
		--subtle: #f7f8fa;
		--border: #d6dbe1;
		--border-muted: #e2e6ea;
		--text: #1f2328;
		--muted: #61656f;
		--muted-light: #6e7781;
		--accent: #0969da;
		--accent-hover: #0550ae;
		--danger: #cf222e;
		--success: #1a7f37;
		--shadow: 0 1px 2px rgba(16, 24, 40, .04);
		--shadow-hover: 0 10px 24px rgba(16, 24, 40, .10);
	}
	* { box-sizing: border-box; }
	body { margin: 0; font: 14px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: var(--text); background: var(--canvas); }
	header { background: #20242a; color: #ffffff; border-bottom: 1px solid #161b22; position: sticky; top: 0; z-index: 10; box-shadow: 0 1px 0 rgba(255,255,255,.06) inset; }
	.header-inner { max-width: 1220px; min-height: 66px; margin: 0 auto; padding: 10px 24px; display: flex; align-items: center; justify-content: space-between; gap: 20px; }
	.brand { display: inline-flex; min-width: 0; align-items: center; gap: 10px; color: #ffffff; }
	a.brand:hover { text-decoration: none; }
	.brand-icon { width: 32px; height: 32px; border-radius: 7px; display: block; box-shadow: inset 0 0 0 1px rgba(27, 31, 36, .08); }
	h1, h2, h3 { margin: 0; line-height: 1.2; }
	h1 { font-size: 20px; font-weight: 650; }
	h2 { font-size: 16px; font-weight: 650; }
	h3 { font-size: 15px; margin-bottom: 10px; }
	main { max-width: 1220px; margin: 0 auto; padding: 22px 24px 40px; }
	a { color: var(--accent); text-decoration: none; }
	a:hover { color: var(--accent-hover); text-decoration: underline; }
	.path { color: var(--muted); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow-wrap: anywhere; }
	.header-title { min-width: 0; }
	.header-path { color: #d0d7de; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: min(760px, 62vw); margin-top: 2px; font-size: 12px; }
	.grid { display: grid; gap: 18px; }
	.summary { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px; margin-bottom: 18px; }
	.metric { min-width: 0; background: var(--panel); border: 1px solid var(--border-muted); border-radius: 8px; padding: 14px 15px; box-shadow: var(--shadow); }
	.metric-label { color: var(--muted); font-size: 11px; font-weight: 650; text-transform: uppercase; letter-spacing: .04em; }
	.metric-value { margin-top: 4px; font-size: 20px; font-weight: 650; color: var(--text); }
	.metric-detail { margin-top: 4px; color: var(--muted); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.metric-hash { display: inline-block; max-width: 100%; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow: hidden; text-overflow: ellipsis; vertical-align: bottom; }
	.change-identity { display: grid; grid-template-columns: minmax(180px, 1.1fr) minmax(120px, .5fr) minmax(220px, 1.6fr); gap: 14px; margin-bottom: 18px; }
	.change-field { min-width: 0; background: var(--panel); border: 1px solid var(--border-muted); border-radius: 8px; padding: 14px 15px; box-shadow: var(--shadow); }
	.change-field-value { margin-top: 4px; font-size: 18px; font-weight: 650; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.revision-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
	.revision-item { min-width: 0; border: 1px solid var(--border-muted); border-radius: 8px; padding: 11px 12px; background: var(--subtle); }
	.revision-hash { display: block; margin-top: 4px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-weight: 650; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.context-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 10px; margin-bottom: 12px; }
	.context-item { min-width: 0; border: 1px solid var(--border-muted); border-radius: 8px; padding: 9px 10px; background: var(--subtle); }
	.context-value { margin-top: 3px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 650; }
	.panel { background: var(--panel); border: 1px solid var(--border-muted); border-radius: 8px; overflow: hidden; box-shadow: var(--shadow); }
	.panel:hover { border-color: #d0d7de; }
	.panel-head { padding: 13px 16px; border-bottom: 1px solid var(--border-muted); background: var(--panel); display: flex; justify-content: space-between; gap: 14px; align-items: center; }
	.panel-title { color: var(--text); }
	.panel-title:hover { color: var(--accent); text-decoration: none; }
	.panel-body { padding: 15px 16px 17px; }
	.flash { margin-bottom: 18px; padding: 10px 12px; border: 1px solid #b6d7f2; border-radius: 8px; background: #e8f2fc; color: #0550ae; font-weight: 600; box-shadow: var(--shadow); }
	.flash.success { background: #dafbe1; border-color: #aceebb; color: #116329; }
	.flash.error { background: #ffebe9; border-color: #ffcecb; color: var(--danger); }
	.flash.skipped { background: #fff8c5; border-color: #fae17d; color: #9a6700; }
	.flash.running { background: #ddf4ff; border-color: #b6e3ff; color: var(--accent); }
	.flash.info { background: #e8f2fc; border-color: #b6d7f2; color: #0550ae; }
	.filter { display: inline-flex; gap: 2px; padding: 2px; border: 1px solid var(--border-muted); border-radius: 8px; background: var(--subtle); }
	.filter-link { display: inline-flex; min-height: 26px; align-items: center; padding: 3px 9px; border: 0; border-radius: 6px; background: transparent; color: var(--muted); font: inherit; font-size: 12px; font-weight: 600; cursor: pointer; }
	.filter-link:hover { color: var(--text); background: var(--subtle); text-decoration: none; }
	.filter-link.active { background: var(--accent); color: #ffffff; }
	.activity-wrap { overflow-x: auto; overflow-y: visible; margin: -2px; padding: 2px 160px 34px; }
	.activity-grid { display: grid; grid-auto-flow: column; grid-auto-columns: 12px; grid-template-rows: repeat(7, 12px); gap: 3px; width: max-content; padding: 2px; }
	.activity-cell { position: relative; width: 12px; height: 12px; border-radius: 2px; background: #ebedf0; border: 1px solid rgba(27,31,36,.06); }
	.activity-cell:hover, .activity-cell:focus-visible { outline: 1px solid var(--muted); outline-offset: 1px; }
	.activity-cell[data-title]:hover::after, .activity-cell[data-title]:focus-visible::after { content: attr(data-title); position: absolute; z-index: 2; top: 18px; left: 50%; transform: translateX(-50%); padding: 5px 8px; border-radius: 6px; background: #24292f; color: white; font-size: 12px; line-height: 1.2; white-space: nowrap; box-shadow: 0 8px 24px rgba(27,31,36,.18); pointer-events: none; }
	.activity-cell[data-level="1"] { background: #9be9a8; }
	.activity-cell[data-level="2"] { background: #40c463; }
	.activity-cell[data-level="3"] { background: #30a14e; }
	.activity-cell[data-level="4"] { background: #216e39; }
	.activity-meta { display: flex; align-items: center; justify-content: space-between; gap: 14px; margin-top: 12px; color: var(--muted); font-size: 12px; }
	.activity-legend { display: flex; align-items: center; gap: 5px; white-space: nowrap; }
	.activity-legend .activity-cell { display: inline-block; flex: 0 0 auto; }
	.pagination { padding: 11px 16px; display: flex; align-items: center; justify-content: space-between; gap: 12px; border-top: 1px solid var(--border-muted); background: var(--panel); }
	.pagination-info { color: var(--muted); font-size: 12px; }
	.pagination-actions, .actions { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
	.page-link, .button { display: inline-flex; align-items: center; justify-content: center; min-height: 28px; padding: 4px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--panel); color: var(--text); font: inherit; font-size: 12px; font-weight: 600; cursor: pointer; box-shadow: var(--shadow); }
	.page-link:hover, .button:hover { background: var(--subtle); border-color: #b8c0ca; text-decoration: none; color: var(--text); }
	.page-link.disabled { color: #8c959f; background: var(--subtle); pointer-events: none; box-shadow: none; }
	.button.danger { color: var(--danger); }
	.button.quiet { color: var(--muted); }
	.button.warn { color: #9a6700; background: #fff8c5; border-color: #fae17d; }
	.button.warn:hover { background: #fff1a8; border-color: #d4a72c; }
	.button.primary { background: var(--accent); border-color: var(--accent); color: #ffffff; }
	.button.primary:hover { background: var(--accent-hover); border-color: var(--accent-hover); color: #ffffff; }
	.action-form { display: inline-flex; align-items: center; gap: 8px; margin: 0; }
	.toolbar { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-bottom: 14px; flex-wrap: wrap; }
	.toolbar-group { display: inline-flex; align-items: center; gap: 8px; flex-wrap: wrap; }
	.plugin-list { display: grid; gap: 14px; }
	.plugin-card { padding: 0; overflow: hidden; }
	.plugin-card:hover { transform: none; }
	.plugin-card-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; padding: 14px 16px 12px; border-bottom: 1px solid var(--border-muted); background: var(--subtle); }
	.plugin-card-title { display: flex; align-items: center; gap: 8px; margin-bottom: 4px; }
	.plugin-card-title strong { font-size: 14px; }
	.plugin-description { color: var(--muted); font-size: 12px; }
	.plugin-enable { flex: 0 0 auto; padding-top: 1px; }
	.plugin-card-body { padding: 14px 16px 16px; }
	.plugin-settings { display: grid; grid-template-columns: repeat(12, minmax(0, 1fr)); gap: 12px; margin-top: 14px; }
	.plugin-section-title { margin-top: 14px; color: var(--text); font-size: 12px; font-weight: 700; }
	.plugin-advanced { margin-top: 14px; border: 1px solid var(--border-muted); border-radius: 9px; background: var(--subtle); }
	.plugin-advanced > summary { display: flex; justify-content: space-between; gap: 12px; padding: 10px 12px; cursor: pointer; color: var(--text); font-size: 12px; font-weight: 700; }
	.plugin-advanced > summary span { color: var(--muted); font-weight: 500; }
	.plugin-advanced[open] > summary { border-bottom: 1px solid var(--border-muted); }
	.plugin-advanced .plugin-settings { margin: 0; padding: 12px; background: #fff; border-radius: 0 0 9px 9px; }
	.plugin-field { display: grid; grid-column: span 4; gap: 6px; min-width: 0; margin: 0; }
	.plugin-field-label { display: flex; align-items: center; gap: 5px; min-height: 20px; color: var(--muted); font-size: 11px; font-weight: 650; }
	.plugin-help { display: inline-flex; align-items: center; justify-content: center; width: 16px; height: 16px; border: 1px solid var(--border); border-radius: 50%; color: var(--muted); font-size: 10px; cursor: help; }
	.plugin-field .input { width: 100%; }
	.plugin-field-prompt { grid-column: span 12; }
	.plugin-field-url, .plugin-field-token, .plugin-field-include_patterns, .plugin-field-exclude_patterns { grid-column: span 6; }
	.plugin-field-max_context_bytes, .plugin-field-max_file_diff_bytes { grid-column: span 3; }
	.plugin-field textarea.input { min-height: 76px; }
	.plugin-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 14px; padding-top: 12px; border-top: 1px solid var(--border-muted); }
	.search { width: min(360px, 100%); }
	.repo-select { margin-right: 8px; accent-color: var(--accent); }
	.select-all { display: inline-flex; align-items: center; gap: 6px; color: var(--muted); font-size: 12px; font-weight: 600; user-select: none; }
	.button:disabled { opacity: .52; cursor: not-allowed; background: var(--subtle); box-shadow: none; }
	.input { width: 76px; min-height: 28px; padding: 4px 8px; border: 1px solid var(--border); border-radius: 8px; background: #ffffff; color: var(--text); font: inherit; box-shadow: inset 0 1px 0 rgba(0, 0, 0, .03); }
	.input.wide { width: min(360px, 100%); }
	textarea.input { min-height: 110px; resize: vertical; font: 12px/1.45 ui-monospace, SFMono-Regular, Menlo, monospace; }
	.input:focus, .button:focus-visible, .page-link:focus-visible, .filter-link:focus-visible, a.repo:focus-visible { outline: 2px solid rgba(9, 105, 218, .35); outline-offset: 2px; }
	.input.confirm { width: 220px; }
	.repo-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 14px; }
	.repo { display: block; min-width: 0; border: 1px solid var(--border-muted); border-radius: 8px; padding: 13px; background: var(--panel); color: inherit; box-shadow: var(--shadow); transition: border-color .12s ease, box-shadow .12s ease, transform .12s ease; }
	.repo:hover { border-color: #8c959f; box-shadow: var(--shadow-hover); transform: translateY(-1px); text-decoration: none; }
	.repo-title { display: flex; justify-content: space-between; gap: 10px; align-items: center; margin-bottom: 8px; min-width: 0; }
	.repo-title strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 650; }
	.repo-detail { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	table { width: 100%; border-collapse: collapse; }
	th, td { padding: 9px 12px; border-bottom: 1px solid var(--border-muted); text-align: left; vertical-align: top; }
	th { color: var(--muted); font-size: 11px; background: var(--subtle); font-weight: 650; text-transform: uppercase; letter-spacing: .04em; }
	tr:hover td { background: var(--subtle); }
	td pre { margin: 0; max-height: 160px; }
	.update-preview { max-height: 12.5em; white-space: pre-wrap; }
	.update-preview-link { display: block; color: inherit; }
	.update-preview-link:hover { color: inherit; text-decoration: none; }
	pre { white-space: pre-wrap; background: var(--subtle); border: 1px solid var(--border-muted); border-radius: 8px; padding: 10px 12px; overflow: auto; font: 12px/1.45 ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--text); }
	details.disclosure { border: 1px solid var(--border-muted); border-radius: 8px; background: var(--panel); margin-bottom: 10px; overflow: hidden; }
	details.disclosure > summary { cursor: pointer; padding: 10px 12px; color: var(--text); font-weight: 650; background: var(--subtle); list-style: none; }
	details.disclosure > summary::-webkit-details-marker { display: none; }
	details.disclosure > summary::before { content: "Show"; display: inline-flex; align-items: center; min-height: 22px; margin-right: 8px; padding: 2px 8px; border: 1px solid var(--border); border-radius: 999px; background: #ffffff; color: var(--muted); font-size: 12px; font-weight: 650; }
	details.disclosure[open] > summary::before { content: "Hide"; }
	details.disclosure pre { margin: 0; border: 0; border-top: 1px solid var(--border-muted); border-radius: 0; max-height: 520px; }
	.badge { display: inline-flex; align-items: center; min-height: 22px; padding: 2px 8px; border: 1px solid transparent; border-radius: 999px; background: #eaeef2; color: var(--text); font-size: 12px; font-weight: 600; white-space: nowrap; }
	.badge.success { background: #dafbe1; border-color: #aceebb; color: #116329; }
	.badge.error { background: #ffebe9; border-color: #ffcecb; color: var(--danger); }
	.badge.running { background: #ddf4ff; border-color: #b6e3ff; color: var(--accent); }
	.badge.skipped { background: #fff8c5; border-color: #fae17d; color: #9a6700; }
	.badge.paused { background: #eaeef2; border-color: var(--border); color: var(--muted); }
	.badge.service-running { background: #dafbe1; border-color: #aceebb; color: #116329; }
	.badge.service-stopped { background: #ffebe9; border-color: #ffcecb; color: var(--danger); }
	.badge.service-loaded { background: #ddf4ff; border-color: #b6e3ff; color: var(--accent); }
	.changed-icon { display: inline-flex; align-items: center; justify-content: center; width: 24px; height: 24px; color: #116329; font-size: 16px; font-weight: 800; }
	.changed-icon.empty { color: var(--muted); font-weight: 600; }
	.time { white-space: nowrap; }
	.time-detail { color: var(--muted); font-size: 12px; margin-top: 2px; white-space: nowrap; }
	.plugin-repos { color: var(--muted); font-size: 12px; margin-top: 6px; min-width: 0; overflow-wrap: anywhere; }
	.plugin-repo-list { display: inline-flex; flex-wrap: wrap; gap: 4px; vertical-align: middle; max-width: 100%; }
	.plugin-repo-chip { display: inline-flex; align-items: center; gap: 4px; min-width: 0; max-width: 100%; padding: 2px 4px 2px 6px; border: 1px solid var(--border-muted); border-radius: 999px; background: var(--subtle); }
	.plugin-repo-path { min-width: 0; max-width: 100%; color: var(--muted); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow-wrap: anywhere; word-break: break-word; }
	.chip-remove { display: inline-flex; align-items: center; justify-content: center; width: 18px; height: 18px; border: 1px solid var(--border); border-radius: 999px; background: #ffffff; color: var(--muted); font: inherit; font-size: 11px; font-weight: 700; cursor: pointer; padding: 0; }
	.chip-remove:hover { color: var(--danger); border-color: #ffcecb; background: #ffebe9; }
	.empty { color: var(--muted); padding: 18px; }
	footer { max-width: 1220px; margin: 0 auto; padding: 0 24px 28px; color: var(--muted); font-size: 12px; }
	@media (max-width: 760px) {
		.header-inner { display: grid; align-items: start; }
		.action-form { width: 100%; }
		.summary { grid-template-columns: 1fr; }
		.change-identity { grid-template-columns: 1fr; }
		.revision-grid { grid-template-columns: 1fr; }
		.context-grid { grid-template-columns: 1fr; }
		.repo-list { grid-template-columns: 1fr; }
		.plugin-field, .plugin-field-url, .plugin-field-token, .plugin-field-include_patterns, .plugin-field-exclude_patterns, .plugin-field-max_context_bytes, .plugin-field-max_file_diff_bytes { grid-column: span 12; }
		.plugin-card-head { align-items: center; }
		.activity-meta { align-items: flex-start; flex-direction: column; }
		th:nth-child(4), td:nth-child(4) { display: none; }
	}
`)

var templateFuncs = template.FuncMap{
	"statusClass": func(status string) string {
		switch status {
		case "success", "error", "running", "skipped":
			return status
		default:
			return ""
		}
	},
	"serviceStatusClass": func(status string) string {
		switch status {
		case "running":
			return "service-running"
		case "not running", "not installed", "unknown":
			return "service-stopped"
		case "loaded but not running":
			return "service-loaded"
		default:
			return ""
		}
	},
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return formatMoscowTime(t)
	},
	"humanTime": func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return humanizeDuration(time.Since(t))
	},
	"humanDuration": humanDurationUnit,
	"join":          strings.Join,
	"humanNumber":   humanizeNumber,
	"basicFields": func(fields []plugins.Field) []plugins.Field {
		out := make([]plugins.Field, 0, len(fields))
		for _, field := range fields {
			if !field.Advanced {
				out = append(out, field)
			}
		}
		return out
	},
	"advancedFields": func(fields []plugins.Field) []plugins.Field {
		out := make([]plugins.Field, 0, len(fields))
		for _, field := range fields {
			if field.Advanced {
				out = append(out, field)
			}
		}
		return out
	},
	"updateURL": updateURL,
	"shortHash": func(hash string) string {
		if len(hash) <= 12 {
			return hash
		}
		return hash[:12]
	},
	"skipReasonLabel": func(reason string) string {
		switch reason {
		case "dirty_worktree":
			return "Uncommitted changes"
		case "not_default_branch":
			return "Not on default branch"
		case "paused":
			return "Paused"
		default:
			return reason
		}
	},
	"firstLine":           firstLine,
	"compactPath":         compactPath,
	"pluginResultMessage": pluginResultMessage,
	"configValue": func(values map[string]string, key string) string {
		if values == nil {
			return ""
		}
		return values[key]
	},
	"inputType": func(fieldType string) string {
		switch fieldType {
		case "password", "url":
			return fieldType
		default:
			return "text"
		}
	},
}

const activityTemplate = `{{define "activity"}}<div class="activity-wrap"><div class="activity-grid" aria-label="Changed update activity from {{.Start}} to {{.End}}">{{range .Cells}}<span class="activity-cell" data-level="{{.Level}}" data-title="{{.Title}}" aria-label="{{.Title}}" tabindex="0"></span>{{end}}</div></div><div class="activity-meta"><div>{{humanNumber .Total}} changed updates in the last year</div><div class="activity-legend"><span>Less</span><span class="activity-cell" data-level="0"></span><span class="activity-cell" data-level="1"></span><span class="activity-cell" data-level="2"></span><span class="activity-cell" data-level="3"></span><span class="activity-cell" data-level="4"></span><span>More</span></div></div>{{end}}`

var indexTemplate = template.Must(template.New("index").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><h1>autogitpull</h1></a><div class="actions"><a class="button" href="/status">Status</a><a class="button" href="/plugins">Plugins</a><a class="button" href="/settings">Settings</a></div></div></header><main>
{{if .Flash.Text}}<div class="flash {{.Flash.Class}}">{{.Flash.Text}}</div>{{end}}
<section class="summary">
	<div class="metric"><div class="metric-label">Repositories</div><div class="metric-value">{{humanNumber .RepoCount}}</div></div>
	<div class="metric"><div class="metric-label">Recent events</div><div class="metric-value">{{humanNumber .TotalUpdates}}</div></div>
	<div class="metric"><div class="metric-label">Plugins</div><div class="metric-value">{{humanNumber .PluginSummary.Enabled}}/{{humanNumber .PluginSummary.Total}}</div><div class="metric-detail"><a href="/plugins">Configure plugins</a></div></div>
</section>
<div class="grid">
<section class="panel" id="activity"><div class="panel-head"><h2><a class="panel-title" href="#activity">Activity</a></h2></div><div class="panel-body">
{{template "activity" .Activity}}
</div></section>
<section class="panel" id="repositories"><div class="panel-head"><h2><a class="panel-title" href="#repositories">Repositories</a></h2></div><div class="panel-body">
{{if .RepoCards}}<form id="bulk-selected-form" method="post" action="/repos/bulk"><div class="toolbar"><div class="toolbar-group"><input id="repo-search" class="input search" placeholder="Search repositories" autocomplete="off"><div class="filter" id="repo-status-filter"><button class="filter-link active" type="button" data-status="all">All</button><button class="filter-link" type="button" data-status="error">Error</button><button class="filter-link" type="button" data-status="skipped">Skipped</button><button class="filter-link" type="button" data-status="paused">Paused</button><button class="filter-link" type="button" data-status="changed">Changed</button></div></div><div class="toolbar-group"><label class="select-all"><input id="repo-select-all" type="checkbox"> Select all</label><button class="button primary" name="action" value="pull_all" type="submit">Pull all</button><button class="button" name="action" value="pause_errors" type="submit">Pause errors</button><button class="button warn" id="pause-selected" name="action" value="pause_selected" type="submit" disabled>Pause selected</button><button class="button" id="resume-selected" name="action" value="resume_selected" type="submit" disabled>Resume selected</button></div></div><div class="repo-list" id="repo-list">{{range .RepoCards}}<div class="repo" data-name="{{.Repo.Name}}" data-path="{{.Repo.Path}}" data-status="{{if .Repo.Paused}}paused{{else if .LastUpdate}}{{.LastUpdate.Status}}{{else}}none{{end}}" data-changed="{{if .LastUpdate}}{{.LastUpdate.Changed}}{{else}}false{{end}}" title="{{.Repo.Path}}"><input class="repo-select" type="checkbox" name="repo" value="{{.Repo.Path}}"><a href="/repo?path={{.Repo.Path | urlquery}}"><div class="repo-title"><strong>{{.Repo.Name}}</strong><span class="actions">{{if .Repo.Paused}}<span class="badge paused">paused</span>{{end}}{{if not .Repo.NotificationsEnabled}}<span class="badge paused">muted</span>{{end}}{{if .LastUpdate}}<span class="badge {{statusClass .LastUpdate.Status}}">{{.LastUpdate.Status}}</span>{{end}}<span class="badge">{{.Repo.DefaultBranch}}</span></span></div><div class="path repo-detail">{{compactPath .Repo.Path}}</div><div class="time-detail repo-detail" title="{{if .LastUpdate}}{{if .LastUpdate.Error}}{{.LastUpdate.Error | firstLine}}{{else}}{{.LastUpdate.Result | firstLine}}{{end}}{{end}}">{{if .LastUpdate}}Last event: {{humanTime .LastUpdate.StartedAt}} · {{if .LastUpdate.Error}}{{.LastUpdate.Error | firstLine}}{{else}}{{.LastUpdate.Result | firstLine}}{{end}}{{else}}No recorded events{{end}}</div><div class="time-detail repo-detail">Last sync: {{humanTime .Repo.LastSync}} · {{formatTime .Repo.LastSync}}</div></a></div>{{end}}</div></form>{{else}}<div class="empty">No repositories registered.</div>{{end}}
</div></section>
<section class="panel" id="updates"><div class="panel-head"><h2><a class="panel-title" href="#updates">Recent updates</a></h2><div class="filter">{{range .EventFilter.Options}}<a class="{{.Class}}" href="{{.URL}}">{{.Label}}</a>{{end}}</div></div>
{{if .Updates}}<table><tr><th>Time</th><th>Repo</th><th>Status</th><th>Result</th></tr>
{{range .Updates}}<tr><td><a href="{{updateURL .ID}}"><div class="time">{{humanTime .StartedAt}}</div><div class="time-detail">{{formatTime .StartedAt}}</div></a></td><td><a href="/repo?path={{.RepoPath | urlquery}}">{{.RepoName}}</a><div class="path" title="{{.RepoPath}}">{{compactPath .RepoPath}}</div></td><td><span class="badge {{statusClass .Status}}">{{.Status}}</span></td><td><a class="update-preview-link" href="{{updateURL .ID}}"><pre class="update-preview">{{if .Error}}{{.Error}}{{else}}{{.Result}}{{end}}</pre></a></td></tr>{{end}}
</table>{{template "pagination" .Pagination}}{{else}}<div class="empty">No updates match this filter.</div>{{end}}</section>
</div>
</main><footer>version {{.AppVersion}}</footer><script>
(function(){
  const search = document.getElementById('repo-search');
  const list = document.getElementById('repo-list');
  const selectAll = document.getElementById('repo-select-all');
  const resumeSelected = document.getElementById('resume-selected');
  const pauseSelected = document.getElementById('pause-selected');
  let status = 'all';
  function visibleCards(){
    return list ? Array.from(list.querySelectorAll('.repo')).filter(card => card.style.display !== 'none') : [];
  }
  function updateBulkState(){
    const checks = list ? Array.from(list.querySelectorAll('.repo-select')) : [];
    const checked = checks.filter(x => x.checked).length;
    if (resumeSelected) resumeSelected.disabled = checked === 0;
    if (pauseSelected) pauseSelected.disabled = checked === 0;
    if (selectAll) {
      const visible = visibleCards().map(card => card.querySelector('.repo-select')).filter(Boolean);
      selectAll.checked = visible.length > 0 && visible.every(x => x.checked);
      selectAll.indeterminate = visible.some(x => x.checked) && !selectAll.checked;
    }
  }
  function applyFilters(){
    if (!list) return;
    const q = (search && search.value || '').toLowerCase();
    for (const card of list.querySelectorAll('.repo')) {
      const text = ((card.dataset.name || '') + ' ' + (card.dataset.path || '')).toLowerCase();
      const statusOk = status === 'all' || card.dataset.status === status || (status === 'changed' && card.dataset.changed === 'true');
      card.style.display = text.includes(q) && statusOk ? '' : 'none';
    }
    updateBulkState();
  }
  if (search) search.addEventListener('input', applyFilters);
  if (list) list.addEventListener('change', e => { if (e.target.classList.contains('repo-select')) updateBulkState(); });
  if (selectAll) selectAll.addEventListener('change', () => {
    visibleCards().forEach(card => {
      const checkbox = card.querySelector('.repo-select');
      if (checkbox) checkbox.checked = selectAll.checked;
    });
    updateBulkState();
  });
  document.querySelectorAll('#repo-status-filter [data-status]').forEach(btn => btn.addEventListener('click', () => {
    status = btn.dataset.status;
    document.querySelectorAll('#repo-status-filter .filter-link').forEach(x => x.classList.remove('active'));
    btn.classList.add('active');
    applyFilters();
  }));
  updateBulkState();
  document.querySelectorAll('form').forEach(form => form.addEventListener('submit', () => {
    const submitter = document.activeElement;
    if (submitter && submitter.tagName === 'BUTTON') submitter.textContent = 'Working...';
  }));
  setTimeout(() => {
    if (!document.hidden && document.activeElement.tagName !== 'INPUT') location.reload();
  }, 60000);
})();
</script></body></html>

{{define "pagination"}}<div class="pagination"><div class="pagination-info">{{if .Total}}Showing {{humanNumber .From}}-{{humanNumber .To}} of {{humanNumber .Total}} · page {{humanNumber .Page}} of {{humanNumber .TotalPages}}{{else}}No records{{end}}</div><div class="pagination-actions">{{if .HasPrev}}<a class="page-link" href="{{.PrevURL}}">Prev</a>{{else}}<span class="page-link disabled">Prev</span>{{end}}{{if .HasNext}}<a class="page-link" href="{{.NextURL}}">Next</a>{{else}}<span class="page-link disabled">Next</span>{{end}}</div></div>{{end}}` + activityTemplate))

var repoTemplate = template.Must(template.New("repo").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>{{.Repo.Name}} - autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><div class="header-title"><h1>{{.Repo.Name}}</h1><div class="header-path" title="{{.Repo.Path}}">{{compactPath .Repo.Path}}</div></div></a><div class="actions"><form class="action-form" method="post" action="/repo/pull"><input type="hidden" name="path" value="{{.Repo.Path}}"><button class="button primary" type="submit">Pull now</button></form><form class="action-form" method="post" action="/repo/ai-summary"><input type="hidden" name="path" value="{{.Repo.Path}}"><button class="button" type="submit">Run AI summary</button></form><form class="action-form" method="post" action="/repo/open"><input type="hidden" name="path" value="{{.Repo.Path}}"><input type="hidden" name="target" value="finder"><button class="button" type="submit">Files</button></form><form class="action-form" method="post" action="/repo/open"><input type="hidden" name="path" value="{{.Repo.Path}}"><input type="hidden" name="target" value="terminal"><button class="button" type="submit">Terminal</button></form><form class="action-form" method="post" action="/repo/open"><input type="hidden" name="path" value="{{.Repo.Path}}"><input type="hidden" name="target" value="code"><button class="button" type="submit">VS Code</button></form><form class="action-form" method="post" action="/repo/notify"><input type="hidden" name="path" value="{{.Repo.Path}}">{{if .Repo.NotificationsEnabled}}<input type="hidden" name="notify" value="0"><button class="button quiet" type="submit">Mute notifications</button>{{else}}<input type="hidden" name="notify" value="1"><button class="button quiet" type="submit">Enable notifications</button>{{end}}</form><form class="action-form" method="post" action="/repo/pause"><input type="hidden" name="path" value="{{.Repo.Path}}">{{if .Repo.Paused}}<input type="hidden" name="paused" value="0"><button class="button" type="submit">Resume auto-pull</button>{{else}}<input type="hidden" name="paused" value="1"><button class="button warn" type="submit">Pause auto-pull</button>{{end}}</form><a class="button" href="/plugins">Plugins</a><a class="button danger" href="/repo/unregister?path={{.Repo.Path | urlquery}}">Unregister</a><a class="badge" href="/">Back</a></div></div></header><main class="grid">
{{if .Flash.Text}}<div class="flash {{.Flash.Class}}">{{.Flash.Text}}</div>{{end}}
<section class="summary">
	<div class="metric"><div class="metric-label">Default branch</div><div class="metric-value">{{.Repo.DefaultBranch}}</div></div>
	{{if .RemoteURL}}<div class="metric"><div class="metric-label">Remote repository</div><div class="metric-value"><a href="{{.RemoteURL}}" target="_blank" rel="noopener noreferrer">Open repository</a></div></div>{{end}}
	<div class="metric"><div class="metric-label">Last sync</div><div class="metric-value">{{humanTime .Repo.LastSync}}</div><div class="metric-detail">{{formatTime .Repo.LastSync}}</div></div>
	<div class="metric"><div class="metric-label">Recorded events</div><div class="metric-value">{{humanNumber .TotalUpdates}}</div></div>
	<div class="metric"><div class="metric-label">Auto pull</div><div class="metric-value">{{if .Repo.Paused}}<span class="badge paused">paused</span>{{else}}<span class="badge success">enabled</span>{{end}}</div></div>
	<div class="metric"><div class="metric-label">Notifications</div><div class="metric-value">{{if .Repo.NotificationsEnabled}}<span class="badge success">enabled</span>{{else}}<span class="badge paused">muted</span>{{end}}</div></div>
</section>
<section class="panel" id="plugins"><div class="panel-head"><h2><a class="panel-title" href="#plugins">Plugins</a></h2><a class="filter-link" href="/plugins">Settings</a></div><div class="panel-body">
{{if .PluginControls}}<table><tr><th>Plugin</th><th>Status</th><th>Action</th></tr>{{range .PluginControls}}<tr><td>{{.Name}}</td><td><span class="badge {{.StatusClass}}">{{.Status}}</span></td><td><form class="action-form" method="post" action="/repo/plugin-toggle"><input type="hidden" name="path" value="{{$.Repo.Path}}"><input type="hidden" name="plugin_id" value="{{.ID}}"><input type="hidden" name="enabled" value="{{if .NextEnabled}}1{{else}}0{{end}}"><button class="button {{if not .NextEnabled}}quiet{{end}}" type="submit">{{.Action}}</button></form></td></tr>{{end}}</table>{{else}}<div class="empty">No plugins available.</div>{{end}}
</div></section>
<section class="panel" id="activity"><div class="panel-head"><h2><a class="panel-title" href="#activity">Activity</a></h2></div><div class="panel-body">
{{template "activity" .Activity}}
</div></section>
<section class="panel" id="changes"><div class="panel-head"><h2><a class="panel-title" href="#changes">Current local changes</a></h2></div><div class="panel-body">{{if .ChangedFiles}}<table><tr><th>Status</th><th>File</th></tr>{{range .ChangedFiles}}<tr><td><span class="badge paused">{{.Status}}</span></td><td><span class="path">{{.Path}}</span></td></tr>{{end}}</table>{{else}}<div class="empty">No uncommitted changes</div>{{end}}</div></section>
<section class="panel" id="updates"><div class="panel-head"><h2><a class="panel-title" href="#updates">Updates</a></h2><div class="filter">{{range .EventFilter.Options}}<a class="{{.Class}}" href="{{.URL}}">{{.Label}}</a>{{end}}</div></div>
{{if .Updates}}<table><tr><th>Time</th><th>Status</th><th>Changed</th><th>Result</th></tr>
{{range .Updates}}<tr><td><a href="{{updateURL .ID}}"><div class="time">{{humanTime .StartedAt}}</div><div class="time-detail">{{formatTime .StartedAt}}</div></a></td><td><span class="badge {{statusClass .Status}}">{{.Status}}</span>{{if .SkipReason}}<div class="time-detail">{{skipReasonLabel .SkipReason}}</div>{{end}}</td><td>{{if .Changed}}<span class="changed-icon" title="Changed" aria-label="Changed">&#10003;</span>{{else}}<span class="changed-icon empty" title="No changes" aria-label="No changes">-</span>{{end}}</td><td><a href="{{updateURL .ID}}"><pre>{{if .Error}}{{.Error}}{{else}}{{.Result}}{{end}}</pre></a></td></tr>{{end}}
</table>{{template "pagination" .Pagination}}{{else}}<div class="empty">No updates match this filter.</div>{{end}}</section>
</main><script>document.querySelectorAll('form').forEach(form => form.addEventListener('submit', () => { const b = document.activeElement; if (b && b.tagName === 'BUTTON') b.textContent = 'Working...'; }));</script></body></html>

{{define "pagination"}}<div class="pagination"><div class="pagination-info">{{if .Total}}Showing {{humanNumber .From}}-{{humanNumber .To}} of {{humanNumber .Total}} · page {{humanNumber .Page}} of {{humanNumber .TotalPages}}{{else}}No records{{end}}</div><div class="pagination-actions">{{if .HasPrev}}<a class="page-link" href="{{.PrevURL}}">Prev</a>{{else}}<span class="page-link disabled">Prev</span>{{end}}{{if .HasNext}}<a class="page-link" href="{{.NextURL}}">Next</a>{{else}}<span class="page-link disabled">Next</span>{{end}}</div></div>{{end}}` + activityTemplate))

var pluginsTemplate = template.Must(template.New("plugins").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>Plugins - autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><h1>Plugins</h1></a><a class="badge" href="/">Back</a></div></header><main class="grid">
{{if .Flash.Text}}<div class="flash {{.Flash.Class}}">{{.Flash.Text}}</div>{{end}}
<section class="panel"><div class="panel-head"><h2>Change plugins</h2></div><div class="panel-body">
{{if .Plugins}}<div class="plugin-list">{{range .Plugins}}{{$plugin := .}}<form class="repo plugin-card" method="post" action="/plugins/save">
	<input type="hidden" name="id" value="{{.ID}}">
	<div class="plugin-card-head"><div><div class="plugin-card-title"><strong>{{.Name}}</strong>{{if .Enabled}}<span class="badge success">enabled</span>{{else}}<span class="badge paused">disabled</span>{{end}}</div><div class="plugin-description">{{.Description}}</div></div><label class="select-all plugin-enable"><input type="checkbox" name="enabled" value="1" {{if .Enabled}}checked{{end}}> Enabled</label></div>
	<div class="plugin-card-body"><div class="plugin-repos">Selected repos: {{if .SelectedRepos}}<span class="plugin-repo-list">{{range .SelectedRepos}}<span class="plugin-repo-chip"><span class="plugin-repo-path" title="{{.}}">{{compactPath .}}</span><button class="chip-remove" type="submit" formaction="/plugins/remove-repo" formmethod="post" name="repo" value="{{.}}" title="Remove selected repo" aria-label="Remove selected repo">x</button></span>{{end}}</span>{{else}}none{{end}}</div>
	<div class="plugin-section-title">Settings</div><div class="plugin-settings">{{range basicFields .Fields}}{{$field := .}}<label class="plugin-field plugin-field-{{.Key}}"><span class="plugin-field-label">{{.Label}}{{if .Help}}<span class="plugin-help" title="{{.Help}}" aria-label="{{.Help}}">?</span>{{end}}</span>{{if eq .Type "select"}}<select class="input" name="config_{{.Key}}">{{range .Options}}<option value="{{.Value}}" {{if eq (configValue $plugin.Config $field.Key) .Value}}selected{{end}}>{{.Label}}</option>{{end}}</select>{{else if eq .Type "textarea"}}<textarea class="input" name="config_{{.Key}}">{{configValue $plugin.Config .Key}}</textarea>{{else}}<input class="input" type="{{inputType .Type}}" name="config_{{.Key}}" value="{{configValue $plugin.Config .Key}}">{{end}}</label>{{end}}</div>
	{{with advancedFields .Fields}}<details class="plugin-advanced"><summary>Advanced context controls <span>File filters and size limits</span></summary><div class="plugin-settings">{{range .}}{{$field := .}}<label class="plugin-field plugin-field-{{.Key}}"><span class="plugin-field-label">{{.Label}}{{if .Help}}<span class="plugin-help" title="{{.Help}}" aria-label="{{.Help}}">?</span>{{end}}</span>{{if eq .Type "select"}}<select class="input" name="config_{{.Key}}">{{range .Options}}<option value="{{.Value}}" {{if eq (configValue $plugin.Config $field.Key) .Value}}selected{{end}}>{{.Label}}</option>{{end}}</select>{{else}}<input class="input" type="{{inputType .Type}}" name="config_{{.Key}}" value="{{configValue $plugin.Config .Key}}">{{end}}</label>{{end}}</div></details>{{end}}
	<div class="plugin-actions"><button class="button primary" type="submit">Save</button>{{if eq .ID "ai_summary"}}<button class="button" type="submit" formaction="/plugins/test-ai-summary">Test connection</button>{{else if eq .ID "notifications"}}<button class="button" type="submit" formaction="/plugins/test-notifications">Send test notification</button>{{end}}</div></div>
	</form>{{end}}</div>{{else}}<div class="empty">No plugins available.</div>{{end}}
</div></section>
</main><script>document.querySelectorAll('form').forEach(form => form.addEventListener('submit', () => { const b = document.activeElement; if (b && b.tagName === 'BUTTON') b.textContent = b.formAction && b.formAction.includes('/plugins/test-') ? 'Testing...' : 'Saving...'; }));</script></body></html>`))

var updateTemplate = template.Must(template.New("update").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>Change {{.Update.ID}} - autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><div class="header-title"><h1>Change {{.Update.ID}}</h1><div class="header-path" title="{{.Update.RepoPath}}">{{.Update.RepoName}} · {{compactPath .Update.RepoPath}}</div></div></a><div class="actions"><a class="button" href="/repo?path={{.Update.RepoPath | urlquery}}">Repository</a><a class="badge" href="/">Back</a></div></div></header><main class="grid">
{{if .Flash.Text}}<div class="flash {{.Flash.Class}}">{{.Flash.Text}}</div>{{end}}
<section class="change-identity" aria-label="Change identity">
	<div class="change-field"><div class="metric-label">Repo</div><div class="change-field-value" title="{{.Update.RepoName}}">{{.Update.RepoName}}</div></div>
	<div class="change-field"><div class="metric-label">Change ID</div><div class="change-field-value">#{{.Update.ID}}</div></div>
	<div class="change-field"><div class="metric-label">Path</div><div class="change-field-value path" title="{{.Update.RepoPath}}">{{compactPath .Update.RepoPath}}</div></div>
</section>
<section class="summary">
	<div class="metric"><div class="metric-label">Status</div><div class="metric-value"><span class="badge {{statusClass .Update.Status}}">{{.Update.Status}}</span></div></div>
	<div class="metric"><div class="metric-label">Changed</div><div class="metric-value">{{if .Update.Changed}}<span class="badge success">yes</span>{{else}}<span class="badge paused">no</span>{{end}}</div></div>
	<div class="metric"><div class="metric-label">Started</div><div class="metric-value">{{humanTime .Update.StartedAt}}</div><div class="metric-detail">{{formatTime .Update.StartedAt}}</div></div>
	<div class="metric"><div class="metric-label">Finished</div><div class="metric-value">{{humanTime .Update.FinishedAt}}</div><div class="metric-detail">{{formatTime .Update.FinishedAt}}</div></div>
</section>
<section class="panel" id="revisions"><div class="panel-head"><h2><a class="panel-title" href="#revisions">Revision range</a></h2></div><div class="panel-body"><div class="revision-grid">
	<div class="revision-item"><div class="metric-label">Before</div>{{if .Update.BeforeRev}}<span class="revision-hash" title="{{.Update.BeforeRev}}">{{shortHash .Update.BeforeRev}}</span>{{else}}<span class="revision-hash">-</span>{{end}}</div>
	<div class="revision-item"><div class="metric-label">After</div>{{if .Update.AfterRev}}<span class="revision-hash" title="{{.Update.AfterRev}}">{{shortHash .Update.AfterRev}}</span>{{else}}<span class="revision-hash">-</span>{{end}}</div>
</div></div></section>
<section class="panel" id="change"><div class="panel-head"><h2><a class="panel-title" href="#change">Change</a></h2></div><div class="panel-body"><details class="disclosure"><summary>Pull output for change #{{.Update.ID}}</summary><pre>{{if .Update.Error}}{{.Update.Error}}{{else}}{{.Update.Result}}{{end}}</pre></details></div></section>
<section class="panel" id="ai-summary"><div class="panel-head"><h2><a class="panel-title" href="#ai-summary">AI summaries</a></h2><form class="action-form" method="post" action="/update/ai-summary"><input type="hidden" name="id" value="{{.Update.ID}}"><button class="button primary" type="submit">Generate again</button></form></div><div class="panel-body">
{{if .AISummaries}}{{range .AISummaries}}<details class="disclosure"><summary>AI summary · {{humanTime .CreatedAt}} · {{.Status}}</summary>{{if .Error}}<pre>{{.Error}}</pre>{{end}}{{if .Result}}<pre>{{.Result}}</pre>{{end}}</details>{{end}}{{else}}<div class="empty">No AI summaries yet.</div>{{end}}
</div></section>
<section class="panel" id="ai-input"><div class="panel-head"><h2><a class="panel-title" href="#ai-input">AI model input</a></h2></div><div class="panel-body">
	<div class="context-grid" aria-label="AI input context">
		<div class="context-item"><div class="metric-label">Change</div><div class="context-value">#{{.Update.ID}}</div></div>
		<div class="context-item"><div class="metric-label">Repo</div><div class="context-value" title="{{.Update.RepoName}}">{{.Update.RepoName}}</div></div>
		<div class="context-item"><div class="metric-label">Provider</div><div class="context-value" title="{{.AIInput.Provider}}">{{if .AIInput.Provider}}{{.AIInput.Provider}}{{else}}-{{end}}</div></div>
		<div class="context-item"><div class="metric-label">Model</div><div class="context-value" title="{{.AIInput.Model}}">{{if .AIInput.Model}}{{.AIInput.Model}}{{else}}-{{end}}</div></div>
		<div class="context-item"><div class="metric-label">Before</div><div class="context-value">{{if .Update.BeforeRev}}<span title="{{.Update.BeforeRev}}">{{shortHash .Update.BeforeRev}}</span>{{else}}-{{end}}</div></div>
		<div class="context-item"><div class="metric-label">After</div><div class="context-value">{{if .Update.AfterRev}}<span title="{{.Update.AfterRev}}">{{shortHash .Update.AfterRev}}</span>{{else}}-{{end}}</div></div>
		<div class="context-item"><div class="metric-label">API type</div><div class="context-value">{{if .AIInput.APIType}}{{.AIInput.APIType}}{{else}}responses{{end}}</div></div>
		<div class="context-item"><div class="metric-label">Path</div><div class="context-value path" title="{{.Update.RepoPath}}">{{compactPath .Update.RepoPath}}</div></div>
	</div>
	<details class="disclosure"><summary>System prompt for change #{{.Update.ID}}</summary><pre>{{.AIInput.Prompt}}</pre></details>
	{{if .AIInput.Error}}<details class="disclosure"><summary>User input build error for change #{{.Update.ID}}</summary><pre>{{.AIInput.Error}}</pre></details>{{else}}<details class="disclosure"><summary>User input for change #{{.Update.ID}}</summary><pre>{{.AIInput.Input}}</pre></details>{{end}}
</div></section>
<section class="panel" id="plugin-results"><div class="panel-head"><h2><a class="panel-title" href="#plugin-results">Plugin results</a></h2></div><div class="panel-body">
{{if .PluginResults}}{{range .PluginResults}}<details class="disclosure"><summary>{{.PluginID}} · {{.Status}} · {{humanTime .CreatedAt}}</summary><pre>{{pluginResultMessage .}}</pre></details>{{end}}{{else}}<div class="empty">No plugin results yet.</div>{{end}}
</div></section>
</main><script>document.querySelectorAll('form').forEach(form => form.addEventListener('submit', () => { const b = document.activeElement; if (b && b.tagName === 'BUTTON') b.textContent = 'Working...'; }));</script></body></html>`))

var settingsTemplate = template.Must(template.New("settings").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>Settings - autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><h1>Settings</h1></a><a class="badge" href="/">Back</a></div></header><main class="grid">
{{if .Flash.Text}}<div class="flash {{.Flash.Class}}">{{.Flash.Text}}</div>{{end}}
<section class="panel"><div class="panel-head"><h2>Daemon settings</h2></div><div class="panel-body">
<form class="grid" method="post" action="/settings">
	<div class="summary">
		<div class="metric"><div class="metric-label">Pull interval</div><div class="metric-value"><input class="input" type="number" min="1" name="pull_interval_minutes" value="{{.PullInterval}}" aria-label="Pull interval minutes"></div><div class="metric-detail">minutes between daemon pulls</div></div>
		<div class="metric"><div class="metric-label">History retention</div><div class="metric-value"><input class="input" type="number" min="1" name="history_retention_days" value="{{.RetentionDays}}" aria-label="History retention days"></div><div class="metric-detail">days to keep update history</div></div>
	</div>
	<div class="actions"><button class="button primary" type="submit">Save</button><a class="button" href="/">Cancel</a></div>
</form>
</div></section>
</main><script>document.querySelectorAll('form').forEach(form => form.addEventListener('submit', () => { const b = document.activeElement; if (b && b.tagName === 'BUTTON') b.textContent = 'Saving...'; }));</script></body></html>`))

var statusTemplate = template.Must(template.New("status").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>Status - autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><h1>Status</h1></a><a class="badge" href="/">Back</a></div></header><main class="grid">
{{if .Flash.Text}}<div class="flash {{.Flash.Class}}">{{.Flash.Text}}</div>{{end}}
<section class="summary">
	<div class="metric"><div class="metric-label">Service</div><div class="metric-value"><span class="badge {{serviceStatusClass .ServiceStatus}}">{{.ServiceStatus}}</span></div><div class="metric-detail">{{.ServiceLabel}}</div></div>
	<div class="metric"><div class="metric-label">Database</div><div class="path" title="{{.DBPath}}">{{compactPath .DBPath}}</div></div>
	<div class="metric"><div class="metric-label">Plugins</div><div class="metric-value">{{humanNumber .PluginSummary.Enabled}}/{{humanNumber .PluginSummary.Total}}</div><div class="metric-detail"><a href="/plugins">Configure plugins</a></div></div>
</section>
<section class="panel" id="daemon"><div class="panel-head"><h2><a class="panel-title" href="#daemon">Daemon</a></h2></div><div class="panel-body">
<div class="summary">
	<div class="metric"><div class="metric-label">Next run</div><div class="metric-value">{{humanTime .DaemonStatus.NextRunAt}}</div><div class="metric-detail">{{formatTime .DaemonStatus.NextRunAt}}</div></div>
	<div class="metric"><div class="metric-label">Last run</div><div class="metric-value">{{if .DaemonStatus.LastRunDuration}}{{humanDuration .DaemonStatus.LastRunDuration}}{{else}}-{{end}}</div><div class="metric-detail">{{formatTime .DaemonStatus.LastRunStarted}}</div></div>
	<div class="metric"><div class="metric-label">Pulling now</div><div class="metric-value">{{humanNumber (len .DaemonStatus.RunningRepos)}}</div><div class="metric-detail">{{if .DaemonStatus.RunningRepos}}{{join .DaemonStatus.RunningRepos ", "}}{{else}}Idle{{end}}</div></div>
	<div class="metric"><div class="metric-label">Last run result</div><div class="metric-value">{{humanNumber .DaemonStatus.Checked}}</div><div class="metric-detail">ok {{humanNumber .DaemonStatus.Success}} · skipped {{humanNumber .DaemonStatus.Skipped}} · error {{humanNumber .DaemonStatus.Error}}</div></div>
</div>
</div></section>
</main></body></html>`))

var unregisterTemplate = template.Must(template.New("unregister").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>Unregister {{.Repo.Name}} - autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><div class="header-title"><h1>Unregister {{.Repo.Name}}</h1><div class="header-path" title="{{.Repo.Path}}">{{compactPath .Repo.Path}}</div></div></a><a class="badge" href="/repo?path={{.Repo.Path | urlquery}}">Back</a></div></header><main class="grid">
<section class="panel"><div class="panel-head"><h2>Confirm unregister</h2></div><div class="panel-body">
<p>This removes the repository from autogitpull config. It does not delete files from disk.</p>
<form class="action-form" method="post" action="/repo/unregister">
	<input type="hidden" name="path" value="{{.Repo.Path}}">
	<input class="input confirm" name="confirm_name" placeholder="Type {{.Repo.Name}}" autocomplete="off">
	<button class="button danger" type="submit">Unregister</button>
</form>
</div></section>
</main></body></html>`))
