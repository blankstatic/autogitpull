package web

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/blankstatic/autogitpull/autogitpull_go/internal/config"
	"github.com/blankstatic/autogitpull/autogitpull_go/internal/db"
	"github.com/blankstatic/autogitpull/autogitpull_go/pkg/git"
)

const Addr = ":9009"

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
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	updates, err := s.store.RecentUpdates(100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	repos := s.storage.GetAllRepos()
	dbPath, _ := config.GetUpdatesDBPath()

	_ = indexTemplate.Execute(w, map[string]any{
		"Repos":       repos,
		"Updates":     updates,
		"RepoCount":   len(repos),
		"UpdateCount": len(updates),
		"DBPath":      dbPath,
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

	updates, err := s.store.RepoUpdates(repoPath, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	changes, err := git.GitGetUncommitedChanges(repoPath)
	if err != nil {
		changes = err.Error()
	}

	_ = repoTemplate.Execute(w, map[string]any{
		"Repo":    repo,
		"Updates": updates,
		"Changes": changes,
	})
}

var baseCSS = template.CSS(`
	:root { color-scheme: light; }
	* { box-sizing: border-box; }
	body { margin: 0; font: 14px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: #24292f; background: #f6f8fa; }
	header { background: #24292f; color: white; }
	.header-inner { max-width: 1180px; margin: 0 auto; padding: 22px 24px; display: flex; align-items: flex-end; justify-content: space-between; gap: 20px; }
	h1, h2, h3 { margin: 0; line-height: 1.15; }
	h1 { font-size: 24px; }
	h2 { font-size: 18px; }
	h3 { font-size: 15px; margin-bottom: 10px; }
	main { max-width: 1180px; margin: 0 auto; padding: 22px 24px 36px; }
	a { color: #0969da; text-decoration: none; }
	a:hover { text-decoration: underline; }
	.path { color: #57606a; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow-wrap: anywhere; }
	.header-path { color: #d0d7de; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow-wrap: anywhere; margin-top: 6px; }
	.grid { display: grid; gap: 18px; }
	.summary { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin-bottom: 18px; }
	.metric { background: white; border: 1px solid #d0d7de; border-radius: 8px; padding: 14px; }
	.metric-label { color: #57606a; font-size: 12px; text-transform: uppercase; letter-spacing: .04em; }
	.metric-value { margin-top: 5px; font-size: 22px; font-weight: 650; }
	.panel { background: white; border: 1px solid #d0d7de; border-radius: 8px; overflow: hidden; }
	.panel-head { padding: 14px 16px; border-bottom: 1px solid #d8dee4; background: #f6f8fa; display: flex; justify-content: space-between; gap: 14px; align-items: center; }
	.panel-body { padding: 16px; }
	.repo-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 10px; }
	.repo { border: 1px solid #d8dee4; border-radius: 8px; padding: 12px; background: #fff; }
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
	.empty { color: #57606a; padding: 18px; }
	@media (max-width: 760px) { .header-inner { display: block; } .summary { grid-template-columns: 1fr; } th:nth-child(4), td:nth-child(4) { display: none; } }
`)

var templateFuncs = template.FuncMap{
	"statusClass": func(status string) string {
		switch status {
		case "success", "error", "running":
			return status
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
		return t.Local().Format("2006-01-02 15:04:05")
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
}

var indexTemplate = template.Must(template.New("index").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>autogitpull</title><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><div><h1>autogitpull</h1><div class="header-path">{{.DBPath}}</div></div><div>Dashboard :9009</div></div></header><main>
<section class="summary">
	<div class="metric"><div class="metric-label">Repositories</div><div class="metric-value">{{.RepoCount}}</div></div>
	<div class="metric"><div class="metric-label">Recent events</div><div class="metric-value">{{.UpdateCount}}</div></div>
	<div class="metric"><div class="metric-label">Database</div><div class="path">{{.DBPath}}</div></div>
</section>
<div class="grid">
<section class="panel"><div class="panel-head"><h2>Repositories</h2></div><div class="panel-body">
{{if .Repos}}<div class="repo-list">{{range .Repos}}<article class="repo"><div class="repo-title"><strong><a href="/repo?path={{.Path | urlquery}}">{{.Name}}</a></strong><span class="badge">{{.DefaultBranch}}</span></div><div class="path">{{.Path}}</div><div class="path">Last sync: {{formatTime .LastSync}}</div></article>{{end}}</div>{{else}}<div class="empty">No repositories registered.</div>{{end}}
</div></section>
<section class="panel"><div class="panel-head"><h2>Recent updates</h2></div>
{{if .Updates}}<table><tr><th>Time</th><th>Repo</th><th>Status</th><th>Result</th></tr>
{{range .Updates}}<tr><td>{{formatTime .StartedAt}}</td><td><a href="/repo?path={{.RepoPath | urlquery}}">{{.RepoName}}</a><div class="path">{{.RepoPath}}</div></td><td><span class="badge {{statusClass .Status}}">{{.Status}}</span></td><td><pre>{{if .Error}}{{.Error}}{{else}}{{.Result | firstLine}}{{end}}</pre></td></tr>{{end}}
</table>{{else}}<div class="empty">No updates recorded yet.</div>{{end}}</section>
</div>
</main></body></html>`))

var repoTemplate = template.Must(template.New("repo").Funcs(templateFuncs).Parse(`
<!doctype html><html><head><meta charset="utf-8"><title>{{.Repo.Name}} - autogitpull</title><style>` + string(baseCSS) + `</style></head>
<body><header><div class="header-inner"><div><h1>{{.Repo.Name}}</h1><div class="header-path">{{.Repo.Path}}</div></div><a class="badge" href="/">Back</a></div></header><main class="grid">
<section class="summary">
	<div class="metric"><div class="metric-label">Default branch</div><div class="metric-value">{{.Repo.DefaultBranch}}</div></div>
	<div class="metric"><div class="metric-label">Last sync</div><div class="metric-value">{{formatTime .Repo.LastSync}}</div></div>
	<div class="metric"><div class="metric-label">Recorded events</div><div class="metric-value">{{len .Updates}}</div></div>
</section>
<section class="panel"><div class="panel-head"><h2>Current local changes</h2></div><div class="panel-body"><pre>{{if .Changes}}{{.Changes}}{{else}}No uncommitted changes{{end}}</pre></div></section>
<section class="panel"><div class="panel-head"><h2>Updates</h2></div>
{{if .Updates}}<table><tr><th>Time</th><th>Status</th><th>Changed</th><th>Result</th></tr>
{{range .Updates}}<tr><td>{{formatTime .StartedAt}}</td><td><span class="badge {{statusClass .Status}}">{{.Status}}</span></td><td>{{changedText .Changed}}</td><td><pre>{{if .Error}}{{.Error}}{{else}}{{.Result}}{{end}}</pre></td></tr>{{end}}
</table>{{else}}<div class="empty">No updates recorded for this repository.</div>{{end}}</section>
</main></body></html>`))
