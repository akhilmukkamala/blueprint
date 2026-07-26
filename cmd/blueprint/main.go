// blueprint — the Universal Spec-Driven Development framework CLI.
// See DESIGN.md in the commission workspace; canonical repo layout in .blueprint/.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"blueprint/internal/cli"
	"blueprint/internal/install"
)

var version = "0.0.0-dev" // set by -ldflags at release

func main() {
	// Plumb the stamped version to consumers: `blueprint version --json`
	// (internal/cli) and install self-timing events (internal/install).
	cli.Version = version
	install.Version = version
	root := &cobra.Command{
		Use:           "blueprint",
		Short:         "Spec-driven development for any coding agent: specs that verify, loops that stop, knowledge that lasts",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Global structured-output flag (clig.dev; AC accessibility): every command
	// must honor --json by emitting machine-readable output to stdout.
	root.PersistentFlags().Bool("json", false, "emit machine-readable JSON output")

	for _, c := range cli.All() {
		root.AddCommand(c)
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "blueprint:", err)
		os.Exit(1)
	}
}
