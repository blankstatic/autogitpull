package web

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
	servicepkg "github.com/blankstatic/autogitpull/autogitpull_go/internal/service"
	versionpkg "github.com/blankstatic/autogitpull/autogitpull_go/internal/version"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/notifications"
)

const Addr = ":9009"
const serviceInterval = 30 * time.Minute
const serviceLabel = "com.blankstatic.autogitpull"
const updatesPerPage = 50
const activityWeeks = 53
const eventFilterChanges = "changes"
const eventFilterAll = "all"

//go:embed assets/featurehub.png
var appIcon []byte

type Server struct {
	store   *db.Store
	storage *config.StorageManager
	mux     *http.ServeMux
}

type RepoCard struct {
	Repo       config.RepoInfo
	LastUpdate *db.Update
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
	s := &Server{
		store:   store,
		storage: storage,
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Start() {
	go func() {
		slog.Info("web dashboard started", slog.String("addr", Addr))
		if err := http.ListenAndServe(Addr, s.mux); err != nil && err != http.ErrServerClosed {
			slog.Error("web dashboard failed", slog.String("err", err.Error()))
		}
	}()
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.index)
	s.mux.HandleFunc("/repo", s.repo)
	s.mux.HandleFunc("/repo/pull", s.pullRepo)
	s.mux.HandleFunc("/repo/pause", s.pauseRepo)
	s.mux.HandleFunc("/repo/open", s.openRepo)
	s.mux.HandleFunc("/repo/unregister", s.unregisterRepo)
	s.mux.HandleFunc("/settings", s.settings)
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
	dbPath, _ := config.GetUpdatesDBPath()
	serviceStatus := getServiceStatus()
	cfg := s.storage.GetConfig()
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
		"DBPath":        dbPath,
		"ConfigPath":    s.storage.ConfigPath(),
		"ServiceStatus": serviceStatus,
		"ServiceLabel":  serviceLabel,
		"AppVersion":    versionpkg.AppVersion,
		"PullInterval":  cfg.PullIntervalMinutes,
		"RetentionDays": cfg.HistoryRetentionDays,
		"DaemonStatus":  GetDaemonStatus(),
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

	activity, err := s.activity(repoPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	renderTemplate(w, repoTemplate, map[string]any{
		"Repo":         repo,
		"Updates":      updates,
		"Activity":     activity,
		"Changes":      changes,
		"TotalUpdates": totalUpdates,
		"Pagination":   newPagination(r.URL.Path, repoQueryValues(repoPath, filter), page, totalUpdates),
		"EventFilter":  newEventFilter(r.URL.Path, url.Values{"path": []string{repoPath}}, filter),
		"Flash":        flashFromRequest(r),
	})
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

	updateID, err := s.store.BeginUpdate(repo.Path, repo.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	result, pullErr := performPull(repo)
	if err := s.store.FinishUpdate(updateID, result, pullErr); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	notifyURL := "http://localhost" + Addr + repoURL(repo.Path, "")
	if pullErr != nil {
		go func() {
			if notifyErr := notifications.OSNotifyURL(config.AppName, fmt.Sprintf("%s pull failed", repo.Name), pullErr.Error(), notifyURL); notifyErr != nil {
				slog.Error("failed to send pull notification", slog.String("repo", repo.Name), slog.String("err", notifyErr.Error()))
			}
		}()
		http.Redirect(w, r, repoURLWithFlash(repoPath, eventFilterAll, "Pull failed: "+pullErr.Error()), http.StatusSeeOther)
		return
	}
	if pullErr == nil {
		_ = s.storage.UpdateLastSync(repo.Path)
		go func() {
			if notifyErr := notifications.OSNotifyURL(config.AppName, fmt.Sprintf("%s pull", repo.Name), result, notifyURL); notifyErr != nil {
				slog.Error("failed to send pull notification", slog.String("repo", repo.Name), slog.String("err", notifyErr.Error()))
			}
		}()
	}
	http.Redirect(w, r, repoURLWithFlash(repoPath, eventFilterAll, "Pulled successfully"), http.StatusSeeOther)
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
		redirectRepoFlash(w, r, repoPath, "Repo paused")
		return
	}
	redirectRepoFlash(w, r, repoPath, "Repo resumed")
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
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
	http.Redirect(w, r, "/?flash="+url.QueryEscape("Settings saved"), http.StatusSeeOther)
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
	var cmd *exec.Cmd
	switch target {
	case "code":
		cmd = exec.Command("open", "-a", "Visual Studio Code", repoPath)
	case "terminal":
		cmd = exec.Command("open", "-a", "Terminal", repoPath)
	default:
		cmd = exec.Command("open", repoPath)
	}
	if err := cmd.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectRepoFlash(w, r, repoPath, "Opened in "+openTargetLabel(target))
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
	http.Redirect(w, r, "/?flash="+url.QueryEscape("Repo unregistered: "+repo.Name), http.StatusSeeOther)
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

func redirectRepo(w http.ResponseWriter, r *http.Request, repoPath string) {
	http.Redirect(w, r, repoURL(repoPath, ""), http.StatusSeeOther)
}

func redirectRepoFlash(w http.ResponseWriter, r *http.Request, repoPath, flash string) {
	http.Redirect(w, r, repoURLWithFlash(repoPath, "", flash), http.StatusSeeOther)
}

func repoURL(repoPath, filter string) string {
	values := url.Values{}
	if filter != "" {
		values.Set("filter", filter)
	}
	return "/repo?" + queryWithPath(values, repoPath)
}

func repoURLWithFlash(repoPath, filter, flash string) string {
	values := url.Values{}
	if filter != "" {
		values.Set("filter", filter)
	}
	if flash != "" {
		values.Set("flash", flash)
	}
	return "/repo?" + queryWithPath(values, repoPath)
}

func queryWithPath(values url.Values, repoPath string) string {
	query := values.Encode()
	pathQuery := "path=" + strings.ReplaceAll(url.QueryEscape(repoPath), "%2F", "/")
	if query == "" {
		return pathQuery
	}
	return query + "&" + pathQuery
}

func flashFromRequest(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("flash"))
}

func openTargetLabel(target string) string {
	switch target {
	case "code":
		return "VS Code"
	case "terminal":
		return "Terminal"
	default:
		return "Finder"
	}
}

func performPull(repo *config.RepoInfo) (string, error) {
	currentBranch, err := git.GetCurrentBranch(repo.Path)
	if err != nil {
		return "", fmt.Errorf("get current branch: %w", err)
	}
	if currentBranch != repo.DefaultBranch {
		return "", fmt.Errorf("current branch %s is not default branch %s", currentBranch, repo.DefaultBranch)
	}
	hasChanges, err := git.GitHasUncommitedChanges(repo.Path)
	if err != nil {
		return "", fmt.Errorf("check changes: %w", err)
	}
	if hasChanges {
		return "", fmt.Errorf("repository has uncommitted changes")
	}
	return git.GitPull(repo.Path)
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
	query := next.Encode()
	if query == "" {
		return path
	}
	return path + "?" + query
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
	query := next.Encode()
	if query == "" {
		return path
	}
	return path + "?" + query
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

var baseCSS = template.CSS(`
	:root { color-scheme: light; }
	* { box-sizing: border-box; }
	body { margin: 0; font: 14px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: #24292f; background: #f6f8fa; }
	header { background: #24292f; color: white; }
	.header-inner { max-width: 1180px; height: 72px; margin: 0 auto; padding: 0 24px; display: flex; align-items: center; justify-content: space-between; gap: 20px; }
	.brand { display: inline-flex; align-items: center; gap: 10px; color: white; }
	a.brand:hover { text-decoration: none; }
	.brand-icon { width: 40px; height: 40px; border-radius: 8px; display: block; }
	h1, h2, h3 { margin: 0; line-height: 1.15; }
	h1 { font-size: 24px; }
	h2 { font-size: 18px; }
	h3 { font-size: 15px; margin-bottom: 10px; }
	main { max-width: 1180px; margin: 0 auto; padding: 22px 24px 36px; }
	a { color: #0969da; text-decoration: none; }
	a:hover { text-decoration: underline; }
	.path { color: #57606a; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow-wrap: anywhere; }
	.header-title { min-width: 0; }
	.header-path { color: #d0d7de; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: min(760px, 62vw); margin-top: 4px; font-size: 12px; }
	.grid { display: grid; gap: 18px; }
	.summary { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; margin-bottom: 18px; }
	.metric { background: white; border: 1px solid #d0d7de; border-radius: 8px; padding: 14px; }
	.metric-label { color: #57606a; font-size: 12px; text-transform: uppercase; letter-spacing: .04em; }
	.metric-value { margin-top: 5px; font-size: 22px; font-weight: 650; }
	.metric-detail { margin-top: 4px; color: #57606a; font-size: 12px; }
	.panel { background: white; border: 1px solid #d0d7de; border-radius: 8px; overflow: hidden; }
	.panel-head { padding: 14px 16px; border-bottom: 1px solid #d8dee4; background: #f6f8fa; display: flex; justify-content: space-between; gap: 14px; align-items: center; }
	.panel-title { color: #24292f; }
	.panel-title:hover { color: #0969da; text-decoration: none; }
	.panel-body { padding: 16px; }
	.flash { margin-bottom: 18px; padding: 10px 12px; border: 1px solid #b6e3ff; border-radius: 8px; background: #ddf4ff; color: #0969da; font-weight: 600; }
	.filter { display: inline-flex; gap: 4px; padding: 3px; border: 1px solid #d0d7de; border-radius: 8px; background: white; }
	.filter-link { display: inline-flex; min-height: 26px; align-items: center; padding: 3px 9px; border-radius: 6px; color: #57606a; font-size: 12px; font-weight: 600; }
	.filter-link:hover { color: #24292f; text-decoration: none; }
	.filter-link.active { background: #0969da; color: white; }
	.activity-wrap { overflow-x: auto; overflow-y: visible; margin: -2px; padding: 2px 160px 36px; }
	.activity-grid { display: grid; grid-auto-flow: column; grid-auto-columns: 12px; grid-template-rows: repeat(7, 12px); gap: 3px; width: max-content; padding: 2px; }
	.activity-cell { position: relative; width: 12px; height: 12px; border-radius: 2px; background: #ebedf0; border: 1px solid rgba(27,31,36,.06); }
	.activity-cell:hover, .activity-cell:focus-visible { outline: 1px solid #57606a; outline-offset: 1px; }
	.activity-cell[data-title]:hover::after, .activity-cell[data-title]:focus-visible::after { content: attr(data-title); position: absolute; z-index: 2; top: 18px; left: 50%; transform: translateX(-50%); padding: 5px 7px; border-radius: 6px; background: #24292f; color: white; font-size: 12px; line-height: 1.2; white-space: nowrap; box-shadow: 0 4px 12px rgba(27,31,36,.15); pointer-events: none; }
	.activity-cell[data-level="1"] { background: #9be9a8; }
	.activity-cell[data-level="2"] { background: #40c463; }
	.activity-cell[data-level="3"] { background: #30a14e; }
	.activity-cell[data-level="4"] { background: #216e39; }
	.activity-meta { display: flex; align-items: center; justify-content: space-between; gap: 14px; margin-top: 12px; color: #57606a; font-size: 12px; }
	.activity-legend { display: flex; align-items: center; gap: 5px; white-space: nowrap; }
	.activity-legend .activity-cell { display: inline-block; flex: 0 0 auto; }
	.pagination { padding: 12px 16px; display: flex; align-items: center; justify-content: space-between; gap: 12px; border-top: 1px solid #d8dee4; background: #f6f8fa; }
	.pagination-info { color: #57606a; font-size: 12px; }
	.pagination-actions { display: flex; gap: 8px; }
	.page-link { display: inline-flex; align-items: center; min-height: 28px; padding: 4px 10px; border: 1px solid #d0d7de; border-radius: 6px; background: white; color: #24292f; font-size: 12px; font-weight: 600; }
	.page-link.disabled { color: #8c959f; background: #f6f8fa; pointer-events: none; }
	.actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
	.action-form { display: inline-flex; align-items: center; gap: 8px; margin: 0; }
	.input { width: 76px; min-height: 28px; padding: 4px 8px; border: 1px solid #d0d7de; border-radius: 6px; font: inherit; }
	.input.confirm { width: 220px; }
	.button { display: inline-flex; align-items: center; min-height: 28px; padding: 4px 10px; border: 1px solid #d0d7de; border-radius: 6px; background: white; color: #24292f; font: inherit; font-size: 12px; font-weight: 600; cursor: pointer; }
	.button:hover { background: #f6f8fa; }
	.button.danger { color: #cf222e; }
	.repo-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 10px; }
	.repo { display: block; min-width: 0; border: 1px solid #d8dee4; border-radius: 8px; padding: 12px; background: #fff; color: inherit; transition: border-color .12s ease, box-shadow .12s ease, transform .12s ease; }
	.repo:hover { border-color: #8c959f; box-shadow: 0 1px 4px rgba(27,31,36,.08); transform: translateY(-1px); text-decoration: none; }
	.repo-title { display: flex; justify-content: space-between; gap: 10px; align-items: baseline; margin-bottom: 7px; min-width: 0; }
	.repo-title strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.repo-detail { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	table { width: 100%; border-collapse: collapse; }
	th, td { padding: 10px 12px; border-bottom: 1px solid #d8dee4; text-align: left; vertical-align: top; }
	th { color: #57606a; font-size: 12px; background: #f6f8fa; font-weight: 650; text-transform: uppercase; letter-spacing: .04em; }
	td pre { margin: 0; max-height: 160px; }
	pre { white-space: pre-wrap; background: #f6f8fa; border: 1px solid #d0d7de; border-radius: 6px; padding: 12px; overflow: auto; font: 12px/1.45 ui-monospace, SFMono-Regular, Menlo, monospace; }
	.badge { display: inline-flex; align-items: center; min-height: 22px; padding: 2px 8px; border-radius: 999px; background: #eaeef2; color: #24292f; font-size: 12px; font-weight: 600; white-space: nowrap; }
	.badge.success { background: #dafbe1; color: #116329; }
	.badge.error { background: #ffebe9; color: #cf222e; }
	.badge.running { background: #ddf4ff; color: #0969da; }
	.badge.skipped { background: #fff1db; color: #9a6700; }
	.badge.paused { background: #eaeef2; color: #57606a; }
	.badge.service-running { background: #dafbe1; color: #116329; }
	.badge.service-stopped { background: #ffebe9; color: #cf222e; }
	.badge.service-loaded { background: #ddf4ff; color: #0969da; }
	.time { white-space: nowrap; }
	.time-detail { color: #57606a; font-size: 12px; margin-top: 2px; white-space: nowrap; }
	.empty { color: #57606a; padding: 18px; }
	footer { max-width: 1180px; margin: 0 auto; padding: 0 24px 28px; color: #57606a; font-size: 12px; }
	@media (max-width: 760px) { .header-inner { display: block; } .summary { grid-template-columns: 1fr; } .activity-meta { align-items: flex-start; flex-direction: column; } th:nth-child(4), td:nth-child(4) { display: none; } }
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
	"changedText": func(changed bool) string {
		if changed {
			return "yes"
		}
		return "no"
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
	"firstLine": func(s string) string {
		s = strings.TrimSpace(s)
		if s == "" {
			return "-"
		}
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			return s[:i]
		}
		return s
	},
	"compactPath": compactPath,
}

const activityTemplate = `{{define "activity"}}<div class="activity-wrap"><div class="activity-grid" aria-label="Changed update activity from {{.Start}} to {{.End}}">{{range .Cells}}<span class="activity-cell" data-level="{{.Level}}" data-title="{{.Title}}" aria-label="{{.Title}}" tabindex="0"></span>{{end}}</div></div><div class="activity-meta"><div>{{humanNumber .Total}} changed updates in the last year</div><div class="activity-legend"><span>Less</span><span class="activity-cell" data-level="0"></span><span class="activity-cell" data-level="1"></span><span class="activity-cell" data-level="2"></span><span class="activity-cell" data-level="3"></span><span class="activity-cell" data-level="4"></span><span>More</span></div></div>{{end}}`

var indexTemplate = template.Must(template.New("index").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><h1>autogitpull</h1></a><form class="action-form" method="post" action="/settings"><input class="input" type="number" min="1" name="pull_interval_minutes" value="{{.PullInterval}}" aria-label="Pull interval minutes"><button class="button" type="submit">Interval, min</button><input class="input" type="number" min="1" name="history_retention_days" value="{{.RetentionDays}}" aria-label="History retention days"><button class="button" type="submit">Retention, days</button></form></div></header><main>
{{if .Flash}}<div class="flash">{{.Flash}}</div>{{end}}
<section class="summary">
	<div class="metric"><div class="metric-label">Repositories</div><div class="metric-value">{{humanNumber .RepoCount}}</div></div>
	<div class="metric"><div class="metric-label">Recent events</div><div class="metric-value">{{humanNumber .TotalUpdates}}</div></div>
	<div class="metric"><div class="metric-label">Service</div><div class="metric-value"><span class="badge {{serviceStatusClass .ServiceStatus}}">{{.ServiceStatus}}</span></div><div class="metric-detail">{{.ServiceLabel}}</div></div>
	<div class="metric"><div class="metric-label">Database</div><div class="path" title="{{.DBPath}}">{{compactPath .DBPath}}</div></div>
</section>
<div class="grid">
<section class="panel" id="daemon"><div class="panel-head"><h2><a class="panel-title" href="#daemon">Daemon</a></h2></div><div class="panel-body">
<div class="summary">
	<div class="metric"><div class="metric-label">Next run</div><div class="metric-value">{{humanTime .DaemonStatus.NextRunAt}}</div><div class="metric-detail">{{formatTime .DaemonStatus.NextRunAt}}</div></div>
	<div class="metric"><div class="metric-label">Last run</div><div class="metric-value">{{if .DaemonStatus.LastRunDuration}}{{humanDuration .DaemonStatus.LastRunDuration}}{{else}}-{{end}}</div><div class="metric-detail">{{formatTime .DaemonStatus.LastRunStarted}}</div></div>
	<div class="metric"><div class="metric-label">Pulling now</div><div class="metric-value">{{humanNumber (len .DaemonStatus.RunningRepos)}}</div><div class="metric-detail">{{if .DaemonStatus.RunningRepos}}{{join .DaemonStatus.RunningRepos ", "}}{{else}}Idle{{end}}</div></div>
	<div class="metric"><div class="metric-label">Last run result</div><div class="metric-value">{{humanNumber .DaemonStatus.Checked}}</div><div class="metric-detail">ok {{humanNumber .DaemonStatus.Success}} · skipped {{humanNumber .DaemonStatus.Skipped}} · error {{humanNumber .DaemonStatus.Error}}</div></div>
</div>
</div></section>
<section class="panel" id="activity"><div class="panel-head"><h2><a class="panel-title" href="#activity">Activity</a></h2></div><div class="panel-body">
{{template "activity" .Activity}}
</div></section>
<section class="panel" id="repositories"><div class="panel-head"><h2><a class="panel-title" href="#repositories">Repositories</a></h2></div><div class="panel-body">
{{if .RepoCards}}<div class="repo-list">{{range .RepoCards}}<a class="repo" href="/repo?path={{.Repo.Path | urlquery}}" title="{{.Repo.Path}}"><div class="repo-title"><strong>{{.Repo.Name}}</strong><span class="actions">{{if .Repo.Paused}}<span class="badge paused">paused</span>{{end}}{{if .LastUpdate}}<span class="badge {{statusClass .LastUpdate.Status}}">{{.LastUpdate.Status}}</span>{{end}}<span class="badge">{{.Repo.DefaultBranch}}</span></span></div><div class="path repo-detail">{{compactPath .Repo.Path}}</div><div class="time-detail repo-detail" title="{{if .LastUpdate}}{{if .LastUpdate.Error}}{{.LastUpdate.Error | firstLine}}{{else}}{{.LastUpdate.Result | firstLine}}{{end}}{{end}}">{{if .LastUpdate}}Last event: {{humanTime .LastUpdate.StartedAt}} · {{if .LastUpdate.Error}}{{.LastUpdate.Error | firstLine}}{{else}}{{.LastUpdate.Result | firstLine}}{{end}}{{else}}No recorded events{{end}}</div><div class="time-detail repo-detail">Last sync: {{humanTime .Repo.LastSync}} · {{formatTime .Repo.LastSync}}</div></a>{{end}}</div>{{else}}<div class="empty">No repositories registered.</div>{{end}}
</div></section>
<section class="panel" id="updates"><div class="panel-head"><h2><a class="panel-title" href="#updates">Recent updates</a></h2><div class="filter">{{range .EventFilter.Options}}<a class="{{.Class}}" href="{{.URL}}">{{.Label}}</a>{{end}}</div></div>
{{if .Updates}}<table><tr><th>Time</th><th>Repo</th><th>Status</th><th>Result</th></tr>
{{range .Updates}}<tr><td><div class="time">{{humanTime .StartedAt}}</div><div class="time-detail">{{formatTime .StartedAt}}</div></td><td><a href="/repo?path={{.RepoPath | urlquery}}">{{.RepoName}}</a><div class="path" title="{{.RepoPath}}">{{compactPath .RepoPath}}</div></td><td><span class="badge {{statusClass .Status}}">{{.Status}}</span></td><td><pre>{{if .Error}}{{.Error}}{{else}}{{.Result | firstLine}}{{end}}</pre></td></tr>{{end}}
</table>{{template "pagination" .Pagination}}{{else}}<div class="empty">No updates match this filter.</div>{{end}}</section>
</div>
</main><footer>version {{.AppVersion}} · config <span class="path">{{compactPath .ConfigPath}}</span> · db <span class="path">{{compactPath .DBPath}}</span> · service {{.ServiceLabel}}</footer></body></html>

{{define "pagination"}}<div class="pagination"><div class="pagination-info">{{if .Total}}Showing {{humanNumber .From}}-{{humanNumber .To}} of {{humanNumber .Total}} · page {{humanNumber .Page}} of {{humanNumber .TotalPages}}{{else}}No records{{end}}</div><div class="pagination-actions">{{if .HasPrev}}<a class="page-link" href="{{.PrevURL}}">Prev</a>{{else}}<span class="page-link disabled">Prev</span>{{end}}{{if .HasNext}}<a class="page-link" href="{{.NextURL}}">Next</a>{{else}}<span class="page-link disabled">Next</span>{{end}}</div></div>{{end}}` + activityTemplate))

var repoTemplate = template.Must(template.New("repo").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>{{.Repo.Name}} - autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><div class="header-title"><h1>{{.Repo.Name}}</h1><div class="header-path" title="{{.Repo.Path}}">{{compactPath .Repo.Path}}</div></div></a><div class="actions"><form class="action-form" method="post" action="/repo/pull"><input type="hidden" name="path" value="{{.Repo.Path}}"><button class="button" type="submit">Pull now</button></form><form class="action-form" method="post" action="/repo/open"><input type="hidden" name="path" value="{{.Repo.Path}}"><input type="hidden" name="target" value="finder"><button class="button" type="submit">Finder</button></form><form class="action-form" method="post" action="/repo/open"><input type="hidden" name="path" value="{{.Repo.Path}}"><input type="hidden" name="target" value="terminal"><button class="button" type="submit">Terminal</button></form><form class="action-form" method="post" action="/repo/open"><input type="hidden" name="path" value="{{.Repo.Path}}"><input type="hidden" name="target" value="code"><button class="button" type="submit">VS Code</button></form><form class="action-form" method="post" action="/repo/pause"><input type="hidden" name="path" value="{{.Repo.Path}}">{{if .Repo.Paused}}<input type="hidden" name="paused" value="0"><button class="button" type="submit">Resume</button>{{else}}<input type="hidden" name="paused" value="1"><button class="button" type="submit">Pause</button>{{end}}</form><a class="button danger" href="/repo/unregister?path={{.Repo.Path | urlquery}}">Unregister</a><a class="badge" href="/">Back</a></div></div></header><main class="grid">
{{if .Flash}}<div class="flash">{{.Flash}}</div>{{end}}
<section class="summary">
	<div class="metric"><div class="metric-label">Default branch</div><div class="metric-value">{{.Repo.DefaultBranch}}</div></div>
	<div class="metric"><div class="metric-label">Last sync</div><div class="metric-value">{{humanTime .Repo.LastSync}}</div><div class="metric-detail">{{formatTime .Repo.LastSync}}</div></div>
	<div class="metric"><div class="metric-label">Recorded events</div><div class="metric-value">{{humanNumber .TotalUpdates}}</div></div>
	<div class="metric"><div class="metric-label">Auto pull</div><div class="metric-value">{{if .Repo.Paused}}<span class="badge paused">paused</span>{{else}}<span class="badge success">enabled</span>{{end}}</div></div>
</section>
<section class="panel" id="activity"><div class="panel-head"><h2><a class="panel-title" href="#activity">Activity</a></h2></div><div class="panel-body">
{{template "activity" .Activity}}
</div></section>
<section class="panel" id="changes"><div class="panel-head"><h2><a class="panel-title" href="#changes">Current local changes</a></h2></div><div class="panel-body"><pre>{{if .Changes}}{{.Changes}}{{else}}No uncommitted changes{{end}}</pre></div></section>
<section class="panel" id="updates"><div class="panel-head"><h2><a class="panel-title" href="#updates">Updates</a></h2><div class="filter">{{range .EventFilter.Options}}<a class="{{.Class}}" href="{{.URL}}">{{.Label}}</a>{{end}}</div></div>
{{if .Updates}}<table><tr><th>Time</th><th>Status</th><th>Changed</th><th>Result</th></tr>
{{range .Updates}}<tr><td><div class="time">{{humanTime .StartedAt}}</div><div class="time-detail">{{formatTime .StartedAt}}</div></td><td><span class="badge {{statusClass .Status}}">{{.Status}}</span>{{if .SkipReason}}<div class="time-detail">{{.SkipReason}}</div>{{end}}</td><td>{{changedText .Changed}}</td><td><pre>{{if .Error}}{{.Error}}{{else}}{{.Result}}{{end}}</pre></td></tr>{{end}}
</table>{{template "pagination" .Pagination}}{{else}}<div class="empty">No updates match this filter.</div>{{end}}</section>
</main></body></html>

{{define "pagination"}}<div class="pagination"><div class="pagination-info">{{if .Total}}Showing {{humanNumber .From}}-{{humanNumber .To}} of {{humanNumber .Total}} · page {{humanNumber .Page}} of {{humanNumber .TotalPages}}{{else}}No records{{end}}</div><div class="pagination-actions">{{if .HasPrev}}<a class="page-link" href="{{.PrevURL}}">Prev</a>{{else}}<span class="page-link disabled">Prev</span>{{end}}{{if .HasNext}}<a class="page-link" href="{{.NextURL}}">Next</a>{{else}}<span class="page-link disabled">Next</span>{{end}}</div></div>{{end}}` + activityTemplate))

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
