// Command tollgate is a lightweight AI gateway for coding agents.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/ykakade/tollgate/internal/auth"
	"github.com/ykakade/tollgate/internal/buildinfo"
	"github.com/ykakade/tollgate/internal/config"
	"github.com/ykakade/tollgate/internal/server"
)

const usage = `Tollgate is a lightweight AI gateway for coding agents.

Usage:
  tollgate <command> [flags]

Commands:
  serve             Start the gateway.
  init              Show a starter configuration file.
  keys create       Make a virtual key for a client.
  config validate   Check a configuration file.
  version           Show the version of this binary.
  help              Show this help.

To see the flags of a command, run "tollgate <command> -h".
`

// defaultConfig is the default path of the configuration file.
const defaultConfig = "tollgate.yaml"

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command in args and returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return runServe(ctx, rest, stderr)
	case "init":
		return runInit(rest, stdout, stderr)
	case "keys":
		if len(rest) == 0 || rest[0] != "create" {
			fmt.Fprint(stderr, "Usage: tollgate keys create -id <name>\n")
			return exitUsage
		}
		return runKeysCreate(rest[1:], stdout, stderr)
	case "config":
		if len(rest) == 0 || rest[0] != "validate" {
			fmt.Fprint(stderr, "Usage: tollgate config validate [-config path]\n")
			return exitUsage
		}
		return runConfigValidate(rest[1:], stdout, stderr)
	case "version":
		if len(rest) > 0 {
			fmt.Fprintf(stderr, "The version command takes no arguments. Remove %q and try again.\n", rest[0])
			return exitUsage
		}
		fmt.Fprintf(stdout, "tollgate %s\n", buildinfo.Read())
		return exitOK
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "The command %q is not known. Run \"tollgate help\" to see the commands.\n", cmd)
		return exitUsage
	}
}

// newFlags returns a flag set that writes its help to stderr.
func newFlags(name, synopsis string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: tollgate %s\n\nFlags:\n", synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags parses args and returns an exit code, or -1 to continue.
func parseFlags(fs *flag.FlagSet, args []string, stderr io.Writer) int {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "The argument %q is not known. Use flags only.\n", fs.Arg(0))
		return exitUsage
	}
	return -1
}

// loadConfig loads the configuration and prints its errors.
func loadConfig(path string, stderr io.Writer) (*config.Config, bool) {
	cfg, err := config.Load(path, config.OSEnv())
	if err == nil {
		return cfg, true
	}
	if ve, ok := errors.AsType[*config.ValidationError](err); ok {
		fmt.Fprintln(stderr, ve.Error())
		fmt.Fprintf(stderr, "The configuration file %s has %d errors. Correct them and try again.\n", path, len(ve.Errors))
		return nil, false
	}
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "The configuration file %s does not exist. To make one, run \"tollgate init > %s\".\n", path, path)
		return nil, false
	}
	fmt.Fprintf(stderr, "Tollgate cannot read the configuration file %s: %v\n", path, err)
	return nil, false
}

func runServe(ctx context.Context, args []string, stderr io.Writer) int {
	fs := newFlags("serve", "serve [-config path]", stderr)
	path := fs.String("config", defaultConfig, "Path of the configuration `file`.")
	if code := parseFlags(fs, args, stderr); code >= 0 {
		return code
	}
	cfg, ok := loadConfig(*path, stderr)
	if !ok {
		return exitError
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	adapters, err := server.NewAdapters(cfg.Providers, nil)
	if err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot start the providers: %v\n", err)
		return exitError
	}
	srv, err := server.New(server.Options{Config: cfg, Adapters: adapters, Logger: logger})
	if err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot start: %v\n", err)
		return exitError
	}
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.Server.Listen)
	if err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot listen on %s: %v. Make sure that no other program uses the address.\n", cfg.Server.Listen, err)
		return exitError
	}

	// The first SIGTERM or SIGINT starts the drain. A second signal cancels
	// the open requests at once.
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signals)
	drainCtx, startDrain := context.WithCancel(ctx)
	defer startDrain()
	force := make(chan struct{})
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-signals:
			logger.InfoContext(ctx, "stop signal received", "signal", sig.String())
			startDrain()
		case <-done:
			return
		}
		select {
		case <-signals:
			close(force)
		case <-done:
		}
	}()

	err = srv.Serve(drainCtx, ln, force)
	close(done)
	if err != nil {
		logger.ErrorContext(ctx, "server failed", "error", err.Error())
		return exitError
	}
	return exitOK
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("init", "init > tollgate.yaml", stderr)
	if code := parseFlags(fs, args, stderr); code >= 0 {
		return code
	}
	_, _ = stdout.Write(config.Starter())
	return exitOK
}

func runKeysCreate(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("keys create", "keys create -id <name>", stderr)
	id := fs.String("id", "", "Name of the key, for example the name of the computer that uses it. (required)")
	if code := parseFlags(fs, args, stderr); code >= 0 {
		return code
	}
	if !config.ValidName(*id) {
		fmt.Fprint(stderr, "Give a name for the key with -id. Use letters, digits, \".\", \"_\" and \"-\", with 64 characters or fewer.\n")
		return exitUsage
	}
	key, err := auth.NewRandomKey()
	if err != nil {
		fmt.Fprintf(stderr, "Tollgate cannot make a key: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stdout, `Key: %s

Copy the key to a safe location. Tollgate shows the key one time only.

Add these lines to the keys list of a tenant in the configuration file:

      - id: %s
        hash: %s
`, key, *id, auth.Hash(key))
	return exitOK
}

func runConfigValidate(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("config validate", "config validate [-config path]", stderr)
	path := fs.String("config", defaultConfig, "Path of the configuration `file`.")
	if code := parseFlags(fs, args, stderr); code >= 0 {
		return code
	}
	if _, ok := loadConfig(*path, stderr); !ok {
		return exitError
	}
	fmt.Fprintf(stdout, "The configuration file %s is valid.\n", *path)
	return exitOK
}
