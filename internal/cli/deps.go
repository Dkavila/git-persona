// Package cli assembles the Cobra command tree. Commands stay thin: they parse
// input, call the domain packages and format output.
package cli

import (
	"context"
	"time"

	"github.com/Dkavila/git-persona/internal/config"
)

// GitClient is the slice of the git layer the CLI needs. It is declared here,
// on the consumer side, so commands can be tested without a real git binary.
type GitClient interface {
	ApplyProfile(p config.Profile) error
	CleanLocal(path string) error
}

// KeyManager is the slice of the ssh layer the CLI needs.
type KeyManager interface {
	Generate(ctx context.Context, home, profileName, email string) (keyPath string, err error)
}

// Deps carries everything the command tree needs from the outside world.
type Deps struct {
	// Home is the user's home directory, holding .git-persona and .ssh.
	Home string
	Git  GitClient
	Keys KeyManager
	// Now supplies timestamps; injectable so tests are deterministic.
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now == nil {
		return time.Now()
	}
	return d.Now()
}
