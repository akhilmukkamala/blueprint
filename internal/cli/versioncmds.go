// Version-reporting command (DESIGN §11): `blueprint version` with --json
// emitting {version, go, os, arch} for scripting and release verification.
// This file is owned by the release feature — other features register
// commands in their own files.
package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Version is the release version, plumbed from cmd/blueprint's main.version
// (stamped by -ldflags "-X main.version=..." at release build time).
var Version = "0.0.0-dev"

type versionInfo struct {
	Version string `json:"version"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func init() { Register(versionCommand()) }

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the blueprint version and build platform",
		Long: "Prints the release version plus the Go toolchain and target platform it was\n" +
			"built with. With --json the output is {version, go, os, arch}.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := versionInfo{
				Version: Version,
				Go:      runtime.Version(),
				OS:      runtime.GOOS,
				Arch:    runtime.GOARCH,
			}
			if jsonFlag(cmd) {
				return printJSON(cmd, info)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "blueprint %s (%s %s/%s)\n",
				info.Version, info.Go, info.OS, info.Arch)
			return nil
		},
	}
}
