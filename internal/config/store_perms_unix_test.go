//go:build !windows

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Dkavila/git-persona/internal/config"
)

// Unix-only: Windows does not implement POSIX permission bits. os.Chmod there
// only toggles the read-only attribute, so Mode().Perm() would report 0666 and
// this assertion could never pass. The permission guarantee itself still
// matters because the store sits next to SSH key material.
func TestSave_CreatesDirWithPerms(t *testing.T) {
	home := t.TempDir()

	store := &config.Store{
		Profiles: []config.Profile{
			{Name: "work", Email: "git-persona@corp.com", KeyPath: "/home/git-persona/.ssh/id_ed25519_work"},
		},
		Active: "work",
	}

	if err := store.Save(home); err != nil {
		t.Fatalf("Save() = %v, want nil", err)
	}

	dirInfo, err := os.Stat(filepath.Join(home, ".git-persona"))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("config dir perm = %o, want 0700", got)
	}

	fileInfo, err := os.Stat(config.StorePath(home))
	if err != nil {
		t.Fatalf("stat store file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("store file perm = %o, want 0600", got)
	}
}
