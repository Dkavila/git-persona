// Command git-pilot manages and switches between multiple Git and SSH
// identities by injecting them directly into the global Git configuration.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Dkavila/git-pilot/internal/cli"
	"github.com/Dkavila/git-pilot/internal/git"
	"github.com/Dkavila/git-pilot/internal/rewrite"
	"github.com/Dkavila/git-pilot/internal/ssh"
)

// version is injected at build time via -ldflags "-X main.version=...".
// The default matches the installer's own fallback, so an unreleased build
// reports the same string everywhere instead of three different ones.
var version = "0.0.0-dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// run keeps main free of logic so the exit path stays in exactly one place.
func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}

	// This is the composition root: the only place where the real git and ssh
	// binaries are bound to the command tree.
	keys := ssh.NewExecManager()

	root := cli.NewRootCmd(cli.Deps{
		Home:     home,
		Git:      git.New(git.ExecRunner{}),
		Keys:     keys,
		Prober:   keys,
		Rewriter: rewrite.ExecRewriter{},
	})
	root.Version = version

	return root.ExecuteContext(context.Background())
}
