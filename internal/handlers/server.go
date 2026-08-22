// Package handlers holds every HTTP handler for the homepage, plus the Server
// type carrying their shared dependencies. main.go gathers those dependencies
// and calls New/RegisterRoutes; the whole route table lives in one place here.
package handlers

import (
	"html/template"
	"io/fs"
	"net/http"

	"homepage/internal/counter"
)

// Server carries every handler's dependencies.
type Server struct {
	counter  *counter.Counter
	tmpl     *template.Template
	staticFS fs.FS
}

// New builds a Server. The filesystems are the embed.FS values declared in
// main.go — embed paths cannot reach outside the directory that declares them,
// so main.go must own the //go:embed directives and pass the results in.
//
// Taken as fs.FS rather than embed.FS so the tests can hand over an os.DirFS
// of the repo and exercise the real templates, instead of a fixture copy that
// drifts out of step with them.
func New(c *counter.Counter, templatesFS, staticFS fs.FS) *Server {
	return &Server{
		counter:  c,
		tmpl:     template.Must(template.ParseFS(templatesFS, "templates/*.html")),
		staticFS: staticFS,
	}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.HandleIndex)
	// A wildcard has to be a whole path segment, so this cannot be written as
	// "/image/{n}.png" — that would register a literal segment named
	// "{n}.png". The handler splits the extension off itself.
	mux.HandleFunc("GET /image/{file}", s.HandleImage)
	mux.HandleFunc("GET /healthz", s.HandleHealthz)
	mux.Handle("GET /static/", http.FileServerFS(s.staticFS))
}

// HandleHealthz is what CI polls after a deploy. It deliberately does not
// touch the counter: a deploy should not register as a visitor, and neither
// should a monitoring check.
func (s *Server) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte("ok\n"))
}
