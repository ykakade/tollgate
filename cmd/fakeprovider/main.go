// Command fakeprovider is a test server that speaks the Anthropic Messages API
// and the OpenAI Chat Completions API wire formats. It always returns the same
// reply. Use it to test Tollgate and its clients without a real provider.
//
// The -scenario flag makes every request fail on purpose, for example
// -scenario status:529. The x-fake-scenario header does the same for one
// request. See the fakeprovider package for the scenarios.
//
// The -first-token-delay and -token-interval flags make the replies as slow
// as a real model, for demos and benchmarks. The -prompt-cache flag turns on
// a simulated prompt cache, so the usage has cache read and write tokens.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ykakade/tollgate/internal/fakeprovider"
)

// shutdownTimeout is the longest time the server waits for open requests
// after a stop signal.
const shutdownTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stderr)
	stop()
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(os.Stderr, "The fake provider stopped because of an error: %v\n", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("fakeprovider", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:8090", "Address to listen on.")
	scenario := fs.String("scenario", "", "Failure `scenario` for all requests, for example status:529 or reset-after:5-tokens.")
	firstDelay := fs.Duration("first-token-delay", 0, "Wait before the first body byte of each reply, for example 300ms.")
	interval := fs.Duration("token-interval", 0, "Wait between two output tokens, for example 20ms.")
	promptCache := fs.Duration("prompt-cache", 0, "Lifetime of a simulated prompt cache, for example 5m. 0 turns the cache off.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *firstDelay < 0 || *interval < 0 || *promptCache < 0 {
		return errors.New("the -first-token-delay, -token-interval and -prompt-cache flags must not be negative")
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	fp := fakeprovider.New(fakeprovider.Config{Logger: logger, FirstTokenDelay: *firstDelay, TokenInterval: *interval, PromptCache: *promptCache})
	if err := fp.SetScenario(*scenario); err != nil {
		return fmt.Errorf("the -scenario flag: %w", err)
	}

	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", *listen)
	if err != nil {
		return fmt.Errorf("start listener: %w", err)
	}
	srv := &http.Server{
		Handler:           fp.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	logger.InfoContext(ctx, "fake provider started", "addr", ln.Addr().String())

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	logger.InfoContext(ctx, "fake provider stopping")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	return nil
}
