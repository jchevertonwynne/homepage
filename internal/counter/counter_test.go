package counter

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "count.txt")
}

func TestLoadMissingFileStartsAtZero(t *testing.T) {
	c, err := Load(tempPath(t))
	if err != nil {
		t.Fatalf("Load on a missing file: %v", err)
	}
	if got := c.Value(); got != 0 {
		t.Errorf("Value() = %d, want 0", got)
	}
}

func TestLoadExistingFile(t *testing.T) {
	path := tempPath(t)
	if err := os.WriteFile(path, []byte("41\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.Next(); got != 42 {
		t.Errorf("Next() = %d, want 42", got)
	}
}

// A file we cannot parse must fail loudly. Resetting to zero would discard a
// count we were asked to keep, and the app refusing to start is a far better
// signal than a homepage that quietly says "visitor #1" again.
func TestLoadRejectsUnparseableFile(t *testing.T) {
	for _, content := range []string{"", "  \n", "banana", "-1", "1 2", "3.5"} {
		path := tempPath(t)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("Load(%q) = nil error, want a parse failure", content)
		}
	}
}

func TestFlushRoundTrip(t *testing.T) {
	path := tempPath(t)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 7 {
		c.Next()
	}
	if err := c.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Flush: %v", err)
	}
	if got := reloaded.Value(); got != 7 {
		t.Errorf("reloaded Value() = %d, want 7", got)
	}
}

// The on-disk format is meant to be readable with cat, not just by Load.
func TestFileIsPlainDecimal(t *testing.T) {
	path := tempPath(t)
	c, _ := Load(path)
	c.Next()
	c.Next()
	if err := c.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "2\n" {
		t.Errorf("file contents = %q, want %q", b, "2\n")
	}
}

func TestFlushIsNoOpWhenClean(t *testing.T) {
	path := tempPath(t)
	c, _ := Load(path)

	if err := c.Flush(t.Context()); err != nil {
		t.Fatalf("Flush on a clean counter: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Flush on a clean counter created a file; want no write at all")
	}

	c.Next()
	if err := c.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Second flush with no intervening Next must not rewrite the file.
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := c.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("Flush rewrote the file despite no change since the last flush")
	}
}

func TestFlushLeavesNoTempFile(t *testing.T) {
	path := tempPath(t)
	c, _ := Load(path)
	c.Next()
	if err := c.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("directory holds %v, want just the count file", names)
	}
}

func TestConcurrentNext(t *testing.T) {
	c, _ := Load(tempPath(t))

	const goroutines, each = 8, 250
	var wg sync.WaitGroup
	seen := make(chan uint64, goroutines*each)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				seen <- c.Next()
			}
		}()
	}
	wg.Wait()
	close(seen)

	if got := c.Value(); got != goroutines*each {
		t.Errorf("Value() = %d, want %d", got, goroutines*each)
	}
	// Every caller must have been handed a distinct number — two visitors
	// being told they are both #7 is the bug this guards against.
	dupes := make(map[uint64]bool, goroutines*each)
	for n := range seen {
		if dupes[n] {
			t.Fatalf("Next() returned %d more than once", n)
		}
		dupes[n] = true
	}
}

// A crash mid-write must leave the old value readable rather than a truncated
// file. Simulated by planting a stale .tmp alongside a good count file: the
// temp file is never what Load reads.
func TestStaleTempFileDoesNotCorruptTheCount(t *testing.T) {
	path := tempPath(t)
	if err := os.WriteFile(path, []byte("500\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".tmp", []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load with a stale temp file present: %v", err)
	}
	if got := c.Value(); got != 500 {
		t.Errorf("Value() = %d, want 500", got)
	}
}

func TestRunFlushesOnContextCancel(t *testing.T) {
	path := tempPath(t)
	c, _ := Load(path)
	c.Next()
	c.Next()
	c.Next()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	// A long tick interval so the only flush that can happen is the one on
	// cancellation — that is the graceful-shutdown path under test.
	go func() { done <- c.Run(ctx, time.Hour, nil) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Value(); got != 3 {
		t.Errorf("count after shutdown flush = %d, want 3", got)
	}
}

func TestRunFlushesOnTick(t *testing.T) {
	path := tempPath(t)
	c, _ := Load(path)
	c.Next()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx, time.Millisecond, nil)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if reloaded, err := Load(path); err == nil && reloaded.Value() == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("ticker never flushed the count to disk")
}

// A failing tick must not stop the loop: the next one has to keep trying.
func TestRunKeepsGoingAfterAFailedTick(t *testing.T) {
	dir := t.TempDir()
	// A path inside a directory that does not exist makes every flush fail.
	c, _ := Load(filepath.Join(dir, "missing", "count.txt"))
	c.Next()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var errs int
	go c.Run(ctx, time.Millisecond, func(error) {
		mu.Lock()
		errs++
		mu.Unlock()
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := errs
		mu.Unlock()
		if n >= 3 {
			return // still retrying after earlier failures, which is the point
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Run stopped retrying after a failed flush")
}
