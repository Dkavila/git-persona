//go:build !windows

package ssh_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dkavila/git-persona/internal/ssh"
)

// Unix-only: Windows has no POSIX mode bits. See the equivalent note in
// internal/config.
func TestGenerate_CreatesSSHDirWithPerms(t *testing.T) {
	home := t.TempDir()
	r := &fakeRunner{}
	m := ssh.New(r, nil)

	if _, err := m.Generate(context.Background(), home, "work", "derick@corp.com"); err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	info, err := os.Stat(filepath.Join(home, ".ssh"))
	if err != nil {
		t.Fatalf("stat .ssh: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf(".ssh perm = %o, want 0700", got)
	}
}
