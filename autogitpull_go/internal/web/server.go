package web

import (
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
	servicepkg "github.com/blankstatic/autogitpull/autogitpull_go/internal/service"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
)

const Addr = ":9009"
const serviceInterval = 30 * time.Minute
const updatesPerPage = 50

//go:embed assets/featurehub.png
var appIcon []byte

type Server struct {
	store   *db.Store
	storage *config.StorageManager
	mux     *http.ServeMux
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
	s.mux.HandleFunc("/favicon.ico", s.icon)
	s.mux.HandleFunc("/assets/app-icon.png", s.icon)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	page := pageFromRequest(r)
	totalUpdates, err := s.store.CountUpdates()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	page = clampPage(page, totalUpdates)

	updates, err := s.store.RecentUpdatesPage(updatesPerPage, pageOffset(page))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	repos := s.storage.GetAllRepos()
	dbPath, _ := config.GetUpdatesDBPath()
	serviceStatus := getServiceStatus()

	_ = indexTemplate.Execute(w, map[string]any{
		"Repos":         repos,
		"Updates":       updates,
		"RepoCount":     len(repos),
		"UpdateCount":   len(updates),
		"TotalUpdates":  totalUpdates,
		"Pagination":    newPagination(r.URL.Path, nil, page, totalUpdates),
		"DBPath":        dbPath,
		"ServiceStatus": serviceStatus,
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
	totalUpdates, err := s.store.CountRepoUpdates(repoPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	page = clampPage(page, totalUpdates)

	updates, err := s.store.RepoUpdatesPage(repoPath, updatesPerPage, pageOffset(page))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	changes, err := git.GitGetUncommitedChanges(repoPath)
	if err != nil {
		changes = err.Error()
	}

	_ = repoTemplate.Execute(w, map[string]any{
		"Repo":         repo,
		"Updates":      updates,
		"Changes":      changes,
		"TotalUpdates": totalUpdates,
		"Pagination":   newPagination(r.URL.Path, url.Values{"path": []string{repoPath}}, page, totalUpdates),
	})
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
	next := url.Values{}
	for key, vals := range values {
		for _, val := range vals {
			next.Add(key, val)
		}
	}
	if page > 1 {
		next.Set("page", strconv.Itoa(page))
	}
	query := next.Encode()
	if query == "" {
		return path
	}
	return path + "?" + query
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
	.pagination { padding: 12px 16px; display: flex; align-items: center; justify-content: space-between; gap: 12px; border-top: 1px solid #d8dee4; background: #f6f8fa; }
	.pagination-info { color: #57606a; font-size: 12px; }
	.pagination-actions { display: flex; gap: 8px; }
	.page-link { display: inline-flex; align-items: center; min-height: 28px; padding: 4px 10px; border: 1px solid #d0d7de; border-radius: 6px; background: white; color: #24292f; font-size: 12px; font-weight: 600; }
	.page-link.disabled { color: #8c959f; background: #f6f8fa; pointer-events: none; }
	.repo-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 10px; }
	.repo { display: block; border: 1px solid #d8dee4; border-radius: 8px; padding: 12px; background: #fff; color: inherit; transition: border-color .12s ease, box-shadow .12s ease, transform .12s ease; }
	.repo:hover { border-color: #8c959f; box-shadow: 0 1px 4px rgba(27,31,36,.08); transform: translateY(-1px); text-decoration: none; }
	.repo-title { display: flex; justify-content: space-between; gap: 10px; align-items: baseline; margin-bottom: 7px; }
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
	.badge.service-running { background: #dafbe1; color: #116329; }
	.badge.service-stopped { background: #ffebe9; color: #cf222e; }
	.badge.service-loaded { background: #ddf4ff; color: #0969da; }
	.time { white-space: nowrap; }
	.time-detail { color: #57606a; font-size: 12px; margin-top: 2px; white-space: nowrap; }
	.empty { color: #57606a; padding: 18px; }
	@media (max-width: 760px) { .header-inner { display: block; } .summary { grid-template-columns: 1fr; } th:nth-child(4), td:nth-child(4) { display: none; } }
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

var indexTemplate = template.Must(template.New("index").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><h1>autogitpull</h1></a></div></header><main>
<section class="summary">
	<div class="metric"><div class="metric-label">Repositories</div><div class="metric-value">{{.RepoCount}}</div></div>
	<div class="metric"><div class="metric-label">Recent events</div><div class="metric-value">{{.TotalUpdates}}</div></div>
	<div class="metric"><div class="metric-label">Service</div><div class="metric-value"><span class="badge {{serviceStatusClass .ServiceStatus}}">{{.ServiceStatus}}</span></div><div class="metric-detail">launchd service</div></div>
	<div class="metric"><div class="metric-label">Database</div><div class="path" title="{{.DBPath}}">{{compactPath .DBPath}}</div></div>
</section>
<div class="grid">
<section class="panel" id="repositories"><div class="panel-head"><h2><a class="panel-title" href="#repositories">Repositories</a></h2></div><div class="panel-body">
{{if .Repos}}<div class="repo-list">{{range .Repos}}<a class="repo" href="/repo?path={{.Path | urlquery}}" title="{{.Path}}"><div class="repo-title"><strong>{{.Name}}</strong><span class="badge">{{.DefaultBranch}}</span></div><div class="path">{{compactPath .Path}}</div><div class="time-detail">Last sync: {{humanTime .LastSync}} · {{formatTime .LastSync}}</div></a>{{end}}</div>{{else}}<div class="empty">No repositories registered.</div>{{end}}
</div></section>
<section class="panel" id="updates"><div class="panel-head"><h2><a class="panel-title" href="#updates">Recent updates</a></h2></div>
{{if .Updates}}<table><tr><th>Time</th><th>Repo</th><th>Status</th><th>Result</th></tr>
{{range .Updates}}<tr><td><div class="time">{{humanTime .StartedAt}}</div><div class="time-detail">{{formatTime .StartedAt}}</div></td><td><a href="/repo?path={{.RepoPath | urlquery}}">{{.RepoName}}</a><div class="path" title="{{.RepoPath}}">{{compactPath .RepoPath}}</div></td><td><span class="badge {{statusClass .Status}}">{{.Status}}</span></td><td><pre>{{if .Error}}{{.Error}}{{else}}{{.Result | firstLine}}{{end}}</pre></td></tr>{{end}}
</table>{{template "pagination" .Pagination}}{{else}}<div class="empty">No updates recorded yet.</div>{{end}}</section>
</div>
</main></body></html>

{{define "pagination"}}<div class="pagination"><div class="pagination-info">{{if .Total}}Showing {{.From}}-{{.To}} of {{.Total}} · page {{.Page}} of {{.TotalPages}}{{else}}No records{{end}}</div><div class="pagination-actions">{{if .HasPrev}}<a class="page-link" href="{{.PrevURL}}">Prev</a>{{else}}<span class="page-link disabled">Prev</span>{{end}}{{if .HasNext}}<a class="page-link" href="{{.NextURL}}">Next</a>{{else}}<span class="page-link disabled">Next</span>{{end}}</div></div>{{end}}`))

var repoTemplate = template.Must(template.New("repo").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>{{.Repo.Name}} - autogitpull</title><link rel="icon" type="image/png" href="/favicon.ico"><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><a class="brand" href="/"><img class="brand-icon" src="/assets/app-icon.png" alt=""><div class="header-title"><h1>{{.Repo.Name}}</h1><div class="header-path" title="{{.Repo.Path}}">{{compactPath .Repo.Path}}</div></div></a><a class="badge" href="/">Back</a></div></header><main class="grid">
<section class="summary">
	<div class="metric"><div class="metric-label">Default branch</div><div class="metric-value">{{.Repo.DefaultBranch}}</div></div>
	<div class="metric"><div class="metric-label">Last sync</div><div class="metric-value">{{humanTime .Repo.LastSync}}</div><div class="metric-detail">{{formatTime .Repo.LastSync}}</div></div>
	<div class="metric"><div class="metric-label">Recorded events</div><div class="metric-value">{{.TotalUpdates}}</div></div>
</section>
<section class="panel" id="changes"><div class="panel-head"><h2><a class="panel-title" href="#changes">Current local changes</a></h2></div><div class="panel-body"><pre>{{if .Changes}}{{.Changes}}{{else}}No uncommitted changes{{end}}</pre></div></section>
<section class="panel" id="updates"><div class="panel-head"><h2><a class="panel-title" href="#updates">Updates</a></h2></div>
{{if .Updates}}<table><tr><th>Time</th><th>Status</th><th>Changed</th><th>Result</th></tr>
{{range .Updates}}<tr><td><div class="time">{{humanTime .StartedAt}}</div><div class="time-detail">{{formatTime .StartedAt}}</div></td><td><span class="badge {{statusClass .Status}}">{{.Status}}</span></td><td>{{changedText .Changed}}</td><td><pre>{{if .Error}}{{.Error}}{{else}}{{.Result}}{{end}}</pre></td></tr>{{end}}
</table>{{template "pagination" .Pagination}}{{else}}<div class="empty">No updates recorded for this repository.</div>{{end}}</section>
</main></body></html>

{{define "pagination"}}<div class="pagination"><div class="pagination-info">{{if .Total}}Showing {{.From}}-{{.To}} of {{.Total}} · page {{.Page}} of {{.TotalPages}}{{else}}No records{{end}}</div><div class="pagination-actions">{{if .HasPrev}}<a class="page-link" href="{{.PrevURL}}">Prev</a>{{else}}<span class="page-link disabled">Prev</span>{{end}}{{if .HasNext}}<a class="page-link" href="{{.NextURL}}">Next</a>{{else}}<span class="page-link disabled">Next</span>{{end}}</div></div>{{end}}`))
