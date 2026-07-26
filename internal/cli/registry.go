// Package cli is the subcommand registry. Each feature package registers its
// cobra command from its own file in this package (one file per feature — never
// edit another feature's file, never edit this one). cmd/blueprint pulls the
// registered set. This keeps parallel feature work conflict-free.
package cli

import "github.com/spf13/cobra"

var registered []*cobra.Command

// Register adds a subcommand; called from init() in per-feature files.
func Register(c *cobra.Command) { registered = append(registered, c) }

// All returns every registered subcommand.
func All() []*cobra.Command { return registered }
