// Command git-persona manages and switches between multiple Git and SSH
// identities by injecting them directly into the global Git configuration.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Dkavila/git-persona/internal/cli"
	"github.com/Dkavila/git-persona/internal/git"
	"github.com/Dkavila/git-persona/internal/ssh"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

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
	root := cli.NewRootCmd(cli.Deps{
		Home: home,
		Git:  git.New(git.ExecRunner{}),
		Keys: ssh.NewExecManager(),
	})
	root.Version = version

	return root.ExecuteContext(context.Background())
}
