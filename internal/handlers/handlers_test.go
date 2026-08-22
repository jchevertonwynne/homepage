package handlers_test

import (
	"bytes"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"homepage/internal/art"
	"homepage/internal/counter"
	"homepage/internal/handlers"
)

// The real templates and stylesheet, read straight off disk rather than
// copied into testdata. A fixture copy would drift out of step with the
// template main.go actually embeds, and these tests assert on what the page
// renders.
func repoFS() fs.FS { return os.DirFS("../..") }

func newServer(t *testing.T) (*http.ServeMux, *counter.Counter) {
	return newServerAt(t, 0)
}

// newServerAt is newServer with the count already at start, for the tests that
// care about a particular visit number. It seeds a count file rather than
// calling Next in a loop, so reaching visit 6700 costs one write.
func newServerAt(t *testing.T, start uint64) (*http.ServeMux, *counter.Counter) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "count.txt")
	if start > 0 {
		if err := os.WriteFile(path, []byte(strconv.FormatUint(start, 10)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := counter.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handlers.New(c, repoFS(), repoFS()).RegisterRoutes(mux)
	return mux, c
}

func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestIndexCountsTheVisit(t *testing.T) {
	mux, c := newServer(t)

	for want := 1; want <= 3; want++ {
		rec := get(t, mux, "/")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / = %d, want 200", rec.Code)
		}
		if got := c.Value(); got != uint64(want) {
			t.Errorf("after %d visits count = %d", want, got)
		}
		if !strings.Contains(rec.Body.String(), "#"+strconv.Itoa(want)) {
			t.Errorf("page does not mention visitor #%d: %s", want, rec.Body.String())
		}
	}
}

func TestIndexIsNotCached(t *testing.T) {
	mux, _ := newServer(t)
	rec := get(t, mux, "/")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store — the count changes every visit", got)
	}
}

func TestIndexLinksToAnImageThatExists(t *testing.T) {
	mux, _ := newServer(t)
	body := get(t, mux, "/").Body.String()

	start := strings.Index(body, "/image/")
	if start < 0 {
		t.Fatalf("no image URL in the page: %s", body)
	}
	url := body[start : start+strings.Index(body[start:], `"`)]

	rec := get(t, mux, url)
	if rec.Code != http.StatusOK {
		t.Errorf("the page links to %s, which returns %d", url, rec.Code)
	}
}

// Only the page counts. If image loads or health checks incremented, a single
// visit would register as several.
func TestOnlyTheIndexIncrements(t *testing.T) {
	mux, c := newServer(t)
	for _, path := range []string{"/image/5.png", "/healthz", "/static/style.css", "/nope"} {
		get(t, mux, path)
	}
	if got := c.Value(); got != 0 {
		t.Errorf("count = %d after non-page requests, want 0", got)
	}
}

func TestImageServesAPNG(t *testing.T) {
	mux, _ := newServer(t)
	rec := get(t, mux, "/image/1337.png")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /image/1337.png = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("body is not a valid PNG: %v", err)
	}
	if img.Bounds().Dx() != art.Width || img.Bounds().Dy() != art.Height {
		t.Errorf("image is %dx%d, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), art.Width, art.Height)
	}
	if got := rec.Header().Get("Content-Length"); got == "" {
		t.Error("no Content-Length; Cloudflare caches a known-length body more readily")
	}
}

// The immutable header is a promise that this URL's bytes never change.
func TestImageIsCachedImmutably(t *testing.T) {
	mux, _ := newServer(t)
	rec := get(t, mux, "/image/42.png")
	got := rec.Header().Get("Cache-Control")
	for _, want := range []string{"public", "immutable", "max-age="} {
		if !strings.Contains(got, want) {
			t.Errorf("Cache-Control = %q, want it to contain %q", got, want)
		}
	}
}

func TestImageIsStableAcrossRequests(t *testing.T) {
	mux, _ := newServer(t)
	first := get(t, mux, "/image/42.png").Body.Bytes()
	second := get(t, mux, "/image/42.png").Body.Bytes()
	if !bytes.Equal(first, second) {
		t.Error("the same URL returned different bytes; the immutable cache header would be a lie")
	}
}

func TestImageRejectsBadNames(t *testing.T) {
	mux, _ := newServer(t)
	cases := map[string]int{
		"/image/notanumber.png":              http.StatusBadRequest,
		"/image/-1.png":                      http.StatusBadRequest,
		"/image/1.5.png":                     http.StatusBadRequest,
		"/image/42":                          http.StatusNotFound, // missing extension
		"/image/42.jpg":                      http.StatusNotFound,
		"/image/1000000.png":                 http.StatusNotFound,   // past the six-digit odometer
		"/image/99999999999999999999999.png": http.StatusBadRequest, // overflows uint64
	}
	for path, want := range cases {
		if got := get(t, mux, path).Code; got != want {
			t.Errorf("GET %s = %d, want %d", path, got, want)
		}
	}
}

func TestHealthz(t *testing.T) {
	mux, _ := newServer(t)
	rec := get(t, mux, "/healthz")
	if rec.Code != http.StatusOK {
		t.Errorf("GET /healthz = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("body = %q, want it to say ok", rec.Body.String())
	}
}

// "/" is registered with {$} so it matches only the root; anything else that
// is not a real route must 404 rather than rendering the page and counting a
// visit.
func TestUnknownPathsAreNotTheHomepage(t *testing.T) {
	mux, c := newServer(t)
	for _, path := range []string{"/wp-admin", "/index.php", "/image/", "/a/b/c"} {
		if got := get(t, mux, path).Code; got == http.StatusOK {
			t.Errorf("GET %s returned 200, want a 404", path)
		}
	}
	if got := c.Value(); got != 0 {
		t.Errorf("crawler paths incremented the count to %d", got)
	}
}

func TestConcurrentVisitsAreAllCounted(t *testing.T) {
	mux, c := newServer(t)

	const n = 200
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			get(t, mux, "/")
		}()
	}
	wg.Wait()

	if got := c.Value(); got != n {
		t.Errorf("count = %d after %d concurrent visits", got, n)
	}
}

// The easter egg fires on the digits as written, so 167 counts and 607 does
// not. It is driven by a class on <body> plus a caption line, both rendered
// server-side — the page promises no JavaScript in its own colophon.
func TestSixSevenEasterEgg(t *testing.T) {
	cases := map[uint64]bool{
		66:   false,
		67:   true,
		167:  true,
		607:  false,
		670:  true,
		6700: true,
		7607: false, // a 7, a 6 and a 0 in the way — the digits must be adjacent
		7676: true,
	}
	for visit, want := range cases {
		mux, _ := newServerAt(t, visit-1)
		body := get(t, mux, "/").Body.String()

		if got := strings.Contains(body, `class="six-seven"`); got != want {
			t.Errorf("visit #%d: six-seven body class = %v, want %v", visit, got, want)
		}
		if got := strings.Contains(body, "six seven"); got != want {
			t.Errorf("visit #%d: caption egg = %v, want %v", visit, got, want)
		}
	}
}

// The egg has to be styled to be an egg at all: without these rules the class
// on <body> does nothing and the caption line is indistinguishable from the
// text above it.
func TestSixSevenIsStyled(t *testing.T) {
	rec := get(t, newServerMux(t), "/static/style.css")
	css := rec.Body.String()
	for _, want := range []string{"body.six-seven", ".egg", "prefers-reduced-motion"} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet has no %q rule, so the easter egg would not show", want)
		}
	}
}

func newServerMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux, _ := newServer(t)
	return mux
}
