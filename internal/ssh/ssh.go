// Package ssh manages the ed25519 key material behind each persona and probes
// its connectivity to GitHub.
package ssh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrKeyExists reports that a key file is already present. Generating over it
// would destroy an identity that may already be registered with a remote, so
// the caller must remove it deliberately.
var ErrKeyExists = errors.New("ssh key already exists")

const (
	sshDirName = ".ssh"
	keyPrefix  = "id_ed25519_"
	sshDirPerm = 0o700

	// gitHubHost is the probe target. GitHub always refuses the shell, so this
	// connection can never do anything beyond authenticating.
	gitHubHost = "git@github.com"
)

// successRe matches GitHub's authentication banner. The username is the only
// piece of identity information the probe can recover.
var successRe = regexp.MustCompile(`Hi ([^!]+)! You've successfully authenticated`)

// nonSlug matches every run of characters that may not appear in a key
// filename.
var nonSlug = regexp.MustCompile(`[^a-z0-9-]+`)

// Runner executes an external command and returns its combined output. It is
// the seam that keeps this package free of process and network calls in tests.
//
// Implementations must return combined stdout and stderr: OpenSSH writes its
// authentication banner to stderr, so stdout alone would be empty.
type Runner interface {
	Run(ctx context.Context, args ...string) (output string, err error)
}

// ProbeResult is the verdict of a single connectivity check.
type ProbeResult struct {
	Authenticated bool
	Username      string
	Raw           string
}

// Manager owns key generation and probing. keygen runs ssh-keygen, prober runs
// ssh; either may be nil when the caller only needs the other.
type Manager struct {
	keygen Runner
	prober Runner
}

// New returns a Manager backed by the given runners.
func New(keygen, prober Runner) *Manager {
	return &Manager{keygen: keygen, prober: prober}
}

// Slug normalises a profile name into a filename-safe token: lower case, with
// every run of other characters collapsed to a single dash.
func Slug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nonSlug.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// KeyPathFor returns the private key path for a profile. home is a parameter
// rather than a lookup so tests can inject a temporary directory.
func KeyPathFor(home, profileName string) string {
	return filepath.Join(home, sshDirName, keyPrefix+Slug(profileName))
}

// Generate creates an ed25519 key pair for the profile and returns its private
// key path. The passphrase is empty so that core.sshCommand can authenticate
// unattended; the key's security therefore rests on its file permissions.
func (m *Manager) Generate(ctx context.Context, home, profileName, email string) (string, error) {
	keyPath := KeyPathFor(home, profileName)

	if _, err := os.Stat(keyPath); err == nil {
		return "", fmt.Errorf("%w: %s", ErrKeyExists, keyPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect key path: %w", err)
	}

	dir := filepath.Dir(keyPath)
	if err := os.MkdirAll(dir, sshDirPerm); err != nil {
		return "", fmt.Errorf("create ssh dir: %w", err)
	}
	// MkdirAll applies the umask and no-ops on an existing directory, so the
	// mode is asserted explicitly.
	if err := os.Chmod(dir, sshDirPerm); err != nil {
		return "", fmt.Errorf("secure ssh dir: %w", err)
	}

	args := []string{
		"-t", "ed25519",
		"-C", email,
		"-f", keyPath,
		"-N", "",
	}
	if _, err := m.keygen.Run(ctx, args...); err != nil {
		return "", fmt.Errorf("generate ed25519 key: %w", err)
	}
	return keyPath, nil
}

// PublicKey reads the public half of a key pair, ready to paste into a
// provider's settings page.
func PublicKey(keyPath string) (string, error) {
	raw, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return "", fmt.Errorf("read public key: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// ProbeGitHub opens an authentication-only SSH session to GitHub using the
// given key.
//
// The exit status is deliberately ignored. "ssh -T git@github.com" exits 1 on
// a *successful* authentication, because GitHub declines to provide a shell,
// so the verdict is read from the output. The underlying error surfaces only
// when the output carries no verdict at all.
func (m *Manager) ProbeGitHub(ctx context.Context, keyPath string) (ProbeResult, error) {
	args := []string{
		"-T",
		"-i", keyPath,
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		// BatchMode stops ssh from blocking on a passphrase or host prompt,
		// which would hang the concurrent verify command.
		"-o", "BatchMode=yes",
		gitHubHost,
	}

	out, runErr := m.prober.Run(ctx, args...)
	authenticated, username := ParseProbeOutput(out)
	result := ProbeResult{Authenticated: authenticated, Username: username, Raw: strings.TrimSpace(out)}

	if authenticated || isDenied(out) {
		return result, nil
	}
	if runErr != nil {
		return result, fmt.Errorf("probe github: %w", runErr)
	}
	return result, nil
}

// ParseProbeOutput extracts the authentication verdict and GitHub username
// from an ssh transcript.
func ParseProbeOutput(out string) (bool, string) {
	if match := successRe.FindStringSubmatch(out); match != nil {
		return true, strings.TrimSpace(match[1])
	}
	return false, ""
}

// isDenied reports whether the transcript states that the key was rejected. A
// rejection is a verdict, not a malfunction.
func isDenied(out string) bool {
	return strings.Contains(out, "Permission denied")
}
