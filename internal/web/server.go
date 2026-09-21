// Package web holds the HTTP router, templates, and vendored static assets.
package web

import (
	"database/sql"
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"labdoc/internal/version"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type Server struct {
	db   *sql.DB
	tmpl *template.Template
}

func New(db *sql.DB) (*Server, error) {
	tmpl, err := template.New("").
		Funcs(template.FuncMap{"version": func() string { return version.String() }}).
		ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{db: db, tmpl: tmpl}, nil
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	static, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	r.Get("/", s.index)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := s.db.Ping(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	for _, e := range entities {
		s.mountCRUD(r, e)
	}
	s.mountTopology(r)
	return r
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page := indexPage{base: s.newBase("LabDoc")}
	for _, e := range entities {
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM " + e.Table).Scan(&n); err != nil {
			s.fail(w, "count "+e.Key, err)
			return
		}
		page.Entries = append(page.Entries, indexEntry{Entity: e, Count: n})
	}
	s.render(w, "index.html", page)
}
