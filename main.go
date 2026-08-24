package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"homepage/internal/counter"
	"homepage/internal/handlers"
	"homepage/internal/metrics"
	"homepage/internal/profiling"
	"homepage/internal/tracing"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	countPath := flag.String("counter", "homepage-count.txt", "path to the visit count file")
	flushEvery := flag.Duration("flush-every", 5*time.Second, "how often to write the visit count to disk")
	otelEndpoint := flag.String("otel-endpoint", "", "host:port of an OTLP/gRPC trace collector; tracing is disabled if empty")
	pprofAddr := flag.String("pprof-addr", ":6060", "listen address for pprof debug endpoints; never expose this outside the cluster")
	flag.Parse()

	go profiling.ListenAndServe(*pprofAddr)

	shutdownTracing, err := tracing.Init(context.Background(), "homepage", *otelEndpoint)
	if err != nil {
		log.Fatalf("init tracing: %v", err)
	}

	c, err := counter.Load(*countPath)
	if err != nil {
		// Refusing to start beats silently resetting to zero: an unreadable
		// count file means something is wrong that a human should look at,
		// not that there have been no visitors.
		log.Fatalf("load visit count: %v", err)
	}

	srv := &http.Server{
		Addr:    *addr,
		Handler: tracing.Middleware("homepage", metrics.Instrument(newMux(c))),
		// A public endpoint needs these. Without ReadHeaderTimeout a single
		// client can hold a connection open indefinitely by dribbling out
		// headers, and enough of those exhaust the server.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// systemd sends SIGTERM on restart and on shutdown, so this is the path
	// every deploy and every reboot takes. Counting on it is what makes the
	// periodic flush interval a bound on power-loss only.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	flushed := make(chan error, 1)
	go func() {
		flushed <- c.Run(ctx, *flushEvery, func(err error) {
			log.Printf("warning: %v", err)
		})
	}()

	go func() {
		log.Printf("homepage listening on %s (count: %s, at %d visits)", *addr, *countPath, c.Value())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Print("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	// Must come before shutdownTracing: c.Run's own final flush (triggered
	// by ctx.Done(), same as this shutdown) happens in that goroutine, and
	// its span would be silently dropped if the exporter were already
	// closed by the time it runs.
	if err := <-flushed; err != nil {
		log.Fatalf("final flush: %v", err)
	}
	log.Printf("stopped at %d visits", c.Value())

	if err := shutdownTracing(shutdownCtx); err != nil {
		log.Printf("tracing shutdown: %v", err)
	}
}

func newMux(c *counter.Counter) *http.ServeMux {
	mux := http.NewServeMux()
	handlers.New(c, templatesFS, staticFS).RegisterRoutes(mux)
	mux.Handle("GET /metrics", metrics.Handler())
	return mux
}
