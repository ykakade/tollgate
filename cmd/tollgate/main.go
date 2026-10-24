// Command tollgate is a lightweight AI gateway for coding agents.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ykakade/tollgate/internal/buildinfo"
)

const usage = `Tollgate is a lightweight AI gateway for coding agents.

Usage:
  tollgate <command>

Commands:
  version   Show the version of this binary.
  help      Show this help.
`

// Exit codes.
const (
	exitOK    = 0
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command in args and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch cmd := args[0]; cmd {
	case "version":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "The version command takes no arguments. Remove %q and try again.\n", args[1])
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
