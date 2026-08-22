// Package counter holds a visit counter that survives restarts.
//
// The count lives in memory and is written to disk periodically rather than
// on every visit. A public page attracts crawlers, and one SD card write per
// request is a lot of wear for a number nobody checks to the second. The
// trade-off is bounded: Run flushes on a ticker and again when its context is
// cancelled, so a graceful stop (systemd's SIGTERM on restart or reboot) loses
// nothing at all. Only a power cut can lose counts, and at most one tick's
// worth.
package counter

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Counter is a monotonic visit count backed by a file. It is safe for
// concurrent use; every HTTP handler shares one.
type Counter struct {
	mu    sync.Mutex
	n     uint64
	dirty bool
	path  string
}

// Load reads the count from path. A missing file is not an error — it is what
// the first run on a new machine looks like, and starting from zero is the
// right behaviour. A file that exists but cannot be parsed *is* an error: it
// means something is there and we do not understand it, and silently resetting
// to zero would throw away a number we were asked to keep.
func Load(path string) (*Counter, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Counter{path: path}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read count: %w", err)
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse count from %s: %w", path, err)
	}
	return &Counter{n: n, path: path}, nil
}

// Next increments the count and returns the new value.
func (c *Counter) Next() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	c.dirty = true
	return c.n
}

// Value returns the current count without incrementing it.
func (c *Counter) Value() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// Flush writes the count to disk, atomically, and is a no-op when nothing has
// changed since the last write.
//
// The write goes to a temporary file that is fsynced and then renamed over the
// target, so a crash mid-write leaves the previous count intact rather than a
// truncated file that Load would reject. The containing directory is fsynced
// afterwards too: rename is atomic, but the directory entry it creates is not
// durable until the directory itself is flushed, and durability across an
// abrupt reboot is the whole point of this file.
func (c *Counter) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return nil
	}

	dir := filepath.Dir(c.path)
	tmp := c.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create temp count file: %w", err)
	}
	// Any failure from here on leaves tmp behind, so it is removed on every
	// error path rather than accumulating .tmp files next to the real one.
	if _, err := fmt.Fprintf(f, "%d\n", c.n); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write temp count file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("sync temp count file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp count file: %w", err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename count file into place: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync count file directory: %w", err)
	}

	c.dirty = false
	return nil
}

// Run flushes on a ticker until ctx is cancelled, then flushes one last time
// so a graceful shutdown loses nothing. It returns the error from that final
// flush, which is the one worth reporting — failing to persist on the way out
// is the case where counts actually go missing.
//
// A failed tick is reported to onErr and the loop continues: the count is
// still correct in memory, the file still holds a valid older value, and the
// next tick retries. Giving up on the first transient error would mean a full
// disk at second 5 silently stops all persistence for the life of the
// process. onErr may be nil.
func (c *Counter) Run(ctx context.Context, every time.Duration, onErr func(error)) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := c.Flush(); err != nil && onErr != nil {
				onErr(fmt.Errorf("periodic flush: %w", err))
			}
		case <-ctx.Done():
			return c.Flush()
		}
	}
}

// syncDir fsyncs a directory so a rename into it is durable. Opening a
// directory read-only and syncing it is the portable POSIX way to do this.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
