package config_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dkavila/git-persona/internal/config"
)

// fixedTime keeps round-trip comparisons deterministic. Never use time.Now()
// in these tests: JSON marshalling strips the monotonic clock reading and
// reflect.DeepEqual would then fail for reasons unrelated to the code.
var fixedTime = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func sampleProfile(name, email string) config.Profile {
	return config.Profile{
		Name:      name,
		Email:     email,
		KeyPath:   filepath.Join("/home/git-persona/.ssh", "id_ed25519_"+name),
		CreatedAt: fixedTime,
	}
}

func TestProfileValidate(t *testing.T) {
	tests := []struct {
		name    string
		profile config.Profile
		wantErr bool
	}{
		{"valid", sampleProfile("work", "git-persona@corp.com"), false},
		{"valid with dash", sampleProfile("work-eu", "d@corp.com"), false},
		{"empty name", sampleProfile("", "d@corp.com"), true},
		{"whitespace name", sampleProfile("   ", "d@corp.com"), true},
		{"empty email", sampleProfile("work", ""), true},
		{"malformed email", sampleProfile("work", "not-an-email"), true},
		{"email without domain", sampleProfile("work", "git-persona@"), true},
		{"name with forward slash", sampleProfile("work/eu", "d@corp.com"), true},
		{"name with backslash", sampleProfile("work\\eu", "d@corp.com"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.profile.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() = nil, want error")
				}
				if !errors.Is(err, config.ErrInvalidProfile) {
					t.Fatalf("Validate() error = %v, want it to wrap ErrInvalidProfile", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestStorePath(t *testing.T) {
	home := filepath.Join("/tmp", "fakehome")
	want := filepath.Join(home, ".git-persona", "profiles.json")

	if got := config.StorePath(home); got != want {
		t.Fatalf("StorePath() = %q, want %q", got, want)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	home := t.TempDir()

	store, err := config.Load(home)
	if err != nil {
		t.Fatalf("Load() on missing file = %v, want nil (a first run is not an error)", err)
	}
	if store == nil {
		t.Fatal("Load() returned nil store, want an empty store")
	}
	if len(store.Profiles) != 0 {
		t.Fatalf("len(Profiles) = %d, want 0", len(store.Profiles))
	}
	if store.Active != "" {
		t.Fatalf("Active = %q, want empty", store.Active)
	}
}

func TestLoad_CorruptJSON(t *testing.T) {
	home := t.TempDir()
	path := config.StorePath(home)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := config.Load(home)
	if err == nil {
		t.Fatal("Load() on corrupt file = nil, want error")
	}
	if !errors.Is(err, config.ErrCorruptStore) {
		t.Fatalf("Load() error = %v, want it to wrap ErrCorruptStore", err)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	home := t.TempDir()

	original := &config.Store{
		Profiles: []config.Profile{
			sampleProfile("work", "git-persona@corp.com"),
			sampleProfile("personal", "git@gmail.com"),
		},
		Active: "work",
	}

	if err := original.Save(home); err != nil {
		t.Fatalf("Save() = %v, want nil", err)
	}

	loaded, err := config.Load(home)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if loaded.Active != original.Active {
		t.Fatalf("Active = %q, want %q", loaded.Active, original.Active)
	}
	if len(loaded.Profiles) != len(original.Profiles) {
		t.Fatalf("len(Profiles) = %d, want %d", len(loaded.Profiles), len(original.Profiles))
	}

	for i, want := range original.Profiles {
		got := loaded.Profiles[i]
		if got.Name != want.Name || got.Email != want.Email || got.KeyPath != want.KeyPath {
			t.Fatalf("Profiles[%d] = %+v, want %+v", i, got, want)
		}
		if !got.CreatedAt.Equal(want.CreatedAt) {
			t.Fatalf("Profiles[%d].CreatedAt = %v, want %v", i, got.CreatedAt, want.CreatedAt)
		}
	}
}

// TestSave_JSONShape locks the on-disk format. The file is user-editable, so
// the key names are part of the contract and must not drift silently.
func TestSave_JSONShape(t *testing.T) {
	home := t.TempDir()

	store := &config.Store{
		Profiles: []config.Profile{sampleProfile("work", "git-persona@corp.com")},
		Active:   "work",
	}
	if err := store.Save(home); err != nil {
		t.Fatalf("Save() = %v, want nil", err)
	}

	raw, err := os.ReadFile(config.StorePath(home))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("saved file is not valid json: %v", err)
	}
	for _, key := range []string{"profiles", "active"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("saved json missing key %q, got keys %v", key, decoded)
		}
	}
}

func TestAdd_DuplicateName(t *testing.T) {
	store := &config.Store{}

	if err := store.Add(sampleProfile("work", "git-persona@corp.com")); err != nil {
		t.Fatalf("first Add() = %v, want nil", err)
	}

	err := store.Add(sampleProfile("work", "other@corp.com"))
	if !errors.Is(err, config.ErrDuplicateProfile) {
		t.Fatalf("duplicate Add() error = %v, want ErrDuplicateProfile", err)
	}
	if len(store.Profiles) != 1 {
		t.Fatalf("len(Profiles) = %d, want 1 (rejected profile must not be stored)", len(store.Profiles))
	}
}

// Profile names map onto SSH key filenames, so "Work" and "work" would collide
// on disk. Uniqueness is therefore case-insensitive.
func TestAdd_DuplicateNameIsCaseInsensitive(t *testing.T) {
	store := &config.Store{}

	if err := store.Add(sampleProfile("work", "git-persona@corp.com")); err != nil {
		t.Fatalf("first Add() = %v, want nil", err)
	}

	err := store.Add(sampleProfile("WORK", "other@corp.com"))
	if !errors.Is(err, config.ErrDuplicateProfile) {
		t.Fatalf("case-variant Add() error = %v, want ErrDuplicateProfile", err)
	}
}

func TestAdd_RejectsInvalidProfile(t *testing.T) {
	store := &config.Store{}

	err := store.Add(sampleProfile("work", "not-an-email"))
	if !errors.Is(err, config.ErrInvalidProfile) {
		t.Fatalf("Add() error = %v, want ErrInvalidProfile", err)
	}
	if len(store.Profiles) != 0 {
		t.Fatalf("len(Profiles) = %d, want 0", len(store.Profiles))
	}
}

func TestGet(t *testing.T) {
	store := &config.Store{}
	if err := store.Add(sampleProfile("work", "git-persona@corp.com")); err != nil {
		t.Fatalf("setup: %v", err)
	}

	t.Run("found", func(t *testing.T) {
		got, ok := store.Get("work")
		if !ok {
			t.Fatal("Get(\"work\") ok = false, want true")
		}
		if got.Email != "git-persona@corp.com" {
			t.Fatalf("Email = %q, want %q", got.Email, "git-persona@corp.com")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		if _, ok := store.Get("WoRk"); !ok {
			t.Fatal("Get(\"WoRk\") ok = false, want true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		if _, ok := store.Get("missing"); ok {
			t.Fatal("Get(\"missing\") ok = true, want false")
		}
	})
}

func TestSetActive_UpdatesActiveField(t *testing.T) {
	store := &config.Store{}
	if err := store.Add(sampleProfile("work", "git-persona@corp.com")); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := store.Add(sampleProfile("personal", "git-persona@gmail.com")); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := store.SetActive("personal"); err != nil {
		t.Fatalf("SetActive() = %v, want nil", err)
	}
	if store.Active != "personal" {
		t.Fatalf("Active = %q, want %q", store.Active, "personal")
	}
}

// SetActive must persist the canonical stored name, not whatever casing the
// user typed, so that list output and key paths stay consistent.
func TestSetActive_StoresCanonicalName(t *testing.T) {
	store := &config.Store{}
	if err := store.Add(sampleProfile("work", "git-persona@corp.com")); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := store.SetActive("WORK"); err != nil {
		t.Fatalf("SetActive() = %v, want nil", err)
	}
	if store.Active != "work" {
		t.Fatalf("Active = %q, want canonical %q", store.Active, "work")
	}
}

func TestSetActive_UnknownProfile(t *testing.T) {
	store := &config.Store{}
	if err := store.Add(sampleProfile("work", "git-persona@corp.com")); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := store.SetActive("work"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := store.SetActive("ghost")
	if !errors.Is(err, config.ErrProfileNotFound) {
		t.Fatalf("SetActive() error = %v, want ErrProfileNotFound", err)
	}
	if store.Active != "work" {
		t.Fatalf("Active = %q, want it unchanged at %q", store.Active, "work")
	}
}

func TestRemove(t *testing.T) {
	t.Run("removes profile and clears active", func(t *testing.T) {
		store := &config.Store{}
		if err := store.Add(sampleProfile("work", "git-persona@corp.com")); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := store.Add(sampleProfile("personal", "git-persona@gmail.com")); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := store.SetActive("work"); err != nil {
			t.Fatalf("setup: %v", err)
		}

		if err := store.Remove("work"); err != nil {
			t.Fatalf("Remove() = %v, want nil", err)
		}
		if _, ok := store.Get("work"); ok {
			t.Fatal("profile still present after Remove()")
		}
		if store.Active != "" {
			t.Fatalf("Active = %q, want empty after removing the active profile", store.Active)
		}
		if len(store.Profiles) != 1 {
			t.Fatalf("len(Profiles) = %d, want 1", len(store.Profiles))
		}
	})

	t.Run("keeps active when another profile is removed", func(t *testing.T) {
		store := &config.Store{}
		if err := store.Add(sampleProfile("work", "git-persona@corp.com")); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := store.Add(sampleProfile("personal", "git-persona@gmail.com")); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := store.SetActive("work"); err != nil {
			t.Fatalf("setup: %v", err)
		}

		if err := store.Remove("personal"); err != nil {
			t.Fatalf("Remove() = %v, want nil", err)
		}
		if store.Active != "work" {
			t.Fatalf("Active = %q, want %q", store.Active, "work")
		}
	})

	t.Run("unknown profile", func(t *testing.T) {
		store := &config.Store{}
		err := store.Remove("ghost")
		if !errors.Is(err, config.ErrProfileNotFound) {
			t.Fatalf("Remove() error = %v, want ErrProfileNotFound", err)
		}
	})
}
