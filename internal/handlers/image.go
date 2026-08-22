package handlers

import (
	"bytes"
	"image/png"
	"log"
	"net/http"
	"strconv"
	"strings"

	"homepage/internal/art"
)

// imagePath is the URL for a given count's picture. The count is part of the
// path rather than a query parameter so each one is a distinct, immutable
// resource that Cloudflare's edge can cache forever.
func imagePath(n uint64) string {
	return "/image/" + strconv.FormatUint(n, 10) + ".png"
}

// HandleImage renders the picture for a count taken from the URL.
//
// The endpoint is public and unauthenticated, so the only caller-controlled
// input is this one number, and it is bounded. Canvas size is fixed in the art
// package rather than accepted here: a caller-chosen width and height would
// let anyone ask a Raspberry Pi to allocate and fill an arbitrarily large
// buffer.
func (s *Server) HandleImage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	digits, ok := strings.CutSuffix(name, ".png")
	if !ok {
		http.NotFound(w, r)
		return
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		http.Error(w, "image name must be a number", http.StatusBadRequest)
		return
	}
	if n > art.MaxCount {
		// The odometer only has six digits. Rejecting rather than clamping
		// keeps the cache honest: every URL that returns 200 shows the number
		// it names.
		http.Error(w, "count out of range", http.StatusNotFound)
		return
	}

	// Encoded into memory first so a failure can still become a 500, and so
	// Content-Length is set — Cloudflare caches a known-length body more
	// happily than a chunked one. These images are tens of kilobytes.
	var buf bytes.Buffer
	if err := png.Encode(&buf, art.Render(n)); err != nil {
		log.Printf("encode image for %d: %v", n, err)
		http.Error(w, "could not render image", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	// art.Render is deterministic, so this URL's bytes can never change. That
	// is what makes an immutable, year-long cache truthful rather than a lie
	// that bites on the next deploy.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(buf.Bytes())
}
