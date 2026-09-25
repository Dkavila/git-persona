package ssh_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Dkavila/git-persona/internal/ssh"
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
	home := filepath.Join("/home", "derick")
	want := filepath.Join(home, ".ssh", "id_ed25519_work-profile")

	if got := ssh.KeyPathFor(home, "Work Profile"); got != want {
		t.Fatalf("KeyPathFor() = %q, want %q", got, want)
	}
}

func TestGenerate_UsesEd25519Args(t *testing.T) {
	home := t.TempDir()
	r := &fakeRunner{}
	m := ssh.New(r, nil)

	keyPath, err := m.Generate(context.Background(), home, "work", "derick@corp.com")
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	wantPath := ssh.KeyPathFor(home, "work")
	if keyPath != wantPath {
		t.Fatalf("keyPath = %q, want %q", keyPath, wantPath)
	}

	want := [][]string{{
		"-t", "ed25519",
		"-C", "derick@corp.com",
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

	_, err := m.Generate(context.Background(), home, "work", "derick@corp.com")
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

	_, err := m.Generate(context.Background(), home, "work", "derick@corp.com")
	if !errors.Is(err, boom) {
		t.Fatalf("Generate() error = %v, want it to wrap the runner error", err)
	}
}

func TestPublicKey_ReadsPubFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519_work")
	content := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI derick@corp.com"

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
	r := &fakeRunner{output: "Hi Dkavila! You've successfully authenticated, but GitHub does not provide shell access."}
	m := ssh.New(nil, r)

	if _, err := m.ProbeGitHub(context.Background(), "/home/derick/.ssh/id_ed25519_work"); err != nil {
		t.Fatalf("ProbeGitHub() = %v, want nil", err)
	}

	want := [][]string{{
		"-T",
		"-i", "/home/derick/.ssh/id_ed25519_work",
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
			out:          "Hi Dkavila! You've successfully authenticated, but GitHub does not provide shell access.",
			wantAuth:     true,
			wantUsername: "Dkavila",
		},
		{
			name:         "success with surrounding noise",
			out:          "Warning: Permanently added 'github.com' to the list of known hosts.\nHi derick-work! You've successfully authenticated, but GitHub does not provide shell access.\n",
			wantAuth:     true,
			wantUsername: "derick-work",
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
		output: "Hi Dkavila! You've successfully authenticated, but GitHub does not provide shell access.",
		err:    errors.New("exit status 1"),
	}
	m := ssh.New(nil, r)

	auth, err := m.ProbeGitHub(context.Background(), "/home/derick/.ssh/id_ed25519_work")
	if err != nil {
		t.Fatalf("ProbeGitHub() = %v, want nil despite the non-zero exit", err)
	}
	if !auth.Authenticated {
		t.Fatal("Authenticated = false, want true")
	}
	if auth.Username != "Dkavila" {
		t.Fatalf("Username = %q, want %q", auth.Username, "Dkavila")
	}
}

func TestProbeGitHub_PermissionDeniedIsNotAnError(t *testing.T) {
	r := &fakeRunner{
		output: "git@github.com: Permission denied (publickey).",
		err:    errors.New("exit status 255"),
	}
	m := ssh.New(nil, r)

	auth, err := m.ProbeGitHub(context.Background(), "/home/derick/.ssh/id_ed25519_work")
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

	if _, err := m.ProbeGitHub(context.Background(), "/home/derick/.ssh/id_ed25519_work"); !errors.Is(err, boom) {
		t.Fatalf("ProbeGitHub() error = %v, want it to wrap the runner error", err)
	}
}

func TestProbeGitHub_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := &fakeRunner{output: "", err: context.Canceled}
	m := ssh.New(nil, r)

	if _, err := m.ProbeGitHub(ctx, "/home/derick/.ssh/id_ed25519_work"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ProbeGitHub() error = %v, want context.Canceled", err)
	}
}
