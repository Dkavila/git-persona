package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	dirName  = ".git-persona"
	fileName = "profiles.json"

	// The store sits next to SSH key material, so it is owner-only.
	dirPerm  = 0o700
	filePerm = 0o600
)

// Store is the full persisted state: the known profiles and the name of the
// one currently applied to the global Git config.
type Store struct {
	Profiles []Profile `json:"profiles"`
	Active   string    `json:"active"`
}

// StorePath returns the location of the JSON store for the given home
// directory. home is a parameter rather than a lookup so tests can inject a
// temporary directory.
func StorePath(home string) string {
	return filepath.Join(home, dirName, fileName)
}

// Load reads the store. A missing file is not an error: it means this is the
// first run, so an empty store is returned. A file that exists but cannot be
// decoded yields an error wrapping ErrCorruptStore.
func Load(home string) (*Store, error) {
	raw, err := os.ReadFile(StorePath(home))
	if errors.Is(err, os.ErrNotExist) {
		return &Store{Profiles: []Profile{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read profile store: %w", err)
	}

	var s Store
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptStore, err)
	}
	if s.Profiles == nil {
		s.Profiles = []Profile{}
	}
	return &s, nil
}

// Save writes the store atomically: it renders to a temporary file in the same
// directory and renames it into place, so an interrupted write cannot leave a
// truncated store behind.
func (s *Store) Save(home string) error {
	dir := filepath.Join(home, dirName)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	// MkdirAll applies the umask and is a no-op on an existing directory, so
	// the mode is asserted explicitly.
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("secure config dir: %w", err)
	}

	if s.Profiles == nil {
		s.Profiles = []Profile{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode profile store: %w", err)
	}
	data = append(data, '\n')

	path := StorePath(home)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, filePerm); err != nil {
		return fmt.Errorf("write profile store: %w", err)
	}
	if err := os.Chmod(tmp, filePerm); err != nil {
		return fmt.Errorf("secure profile store: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit profile store: %w", err)
	}
	return nil
}

// indexOf finds a profile by name, case-insensitively, and returns -1 when
// absent.
func (s *Store) indexOf(name string) int {
	target := strings.ToLower(strings.TrimSpace(name))
	for i, p := range s.Profiles {
		if strings.ToLower(p.Name) == target {
			return i
		}
	}
	return -1
}

// Add validates and appends a profile. Names are unique case-insensitively,
// because the name maps onto an SSH key filename.
func (s *Store) Add(p Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if s.indexOf(p.Name) >= 0 {
		return fmt.Errorf("%w: %q", ErrDuplicateProfile, p.Name)
	}
	s.Profiles = append(s.Profiles, p)
	return nil
}

// Get looks a profile up by name, case-insensitively.
func (s *Store) Get(name string) (Profile, bool) {
	i := s.indexOf(name)
	if i < 0 {
		return Profile{}, false
	}
	return s.Profiles[i], true
}

// SetActive marks a profile as the active identity. It records the canonical
// stored spelling rather than the caller's input, so output stays consistent.
func (s *Store) SetActive(name string) error {
	i := s.indexOf(name)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrProfileNotFound, name)
	}
	s.Active = s.Profiles[i].Name
	return nil
}

// Remove deletes a profile. Removing the active profile clears the active
// marker, leaving no dangling reference.
func (s *Store) Remove(name string) error {
	i := s.indexOf(name)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrProfileNotFound, name)
	}
	removed := s.Profiles[i]
	s.Profiles = append(s.Profiles[:i], s.Profiles[i+1:]...)
	if strings.EqualFold(s.Active, removed.Name) {
		s.Active = ""
	}
	return nil
}
