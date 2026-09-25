package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Dkavila/git-persona/internal/cli"
	"github.com/Dkavila/git-persona/internal/config"
	"github.com/Dkavila/git-persona/internal/ssh"
)

var fixedTime = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

// fakeGit records what the CLI asked the git layer to do.
type fakeGit struct {
	applied []config.Profile
	cleaned []string
	err     error
}

func (f *fakeGit) ApplyProfile(p config.Profile) error {
	f.applied = append(f.applied, p)
	return f.err
}

func (f *fakeGit) CleanLocal(path string) error {
	f.cleaned = append(f.cleaned, path)
	return f.err
}

type keygenCall struct{ home, name, email string }

// fakeKeys stands in for the ssh layer and writes real key files, so the CLI's
// "here is your public key" output can be asserted end to end.
type fakeKeys struct {
	calls []keygenCall
	err   error
	pub   string
}

func (f *fakeKeys) Generate(_ context.Context, home, name, email string) (string, error) {
	f.calls = append(f.calls, keygenCall{home, name, email})
	if f.err != nil {
		return "", f.err
	}

	keyPath := ssh.KeyPathFor(home, name)
	if err := os.MkdirAll(strings.TrimSuffix(keyPath, "/"+lastSegment(keyPath)), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(keyPath, []byte("PRIVATE"), 0o600); err != nil {
		return "", err
	}
	pub := f.pub
	if pub == "" {
		pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 " + email
	}
	if err := os.WriteFile(keyPath+".pub", []byte(pub+"\n"), 0o644); err != nil {
		return "", err
	}
	return keyPath, nil
}

func lastSegment(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i < 0 {
		return p
	}
	return p[i+1:]
}

// newTestCmd builds a root command wired to fakes, with output captured.
func newTestCmd(t *testing.T, home string, g *fakeGit, k *fakeKeys, stdin string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()

	out := &bytes.Buffer{}
	root := cli.NewRootCmd(cli.Deps{
		Home: home,
		Git:  g,
		Keys: k,
		Now:  func() time.Time { return fixedTime },
	})
	root.SetOut(out)
	root.SetErr(out)
	root.SetIn(strings.NewReader(stdin))
	return root, out
}

func seedStore(t *testing.T, home string, profiles []config.Profile, active string) {
	t.Helper()

	store := &config.Store{Active: active}
	for _, p := range profiles {
		if err := store.Add(p); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := store.Save(home); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func profile(name, email string) config.Profile {
	return config.Profile{
		Name:      name,
		Email:     email,
		KeyPath:   "/home/derick/.ssh/id_ed25519_" + name,
		CreatedAt: fixedTime,
	}
}

func TestRootCmd_HasExpectedSubcommands(t *testing.T) {
	root, _ := newTestCmd(t, t.TempDir(), &fakeGit{}, &fakeKeys{}, "")

	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}

	// verify is registered in Phase 5 and is deliberately absent here.
	for _, name := range []string{"add", "use", "list", "clean"} {
		if !got[name] {
			t.Errorf("subcommand %q not registered", name)
		}
	}
}

func TestAddCmd_PromptsAndPersists(t *testing.T) {
	home := t.TempDir()
	k := &fakeKeys{}
	root, out := newTestCmd(t, home, &fakeGit{}, k, "work\nderick@corp.com\n")
	root.SetArgs([]string{"add"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	if len(k.calls) != 1 {
		t.Fatalf("keygen calls = %d, want 1", len(k.calls))
	}
	if k.calls[0].name != "work" || k.calls[0].email != "derick@corp.com" {
		t.Fatalf("keygen call = %+v, want name=work email=derick@corp.com", k.calls[0])
	}

	store, err := config.Load(home)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	p, ok := store.Get("work")
	if !ok {
		t.Fatal("profile not persisted")
	}
	if p.Email != "derick@corp.com" {
		t.Fatalf("Email = %q, want %q", p.Email, "derick@corp.com")
	}
	if p.KeyPath != ssh.KeyPathFor(home, "work") {
		t.Fatalf("KeyPath = %q, want %q", p.KeyPath, ssh.KeyPathFor(home, "work"))
	}
	if !p.CreatedAt.Equal(fixedTime) {
		t.Fatalf("CreatedAt = %v, want the injected clock %v", p.CreatedAt, fixedTime)
	}

	// The public key is useless to the user unless it is shown to them.
	if !strings.Contains(out.String(), "ssh-ed25519") {
		t.Fatalf("output does not show the public key:\n%s", out.String())
	}
}

func TestAddCmd_NonInteractiveFlags(t *testing.T) {
	home := t.TempDir()
	k := &fakeKeys{}
	root, _ := newTestCmd(t, home, &fakeGit{}, k, "")
	root.SetArgs([]string{"add", "--name", "personal", "--email", "derick@gmail.com"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if len(k.calls) != 1 {
		t.Fatalf("keygen calls = %d, want 1", len(k.calls))
	}

	store, _ := config.Load(home)
	if _, ok := store.Get("personal"); !ok {
		t.Fatal("profile not persisted")
	}
}

// Windows terminals send CRLF. An untrimmed carriage return would end up
// inside the profile name and the SSH key filename.
func TestAddCmd_TrimsCarriageReturns(t *testing.T) {
	home := t.TempDir()
	k := &fakeKeys{}
	root, _ := newTestCmd(t, home, &fakeGit{}, k, "work\r\nderick@corp.com\r\n")
	root.SetArgs([]string{"add"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if k.calls[0].name != "work" {
		t.Fatalf("name = %q, want %q", k.calls[0].name, "work")
	}
	if k.calls[0].email != "derick@corp.com" {
		t.Fatalf("email = %q, want %q", k.calls[0].email, "derick@corp.com")
	}
}

func TestAddCmd_DuplicateName(t *testing.T) {
	home := t.TempDir()
	seedStore(t, home, []config.Profile{profile("work", "derick@corp.com")}, "")

	k := &fakeKeys{}
	root, _ := newTestCmd(t, home, &fakeGit{}, k, "")
	root.SetArgs([]string{"add", "--name", "work", "--email", "other@corp.com"})

	err := root.Execute()
	if !errors.Is(err, config.ErrDuplicateProfile) {
		t.Fatalf("Execute() error = %v, want ErrDuplicateProfile", err)
	}
	if len(k.calls) != 0 {
		t.Fatalf("keygen calls = %d, want 0 (must not generate a key for a rejected profile)", len(k.calls))
	}

	store, _ := config.Load(home)
	if len(store.Profiles) != 1 {
		t.Fatalf("len(Profiles) = %d, want 1", len(store.Profiles))
	}
}

func TestAddCmd_RejectsInvalidEmailBeforeKeygen(t *testing.T) {
	home := t.TempDir()
	k := &fakeKeys{}
	root, _ := newTestCmd(t, home, &fakeGit{}, k, "")
	root.SetArgs([]string{"add", "--name", "work", "--email", "not-an-email"})

	err := root.Execute()
	if !errors.Is(err, config.ErrInvalidProfile) {
		t.Fatalf("Execute() error = %v, want ErrInvalidProfile", err)
	}
	if len(k.calls) != 0 {
		t.Fatalf("keygen calls = %d, want 0", len(k.calls))
	}
}

func TestAddCmd_PropagatesKeygenFailure(t *testing.T) {
	home := t.TempDir()
	k := &fakeKeys{err: ssh.ErrKeyExists}
	root, _ := newTestCmd(t, home, &fakeGit{}, k, "")
	root.SetArgs([]string{"add", "--name", "work", "--email", "derick@corp.com"})

	if err := root.Execute(); !errors.Is(err, ssh.ErrKeyExists) {
		t.Fatalf("Execute() error = %v, want ErrKeyExists", err)
	}

	// A profile whose key was never created must not be recorded.
	store, _ := config.Load(home)
	if len(store.Profiles) != 0 {
		t.Fatalf("len(Profiles) = %d, want 0", len(store.Profiles))
	}
}

// Adding a profile registers it; applying it is what "use" is for.
func TestAddCmd_DoesNotChangeActiveProfile(t *testing.T) {
	home := t.TempDir()
	seedStore(t, home, []config.Profile{profile("work", "derick@corp.com")}, "work")

	g := &fakeGit{}
	root, _ := newTestCmd(t, home, g, &fakeKeys{}, "")
	root.SetArgs([]string{"add", "--name", "personal", "--email", "derick@gmail.com"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	store, _ := config.Load(home)
	if store.Active != "work" {
		t.Fatalf("Active = %q, want it unchanged at %q", store.Active, "work")
	}
	if len(g.applied) != 0 {
		t.Fatalf("git applies = %d, want 0", len(g.applied))
	}
}

func TestUseCmd_AppliesProfileAndPersistsActive(t *testing.T) {
	home := t.TempDir()
	seedStore(t, home, []config.Profile{
		profile("work", "derick@corp.com"),
		profile("personal", "derick@gmail.com"),
	}, "work")

	g := &fakeGit{}
	root, _ := newTestCmd(t, home, g, &fakeKeys{}, "")
	root.SetArgs([]string{"use", "personal"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	if len(g.applied) != 1 {
		t.Fatalf("git applies = %d, want 1", len(g.applied))
	}
	if g.applied[0].Name != "personal" || g.applied[0].Email != "derick@gmail.com" {
		t.Fatalf("applied = %+v, want the personal profile", g.applied[0])
	}

	store, _ := config.Load(home)
	if store.Active != "personal" {
		t.Fatalf("Active = %q, want %q", store.Active, "personal")
	}
}

func TestUseCmd_UnknownProfile(t *testing.T) {
	home := t.TempDir()
	seedStore(t, home, []config.Profile{profile("work", "derick@corp.com")}, "work")

	g := &fakeGit{}
	root, _ := newTestCmd(t, home, g, &fakeKeys{}, "")
	root.SetArgs([]string{"use", "ghost"})

	if err := root.Execute(); !errors.Is(err, config.ErrProfileNotFound) {
		t.Fatalf("Execute() error = %v, want ErrProfileNotFound", err)
	}
	if len(g.applied) != 0 {
		t.Fatalf("git applies = %d, want 0", len(g.applied))
	}

	store, _ := config.Load(home)
	if store.Active != "work" {
		t.Fatalf("Active = %q, want it unchanged", store.Active)
	}
}

// The active marker must not move if the git write failed.
func TestUseCmd_DoesNotPersistWhenApplyFails(t *testing.T) {
	home := t.TempDir()
	seedStore(t, home, []config.Profile{
		profile("work", "derick@corp.com"),
		profile("personal", "derick@gmail.com"),
	}, "work")

	g := &fakeGit{err: errors.New("git exploded")}
	root, _ := newTestCmd(t, home, g, &fakeKeys{}, "")
	root.SetArgs([]string{"use", "personal"})

	if err := root.Execute(); err == nil {
		t.Fatal("Execute() = nil, want error")
	}

	store, _ := config.Load(home)
	if store.Active != "work" {
		t.Fatalf("Active = %q, want it unchanged at %q", store.Active, "work")
	}
}

func TestUseCmd_RequiresProfileName(t *testing.T) {
	root, _ := newTestCmd(t, t.TempDir(), &fakeGit{}, &fakeKeys{}, "")
	root.SetArgs([]string{"use"})

	if err := root.Execute(); err == nil {
		t.Fatal("Execute() = nil, want an argument error")
	}
}

func TestListCmd_MarksActive(t *testing.T) {
	home := t.TempDir()
	seedStore(t, home, []config.Profile{
		profile("work", "derick@corp.com"),
		profile("personal", "derick@gmail.com"),
	}, "personal")

	root, out := newTestCmd(t, home, &fakeGit{}, &fakeKeys{}, "")
	root.SetArgs([]string{"list"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	got := out.String()
	for _, want := range []string{"work", "personal", "derick@corp.com", "derick@gmail.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}

	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "personal") && !strings.Contains(line, "*") {
			t.Errorf("active profile line is not marked:\n%s", line)
		}
		if strings.Contains(line, "work") && strings.Contains(line, "*") {
			t.Errorf("inactive profile line is marked:\n%s", line)
		}
	}
}

func TestListCmd_EmptyStore(t *testing.T) {
	root, out := newTestCmd(t, t.TempDir(), &fakeGit{}, &fakeKeys{}, "")
	root.SetArgs([]string{"list"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil (an empty store is not an error)", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("output is empty, want a hint telling the user how to add a profile")
	}
	if !strings.Contains(out.String(), "add") {
		t.Fatalf("output does not point at the add command:\n%s", out.String())
	}
}

func TestCleanCmd_NoArgUsesCurrentDirectory(t *testing.T) {
	g := &fakeGit{}
	root, _ := newTestCmd(t, t.TempDir(), g, &fakeKeys{}, "")
	root.SetArgs([]string{"clean"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if len(g.cleaned) != 1 {
		t.Fatalf("clean calls = %d, want 1", len(g.cleaned))
	}
	if g.cleaned[0] != "." {
		t.Fatalf("path = %q, want %q", g.cleaned[0], ".")
	}
}

func TestCleanCmd_WithPathArg(t *testing.T) {
	g := &fakeGit{}
	root, _ := newTestCmd(t, t.TempDir(), g, &fakeKeys{}, "")
	root.SetArgs([]string{"clean", "/srv/work/api"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	if g.cleaned[0] != "/srv/work/api" {
		t.Fatalf("path = %q, want %q", g.cleaned[0], "/srv/work/api")
	}
}

func TestCleanCmd_PropagatesError(t *testing.T) {
	g := &fakeGit{err: errors.New("not a git repository")}
	root, _ := newTestCmd(t, t.TempDir(), g, &fakeKeys{}, "")
	root.SetArgs([]string{"clean", "/not/a/repo"})

	if err := root.Execute(); err == nil {
		t.Fatal("Execute() = nil, want error")
	}
}
