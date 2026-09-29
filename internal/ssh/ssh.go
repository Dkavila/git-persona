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

// KeyExists reports whether a private key is already present at keyPath.
//
// It is separate from Generate so callers can decide what to do about an
// existing key before any irreversible step is taken.
func KeyExists(keyPath string) (bool, error) {
	_, err := os.Stat(keyPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect key path: %w", err)
}

// backupSuffix marks the temporary copy taken before a key is replaced.
const backupSuffix = ".bak"

// keyBackup records where a key pair was moved so it can be put back.
type keyBackup struct {
	keyPath string
	// moved lists the pairs actually renamed, as {from, to}. Only files that
	// existed are recorded, so restoring never invents a file.
	moved [][2]string
}

// Overwrite replaces an existing key pair with a freshly generated one.
//
// It is the deliberate counterpart to Generate, which refuses to clobber.
//
// The old pair is moved aside rather than deleted, for two reasons. ssh-keygen
// prompts for confirmation when the target exists, which would hang an
// unattended run; and if generation then fails, the original is put back. The
// destructive alternative, deleting first, leaves the user with no key at all
// when ssh-keygen fails: the old one is gone and the new one was never
// written. That old key may be registered with a provider.
func (m *Manager) Overwrite(ctx context.Context, home, profileName, email string) (string, error) {
	keyPath := KeyPathFor(home, profileName)

	backup, err := moveKeyPairAside(keyPath)
	if err != nil {
		return "", fmt.Errorf("back up the existing key: %w", err)
	}

	newPath, genErr := m.Generate(ctx, home, profileName, email)
	if genErr != nil {
		if restoreErr := backup.restore(); restoreErr != nil {
			// Both failed. Say where the material is, because at this point
			// only the user can recover it.
			return "", fmt.Errorf(
				"%w (restoring the previous key also failed: %v; it is at %s%s)",
				genErr, restoreErr, keyPath, backupSuffix)
		}
		return "", genErr
	}

	// The new key is in place, so the backup is now stale key material and
	// must not linger beside it.
	backup.discard()
	return newPath, nil
}

// moveKeyPairAside renames both halves of a key pair out of the way. Files
// that do not exist are skipped, so a half pair or no pair at all is fine.
func moveKeyPairAside(keyPath string) (*keyBackup, error) {
	b := &keyBackup{keyPath: keyPath}

	for _, path := range []string{keyPath, keyPath + ".pub"} {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}

		// os.Rename replaces the destination on every supported platform, so a
		// stale .bak from an earlier interrupted run does not block this.
		dst := path + backupSuffix
		if err := os.Rename(path, dst); err != nil {
			// Undo whatever moved already, so a partial failure does not leave
			// the pair split between two names.
			_ = b.restore()
			return nil, err
		}
		b.moved = append(b.moved, [2]string{path, dst})
	}
	return b, nil
}

// restore puts every moved file back under its original name.
func (b *keyBackup) restore() error {
	var firstErr error
	for _, pair := range b.moved {
		if err := os.Rename(pair[1], pair[0]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		b.moved = nil
	}
	return firstErr
}

// discard deletes the backup once it is no longer needed. Failures are
// ignored: the new key is already in place, and refusing to return it because
// a leftover file could not be removed would be worse than the leftover.
func (b *keyBackup) discard() {
	for _, pair := range b.moved {
		_ = os.Remove(pair[1])
	}
	b.moved = nil
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

// RemoveKeyPair deletes both halves of a key pair. Files that are already gone
// are ignored, so purging a profile whose key was deleted by hand still
// succeeds rather than blocking the removal.
//
// This is irreversible: there is no recycle bin on this path, and a key
// registered with a provider cannot be recovered.
func RemoveKeyPair(keyPath string) error {
	for _, path := range []string{keyPath, keyPath + ".pub"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}
