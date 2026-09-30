// Package cli assembles the Cobra command tree. Commands stay thin: they parse
// input, call the domain packages and format output.
package cli

import (
	"context"
	"time"

	"github.com/Dkavila/git-pilot/internal/config"
	"github.com/Dkavila/git-pilot/internal/rewrite"
	"github.com/Dkavila/git-pilot/internal/verify"
)

// GitClient is the slice of the git layer the CLI needs. It is declared here,
// on the consumer side, so commands can be tested without a real git binary.
type GitClient interface {
	ApplyProfile(p config.Profile) error
	CleanLocal(path string) error
	UnsetGlobal() error
}

// KeyManager is the slice of the ssh layer the CLI needs.
type KeyManager interface {
	// Generate creates a new key pair and refuses if one already exists.
	Generate(ctx context.Context, home, profileName, email string) (keyPath string, err error)
	// Overwrite replaces an existing key pair with a new one.
	Overwrite(ctx context.Context, home, profileName, email string) (keyPath string, err error)
}

// Rewriter is the slice of the rewrite layer the CLI needs.
type Rewriter interface {
	BuildPlan(repo string, sel rewrite.Selector, target rewrite.Identity) (*rewrite.Plan, error)
	Apply(repo string, plan *rewrite.Plan, target rewrite.Identity, opts rewrite.Options) (backupRef string, err error)
}

// Deps carries everything the command tree needs from the outside world.
type Deps struct {
	// Home is the user's home directory, holding .git-pilot and .ssh.
	Home string
	Git  GitClient
	Keys KeyManager
	// Prober checks a key's connectivity. It is separate from KeyManager so a
	// command that only creates keys never gains the ability to open sockets.
	Prober verify.Prober
	// Rewriter changes commit authorship. It is keyed by repository path
	// rather than bound at construction, because rewrite operates on whatever
	// repository the user points at, not on the one git-pilot lives in.
	Rewriter Rewriter
	// Now supplies timestamps; injectable so tests are deterministic.
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now == nil {
		return time.Now()
	}
	return d.Now()
}
