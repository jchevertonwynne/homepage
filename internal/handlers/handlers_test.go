package handlers_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

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

// Only the page counts. If image loads or health checks incremented, a single
// visit would register as several.
func TestOnlyTheIndexIncrements(t *testing.T) {
	mux, c := newServer(t)
	for _, path := range []string{"/healthz", "/static/style.css", "/nope"} {
		get(t, mux, path)
	}
	if got := c.Value(); got != 0 {
		t.Errorf("count = %d after non-page requests, want 0", got)
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
	for _, path := range []string{"/wp-admin", "/index.php", "/image/1.png", "/a/b/c"} {
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

// Each easter egg fires on the digits as written, so 167 counts for 67 and
// 607 does not. Eggs are driven by a class on <body> plus a caption line,
// both rendered server-side — the page promises no JavaScript in its own
// colophon. A number can trigger more than one egg at once, like 420 (which
// contains both "42" and "420").
func TestEasterEggs(t *testing.T) {
	cases := map[uint64][]string{
		66:    nil,
		67:    {"six-seven"},
		167:   {"six-seven"},
		607:   nil,
		670:   {"six-seven"},
		6700:  {"six-seven"},
		7607:  nil, // a 7, a 6 and a 0 in the way — the digits must be adjacent
		7676:  {"six-seven"},
		69:    {"nice"},
		169:   {"nice"},
		691:   {"nice"},
		42:    {"the-answer"},
		4210:  {"the-answer"}, // has "42" but not "420"
		420:   {"the-answer", "blaze-it"},
		4200:  {"the-answer", "blaze-it"},
		1337:  {"leet"},
		51337: {"leet"},
		1998:  {"born-98"},
		30498: {"birthday"},
	}
	captions := map[string]string{
		"six-seven":  "six seven",
		"nice":       "nice",
		"the-answer": "the answer",
		"blaze-it":   "blaze it",
		"leet":       "leet",
		"born-98":    "98 baby",
		"birthday":   "happy birthday",
	}
	for visit, wantClasses := range cases {
		mux, _ := newServerAt(t, visit-1)
		body := get(t, mux, "/").Body.String()
		bodyTag := body[:strings.Index(body, "<main>")]

		if len(wantClasses) == 0 {
			if !strings.Contains(bodyTag, "<body>") {
				t.Errorf("visit #%d: want an unadorned <body>, got %q", visit, bodyTag)
			}
			continue
		}
		classAttr := bodyTag[strings.Index(bodyTag, `class="`)+len(`class="`):]
		gotClasses := strings.Fields(classAttr[:strings.Index(classAttr, `"`)])
		for _, class := range wantClasses {
			if !slices.Contains(gotClasses, class) {
				t.Errorf("visit #%d: body classes %v missing %q", visit, gotClasses, class)
			}
			if want := captions[class]; !strings.Contains(body, want) {
				t.Errorf("visit #%d: caption %q missing", visit, want)
			}
		}
	}
}

// The eggs have to be styled to be eggs at all: without these rules the
// classes on <body> do nothing and the caption lines are indistinguishable
// from the text above them.
func TestEasterEggsAreStyled(t *testing.T) {
	rec := get(t, newServerMux(t), "/static/style.css")
	css := rec.Body.String()
	want := []string{
		"body.six-seven", "body.nice", "body.the-answer", "body.blaze-it",
		"body.leet", "body.born-98", "body.birthday",
		".egg", "prefers-reduced-motion",
	}
	for _, w := range want {
		if !strings.Contains(css, w) {
			t.Errorf("stylesheet has no %q rule, so an easter egg would not show", w)
		}
	}
}

func newServerMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux, _ := newServer(t)
	return mux
}

// The picture is embedded rather than linked, so there is no URL to walk.
func TestImageIsInlinedNotLinked(t *testing.T) {
	mux, _ := newServer(t)
	body := get(t, mux, "/").Body.String()

	if strings.Contains(body, "/image/") {
		t.Error("the page still references an /image/ URL")
	}
	if !strings.Contains(body, "src=\"data:image/png;base64,") {
		t.Fatal("the page does not embed a PNG data URI")
	}
}

// html/template rejects data: URIs in src by default and silently replaces
// them with #ZgotmplZ — the page renders, the image is broken, and nothing
// logs an error. The Image field is template.URL to avoid that, and this is
// the assertion that catches it if someone changes the type back.
func TestImageDataURISurvivesTemplating(t *testing.T) {
	mux, _ := newServer(t)
	body := get(t, mux, "/").Body.String()

	if strings.Contains(body, "ZgotmplZ") {
		t.Fatal("html/template sanitised the data URI away; Image must be template.URL")
	}
}

// The whole point of inlining: previous visitors' pictures must not be
// fetchable, by any address.
func TestNoImageEndpointRemains(t *testing.T) {
	mux, _ := newServer(t)
	for _, path := range []string{"/image/1.png", "/image/1", "/image/", "/image"} {
		if code := get(t, mux, path).Code; code == http.StatusOK {
			t.Errorf("GET %s returned 200; the image endpoint should be gone", path)
		}
	}
}

// The embedded image must be the one for this visit, not a stale or shared
// one: two visits in a row must embed different pictures.
func TestEachVisitEmbedsItsOwnImage(t *testing.T) {
	mux, _ := newServer(t)
	first := get(t, mux, "/").Body.String()
	second := get(t, mux, "/").Body.String()

	extract := func(s string) string {
		i := strings.Index(s, "base64,")
		if i < 0 {
			t.Fatal("no data URI in the page")
		}
		return s[i : i+200]
	}
	if extract(first) == extract(second) {
		t.Error("consecutive visits embedded the same image")
	}
}
