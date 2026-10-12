// Command squad: teams of Claude Code agents with a gate on every mail and spawn, and Paseo as
// the window onto them.
package main

import (
	"os"

	"github.com/bacnx/squad/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
