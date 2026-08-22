package handlers

import (
	"log"
	"net/http"

	"homepage/internal/art"
)

// indexData is what templates/index.html renders against.
type indexData struct {
	Count    uint64
	ImageURL string
	Capped   bool
}

// HandleIndex counts the visit and renders the page. This is the only route
// that increments: image requests and health checks must not inflate the
// number, or every page view would count as two.
func (s *Server) HandleIndex(w http.ResponseWriter, r *http.Request) {
	n := s.counter.Next()

	// The odometer tops out at six digits and HandleImage refuses anything
	// larger, so past that point the page must ask for the clamped picture or
	// it would link to its own 404. The true count still appears as text.
	data := indexData{
		Count:    n,
		ImageURL: imagePath(min(n, art.MaxCount)),
		Capped:   n > art.MaxCount,
	}

	// The count changes on every visit, so the page itself must never be
	// cached — by Cloudflare, by a browser, or by anything between. The
	// expensive half of the work is the image, and that is cached instead.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if err := s.tmpl.ExecuteTemplate(w, "index", data); err != nil {
		// The status line is already sent by the time a template fails
		// mid-write, so there is nothing useful to tell the client. Log it and
		// let the truncated response speak for itself.
		log.Printf("render index: %v", err)
	}
}
