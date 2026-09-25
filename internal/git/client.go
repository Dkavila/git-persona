// Package git applies and removes Git identities by writing directly to the
// Git configuration, avoiding any change to the user's ~/.ssh/config.
package git

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Dkavila/git-persona/internal/config"
)

// Exit codes returned by the git binary that carry meaning for this package.
const (
	exitKeyNotFound = 1 // git config --get: key is not set
	exitUnsetNoKey  = 5 // git config --unset: key does not exist
)

// identityKeys are the three config keys a persona owns, in the order they are
// written and unset.
var identityKeys = []string{"user.name", "user.email", "core.sshCommand"}

// Runner executes the git binary. It exists so the package can be tested
// without spawning a real process.
type Runner interface {
	Run(args ...string) (stdout string, err error)
}

// ExitError reports a non-zero exit from the git binary. Runner implementations
// must return this type so callers can react to specific exit codes.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("git exited with code %d: %s", e.Code, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("git exited with code %d", e.Code)
}

// Client manipulates Git configuration through a Runner.
type Client struct {
	r Runner
}

// New returns a Client backed by the given Runner.
func New(r Runner) *Client { return &Client{r: r} }

// SSHCommand renders the core.sshCommand value that pins SSH to a single
// identity file.
//
// The path is quoted and its separators normalised to forward slashes. Git
// parses this value with shell-like rules in which a backslash escapes the
// next character, so a raw Windows path would be mangled; OpenSSH and Git both
// accept forward slashes on Windows. The quotes additionally cover paths that
// contain spaces.
func SSHCommand(keyPath string) string {
	normalised := strings.ReplaceAll(keyPath, `\`, "/")
	return fmt.Sprintf("ssh -i %q -o IdentitiesOnly=yes", normalised)
}

// ApplyProfile writes the profile into the global Git configuration. The
// profile is validated first, so a malformed one cannot leave a half-applied
// identity behind.
func (c *Client) ApplyProfile(p config.Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}

	values := []string{p.Name, p.Email, SSHCommand(p.KeyPath)}
	for i, key := range identityKeys {
		if _, err := c.r.Run("config", "--global", key, values[i]); err != nil {
			return fmt.Errorf("set global %s: %w", key, err)
		}
	}
	return nil
}

// CurrentGlobalIdentity reports the user.name and user.email currently set in
// the global configuration. An unset key yields an empty string rather than an
// error, since that is the normal state of a fresh machine.
func (c *Client) CurrentGlobalIdentity() (name, email string, err error) {
	name, err = c.globalGet("user.name")
	if err != nil {
		return "", "", err
	}
	email, err = c.globalGet("user.email")
	if err != nil {
		return "", "", err
	}
	return name, email, nil
}

func (c *Client) globalGet(key string) (string, error) {
	out, err := c.r.Run("config", "--global", "--get", key)
	if err != nil {
		if exitCode(err) == exitKeyNotFound {
			return "", nil
		}
		return "", fmt.Errorf("read global %s: %w", key, err)
	}
	return strings.TrimSpace(out), nil
}

// CleanLocal removes the three identity keys from a repository's local
// configuration so the global persona takes effect again. An empty path means
// the current directory.
//
// A key that is already absent (exit code 5) is not an error and does not stop
// the remaining keys from being cleared.
func (c *Client) CleanLocal(path string) error {
	if strings.TrimSpace(path) == "" {
		path = "."
	}

	for _, key := range identityKeys {
		if _, err := c.r.Run("-C", path, "config", "--unset", key); err != nil {
			if exitCode(err) == exitUnsetNoKey {
				continue
			}
			return fmt.Errorf("unset local %s in %s: %w", key, path, err)
		}
	}
	return nil
}

// exitCode extracts the git exit code from err, or -1 when err is not an
// ExitError.
func exitCode(err error) int {
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return -1
}
