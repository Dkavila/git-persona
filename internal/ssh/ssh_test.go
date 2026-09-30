package ssh_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Dkavila/git-pilot/internal/ssh"
)

// fakeRunner records argv and replays a scripted result, so no test in this
// package runs ssh-keygen or opens a network connection.
type fakeRunner struct {
	calls  [][]string
	output string
	err    error
}

func (f *fakeRunner) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	return f.output, f.err
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"work", "work"},
		{"Work", "work"},
		{"WORK", "work"},
		{"Work Profile", "work-profile"},
		{"  spaced  ", "spaced"},
		{"work@corp", "work-corp"},
		{"work..eu", "work-eu"},
		{"EZ Ops", "ez-ops"},
		{"client-42", "client-42"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ssh.Slug(tt.in); got != tt.want {
				t.Fatalf("Slug(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestKeyPathFor(t *testing.T) {
	home := filepath.Join("/home", "git-pilot")
	want := filepath.Join(home, ".ssh", "id_ed25519_work-profile")

	if got := ssh.KeyPathFor(home, "Work Profile"); got != want {
		t.Fatalf("KeyPathFor() = %q, want %q", got, want)
	}
}

func TestGenerate_UsesEd25519Args(t *testing.T) {
	home := t.TempDir()
	r := &fakeRunner{}
	m := ssh.New(r, nil)

	keyPath, err := m.Generate(context.Background(), home, "work", "dev@acme-corp.com")
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	wantPath := ssh.KeyPathFor(home, "work")
	if keyPath != wantPath {
		t.Fatalf("keyPath = %q, want %q", keyPath, wantPath)
	}

	want := [][]string{{
		"-t", "ed25519",
		"-C", "dev@acme-corp.com",
		"-f", wantPath,
		"-N", "",
	}}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("argv =\n%q\nwant\n%q", r.calls, want)
	}
}

// Overwriting a key would silently destroy an identity that may be registered
// with a remote. The caller must remove it deliberately.
func TestGenerate_RefusesOverwrite(t *testing.T) {
	home := t.TempDir()
	keyPath := ssh.KeyPathFor(home, "work")

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("existing key"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	r := &fakeRunner{}
	m := ssh.New(r, nil)

	_, err := m.Generate(context.Background(), home, "work", "dev@acme-corp.com")
	if !errors.Is(err, ssh.ErrKeyExists) {
		t.Fatalf("Generate() error = %v, want ErrKeyExists", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("len(calls) = %d, want 0 (ssh-keygen must not run)", len(r.calls))
	}
}

func TestGenerate_PropagatesKeygenFailure(t *testing.T) {
	home := t.TempDir()
	boom := errors.New("ssh-keygen exploded")
	r := &fakeRunner{err: boom}
	m := ssh.New(r, nil)

	_, err := m.Generate(context.Background(), home, "work", "dev@acme-corp.com")
	if !errors.Is(err, boom) {
		t.Fatalf("Generate() error = %v, want it to wrap the runner error", err)
	}
}

func TestPublicKey_ReadsPubFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519_work")
	content := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI dev@acme-corp.com"

	if err := os.WriteFile(keyPath+".pub", []byte(content+"\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := ssh.PublicKey(keyPath)
	if err != nil {
		t.Fatalf("PublicKey() = %v, want nil", err)
	}
	if got != content {
		t.Fatalf("PublicKey() = %q, want %q (trailing newline trimmed)", got, content)
	}
}

func TestPublicKey_MissingFile(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id_ed25519_ghost")

	if _, err := ssh.PublicKey(keyPath); err == nil {
		t.Fatal("PublicKey() = nil, want error for a missing .pub file")
	}
}

func TestProbeGitHub_Argv(t *testing.T) {
	r := &fakeRunner{output: "Hi octocat! You've successfully authenticated, but GitHub does not provide shell access."}
	m := ssh.New(nil, r)

	if _, err := m.ProbeGitHub(context.Background(), "/home/user/.ssh/id_ed25519_work"); err != nil {
		t.Fatalf("ProbeGitHub() = %v, want nil", err)
	}

	want := [][]string{{
		"-T",
		"-i", "/home/user/.ssh/id_ed25519_work",
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		"git@github.com",
	}}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("argv =\n%q\nwant\n%q", r.calls, want)
	}
}

func TestParseProbeOutput(t *testing.T) {
	tests := []struct {
		name         string
		out          string
		wantAuth     bool
		wantUsername string
	}{
		{
			name:         "success",
			out:          "Hi octocat! You've successfully authenticated, but GitHub does not provide shell access.",
			wantAuth:     true,
			wantUsername: "octocat",
		},
		{
			name:         "success with surrounding noise",
			out:          "Warning: Permanently added 'github.com' to the list of known hosts.\nHi git-pilot-work! You've successfully authenticated, but GitHub does not provide shell access.\n",
			wantAuth:     true,
			wantUsername: "git-pilot-work",
		},
		{
			name:         "permission denied",
			out:          "git@github.com: Permission denied (publickey).",
			wantAuth:     false,
			wantUsername: "",
		},
		{
			name:         "empty output",
			out:          "",
			wantAuth:     false,
			wantUsername: "",
		},
		{
			name:         "unrelated output",
			out:          "ssh: connect to host github.com port 22: Connection timed out",
			wantAuth:     false,
			wantUsername: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth, username := ssh.ParseProbeOutput(tt.out)
			if auth != tt.wantAuth {
				t.Fatalf("authenticated = %v, want %v", auth, tt.wantAuth)
			}
			if username != tt.wantUsername {
				t.Fatalf("username = %q, want %q", username, tt.wantUsername)
			}
		})
	}
}

// This is the single most important rule in the package. "ssh -T git@github.com"
// exits 1 even when authentication succeeds, because GitHub refuses the shell.
// The verdict must come from the output, never from the exit status.
func TestProbeGitHub_NonZeroExitIsStillSuccess(t *testing.T) {
	r := &fakeRunner{
		output: "Hi octocat! You've successfully authenticated, but GitHub does not provide shell access.",
		err:    errors.New("exit status 1"),
	}
	m := ssh.New(nil, r)

	auth, err := m.ProbeGitHub(context.Background(), "/home/user/.ssh/id_ed25519_work")
	if err != nil {
		t.Fatalf("ProbeGitHub() = %v, want nil despite the non-zero exit", err)
	}
	if !auth.Authenticated {
		t.Fatal("Authenticated = false, want true")
	}
	if auth.Username != "octocat" {
		t.Fatalf("Username = %q, want %q", auth.Username, "octocat")
	}
}

func TestProbeGitHub_PermissionDeniedIsNotAnError(t *testing.T) {
	r := &fakeRunner{
		output: "git@github.com: Permission denied (publickey).",
		err:    errors.New("exit status 255"),
	}
	m := ssh.New(nil, r)

	auth, err := m.ProbeGitHub(context.Background(), "/home/user/.ssh/id_ed25519_work")
	if err != nil {
		t.Fatalf("ProbeGitHub() = %v, want nil (a rejected key is a verdict, not a crash)", err)
	}
	if auth.Authenticated {
		t.Fatal("Authenticated = true, want false")
	}
}

// When the output carries no recognisable verdict, the underlying failure is
// the only information available and must not be swallowed.
func TestProbeGitHub_UnrecognisedOutputPropagatesError(t *testing.T) {
	boom := errors.New("exec: \"ssh\": executable file not found in $PATH")
	r := &fakeRunner{output: "", err: boom}
	m := ssh.New(nil, r)

	if _, err := m.ProbeGitHub(context.Background(), "/home/user/.ssh/id_ed25519_work"); !errors.Is(err, boom) {
		t.Fatalf("ProbeGitHub() error = %v, want it to wrap the runner error", err)
	}
}

func TestProbeGitHub_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := &fakeRunner{output: "", err: context.Canceled}
	m := ssh.New(nil, r)

	if _, err := m.ProbeGitHub(ctx, "/home/user/.ssh/id_ed25519_work"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ProbeGitHub() error = %v, want context.Canceled", err)
	}
}

func TestRemoveKeyPair_DeletesBothFiles(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519_work")

	if err := os.WriteFile(keyPath, []byte("PRIVATE"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath+".pub", []byte("PUBLIC"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := ssh.RemoveKeyPair(keyPath); err != nil {
		t.Fatalf("RemoveKeyPair() = %v, want nil", err)
	}

	for _, p := range []string{keyPath, keyPath + ".pub"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists after RemoveKeyPair()", p)
		}
	}
}

// Idempotent on purpose: purging a profile whose key was already deleted by
// hand must succeed rather than block the profile removal.
func TestRemoveKeyPair_MissingFilesAreNotAnError(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id_ed25519_ghost")

	if err := ssh.RemoveKeyPair(keyPath); err != nil {
		t.Fatalf("RemoveKeyPair() = %v, want nil for absent files", err)
	}
}

// Half a key pair is still worth cleaning up.
func TestRemoveKeyPair_RemovesPrivateWhenPublicIsMissing(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519_work")

	if err := os.WriteFile(keyPath, []byte("PRIVATE"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := ssh.RemoveKeyPair(keyPath); err != nil {
		t.Fatalf("RemoveKeyPair() = %v, want nil", err)
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private key still exists after RemoveKeyPair()")
	}
}

func TestKeyExists(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519_work")

	t.Run("absent", func(t *testing.T) {
		got, err := ssh.KeyExists(keyPath)
		if err != nil {
			t.Fatalf("KeyExists() = %v, want nil", err)
		}
		if got {
			t.Fatal("KeyExists() = true, want false")
		}
	})

	t.Run("present", func(t *testing.T) {
		if err := os.WriteFile(keyPath, []byte("PRIVATE"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}
		got, err := ssh.KeyExists(keyPath)
		if err != nil {
			t.Fatalf("KeyExists() = %v, want nil", err)
		}
		if !got {
			t.Fatal("KeyExists() = false, want true")
		}
	})
}

// Overwrite is the deliberate counterpart to Generate: it removes the existing
// pair first, so the refusal built into Generate does not block a user who has
// explicitly asked to replace the key.
func TestOverwrite_ReplacesAnExistingKey(t *testing.T) {
	home := t.TempDir()
	keyPath := ssh.KeyPathFor(home, "work")

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("OLD PRIVATE"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath+".pub", []byte("OLD PUBLIC"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	r := &fakeRunner{}
	m := ssh.New(r, nil)

	got, err := m.Overwrite(context.Background(), home, "work", "dev@acme-corp.com")
	if err != nil {
		t.Fatalf("Overwrite() = %v, want nil", err)
	}
	if got != keyPath {
		t.Fatalf("keyPath = %q, want %q", got, keyPath)
	}

	want := [][]string{{
		"-t", "ed25519",
		"-C", "dev@acme-corp.com",
		"-f", keyPath,
		"-N", "",
	}}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("argv =\n%q\nwant\n%q", r.calls, want)
	}

	// The stale files must be gone before ssh-keygen runs, otherwise keygen
	// itself would prompt for confirmation and hang.
	if _, err := os.Stat(keyPath + ".pub"); !errors.Is(err, os.ErrNotExist) {
		t.Error("the old public key survived Overwrite()")
	}
}

func TestOverwrite_WorksWhenNoKeyExists(t *testing.T) {
	home := t.TempDir()
	r := &fakeRunner{}
	m := ssh.New(r, nil)

	if _, err := m.Overwrite(context.Background(), home, "work", "dev@acme-corp.com"); err != nil {
		t.Fatalf("Overwrite() = %v, want nil", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("keygen calls = %d, want 1", len(r.calls))
	}
}

// --- Overwrite crash safety ---------------------------------------------------

// The old pair must survive a failed regeneration. Removing it before running
// ssh-keygen leaves the user with nothing at all when keygen fails: the old key
// is gone and the new one was never written. The old key may be registered
// with a provider, so losing it locks the account out.
func TestOverwrite_RestoresTheOldKeyWhenKeygenFails(t *testing.T) {
	home := t.TempDir()
	keyPath := ssh.KeyPathFor(home, "work")

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("ORIGINAL PRIVATE"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath+".pub", []byte("ORIGINAL PUBLIC"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	boom := errors.New("ssh-keygen: no space left on device")
	m := ssh.New(&fakeRunner{err: boom}, nil)

	_, err := m.Overwrite(context.Background(), home, "work", "dev@acme-corp.com")
	if !errors.Is(err, boom) {
		t.Fatalf("Overwrite() error = %v, want it to wrap the keygen failure", err)
	}

	priv, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("the private key was not restored: %v", err)
	}
	if string(priv) != "ORIGINAL PRIVATE" {
		t.Fatalf("private key = %q, want the original contents", priv)
	}

	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatalf("the public key was not restored: %v", err)
	}
	if string(pub) != "ORIGINAL PUBLIC" {
		t.Fatalf("public key = %q, want the original contents", pub)
	}
}

// A failed overwrite must leave the directory exactly as it found it, with no
// half-renamed leftovers for the next run to trip over.
func TestOverwrite_LeavesNoBackupBehindOnFailure(t *testing.T) {
	home := t.TempDir()
	keyPath := ssh.KeyPathFor(home, "work")

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("ORIGINAL PRIVATE"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath+".pub", []byte("ORIGINAL PUBLIC"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	m := ssh.New(&fakeRunner{err: errors.New("keygen exploded")}, nil)
	_, _ = m.Overwrite(context.Background(), home, "work", "dev@acme-corp.com")

	entries, err := os.ReadDir(filepath.Dir(keyPath))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			t.Errorf("stray backup left behind: %s", e.Name())
		}
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %d entries (%v), want exactly the two original files", len(entries), names)
	}
}

// On success the backup has served its purpose and must not linger: a stale
// .bak beside a live key is confusing and is still usable key material.
func TestOverwrite_DiscardsTheBackupOnSuccess(t *testing.T) {
	home := t.TempDir()
	keyPath := ssh.KeyPathFor(home, "work")

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("ORIGINAL PRIVATE"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath+".pub", []byte("ORIGINAL PUBLIC"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// The fake keygen succeeds without writing anything, which is enough to
	// prove the backup handling.
	m := ssh.New(&fakeRunner{}, nil)

	if _, err := m.Overwrite(context.Background(), home, "work", "dev@acme-corp.com"); err != nil {
		t.Fatalf("Overwrite() = %v, want nil", err)
	}

	for _, suffix := range []string{".bak", ".pub.bak"} {
		if _, err := os.Stat(keyPath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("backup %s survived a successful overwrite", keyPath+suffix)
		}
	}
}

// ssh-keygen must not find the old files in place, or it prompts for
// confirmation and hangs an unattended run.
func TestOverwrite_OldKeyIsOutOfTheWayWhenKeygenRuns(t *testing.T) {
	home := t.TempDir()
	keyPath := ssh.KeyPathFor(home, "work")

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("ORIGINAL PRIVATE"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var presentDuringKeygen bool
	r := &inspectingRunner{
		before: func() {
			_, err := os.Stat(keyPath)
			presentDuringKeygen = err == nil
		},
	}
	m := ssh.New(r, nil)

	if _, err := m.Overwrite(context.Background(), home, "work", "dev@acme-corp.com"); err != nil {
		t.Fatalf("Overwrite() = %v, want nil", err)
	}
	if presentDuringKeygen {
		t.Fatal("the old private key was still in place when ssh-keygen ran")
	}
}

// A leftover .bak from an earlier interrupted run must not block a new one.
func TestOverwrite_ToleratesAStaleBackup(t *testing.T) {
	home := t.TempDir()
	keyPath := ssh.KeyPathFor(home, "work")

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("CURRENT"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(keyPath+".bak", []byte("STALE FROM A PREVIOUS CRASH"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	m := ssh.New(&fakeRunner{}, nil)

	if _, err := m.Overwrite(context.Background(), home, "work", "dev@acme-corp.com"); err != nil {
		t.Fatalf("Overwrite() = %v, want nil despite the stale backup", err)
	}
}

// inspectingRunner lets a test observe the filesystem at the instant the
// command would run.
type inspectingRunner struct {
	before func()
	calls  [][]string
}

func (r *inspectingRunner) Run(_ context.Context, args ...string) (string, error) {
	if r.before != nil {
		r.before()
	}
	r.calls = append(r.calls, args)
	return "", nil
}
