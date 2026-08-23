package handlers

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"image/png"
	"log"
	"net/http"
	"strconv"
	"strings"

	"homepage/internal/art"
)

// indexData is what templates/index.html renders against.
type indexData struct {
	Count uint64
	// Image is a data: URI holding the whole PNG, not a link to one.
	//
	// It is template.URL because html/template refuses data: URIs in an src
	// attribute by default and silently substitutes #ZgotmplZ — the page would
	// render with a broken image and no error anywhere. The value is built
	// here from bytes this package produced, so bypassing the sanitiser is
	// safe; it would not be for anything derived from a request.
	Image    template.URL
	Capped   bool
	SixSeven bool
}

// HandleIndex counts the visit and renders the page. This is the only route
// that increments: image requests and health checks must not inflate the
// number, or every page view would count as two.
func (s *Server) HandleIndex(w http.ResponseWriter, r *http.Request) {
	n := s.counter.Next()

	// The picture is embedded in the page rather than served from its own URL.
	// A URL per count meant anyone could walk /image/1.png upwards and look at
	// every visitor's image, which makes "yours is the only one that looks
	// like that" untrue. There is now no address to walk.
	//
	// The cost is real and was measured: this is the only cacheable thing the
	// site had, so every page load now renders (~155ms on the Pi) instead of
	// Cloudflare serving a repeat, and the response grows from ~1.3KB to
	// ~80KB. At this traffic that is a few seconds of CPU an hour.
	//
	// The odometer tops out at six digits, so past that the picture shows the
	// clamped number while the text still reports the true count.
	img, err := imageDataURI(min(n, art.MaxCount))
	if err != nil {
		log.Printf("render image for %d: %v", n, err)
		http.Error(w, "could not render the page", http.StatusInternalServerError)
		return
	}

	data := indexData{
		Count:    n,
		Image:    img,
		Capped:   n > art.MaxCount,
		SixSeven: sixSeven(n),
	}

	// The count changes on every visit, so the page must never be cached — by
	// Cloudflare, by a browser, or by anything between. With the image inlined
	// that now means nothing about a visit is cacheable, which is the price of
	// the image not having an address.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if err := s.tmpl.ExecuteTemplate(w, "index", data); err != nil {
		// The status line is already sent by the time a template fails
		// mid-write, so there is nothing useful to tell the client. Log it and
		// let the truncated response speak for itself.
		log.Printf("render index: %v", err)
	}
}

// imageDataURI renders the picture for a count and returns it as a data: URI.
//
// base64 costs a third more bytes than the binary, which is the standard
// trade for embedding: the alternative is a second request, and a second
// request needs a URL, which is the thing being removed.
func imageDataURI(n uint64) (template.URL, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, art.Render(n)); err != nil {
		return "", err
	}
	var uri strings.Builder
	uri.WriteString("data:image/png;base64,")
	enc := base64.NewEncoder(base64.StdEncoding, &uri)
	if _, err := enc.Write(buf.Bytes()); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return template.URL(uri.String()), nil
}

// sixSeven reports whether the visit number has a 67 in it, which is the whole
// qualification for the easter egg. It looks at the digits as written — 167
// and 6700 count, 607 does not — because the joke is about seeing "67" in the
// number on the page, not about arithmetic.
func sixSeven(n uint64) bool {
	return strings.Contains(strconv.FormatUint(n, 10), "67")
}
