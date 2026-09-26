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
	db           *sql.DB
	tmpl         *template.Template
	loginLimiter *loginLimiter
	resetLimiter *loginLimiter // same throttling, for /forgot-password
	mail         MailConfig
}

func New(db *sql.DB) (*Server, error) {
	tmpl, err := template.New("").
		Funcs(template.FuncMap{"version": func() string { return version.String() }}).
		ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{db: db, tmpl: tmpl, loginLimiter: newLoginLimiter(), resetLimiter: newLoginLimiter()}, nil
}

// SetMail wires outgoing SMTP settings after New; left unset (the zero
// value), email verification and "forgot password" report a friendly error
// instead of attempting to send anything.
func (s *Server) SetMail(cfg MailConfig) { s.mail = cfg }

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	// Runs for every request, including /login and /setup: ensures a CSRF
	// cookie exists and rejects state-changing requests that don't echo it
	// back (see csrfProtect's doc comment).
	r.Use(s.csrfProtect)
	// Every route below, including /static/*, requires a session except the
	// handful sessionAuth treats as public (see its doc comment).
	r.Use(s.sessionAuth)

	static, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	r.Get("/setup", s.setupGet)
	r.Post("/setup", s.setupPost)
	r.Get("/login", s.loginGet)
	r.Post("/login", s.loginPost)
	r.Post("/logout", s.logoutPost)
	r.Get("/account", func(w http.ResponseWriter, r *http.Request) {
		notice := ""
		if r.URL.Query().Get("verified") == "1" {
			notice = "Email confirmed."
		}
		s.accountGet(w, r, notice, "")
	})
	r.Post("/account/password", s.changePassword)
	r.Post("/account/email", s.saveEmail)
	r.Post("/account/users", s.addUser)
	r.Post("/account/users/{id}/delete", s.deleteUser)
	r.Get("/verify-email/{token}", s.verifyEmail)
	r.Get("/forgot-password", s.forgotPasswordGet)
	r.Post("/forgot-password", s.forgotPasswordPost)
	r.Get("/reset-password/{token}", s.resetPasswordGet)
	r.Post("/reset-password/{token}", s.resetPasswordPost)

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
	page := indexPage{base: s.newBase(r, "LabDoc")}
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
