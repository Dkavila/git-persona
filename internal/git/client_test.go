package git_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Dkavila/git-persona/internal/config"
	"github.com/Dkavila/git-persona/internal/git"
)

// fakeRunner records every argv it is handed and replays scripted results, so
// no test in this package ever executes the real git binary.
type fakeRunner struct {
	calls   [][]string
	outputs []string
	errs    []error
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	i := len(f.calls)
	f.calls = append(f.calls, args)

	var out string
	if i < len(f.outputs) {
		out = f.outputs[i]
	}
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return out, err
}

func testProfile() config.Profile {
	return config.Profile{
		Name:    "work",
		Email:   "git-persona@corp.com",
		KeyPath: "/home/git-persona/.ssh/id_ed25519_work",
	}
}

func TestApplyProfile_SetsThreeGlobalKeys(t *testing.T) {
	r := &fakeRunner{}
	c := git.New(r)

	if err := c.ApplyProfile(testProfile()); err != nil {
		t.Fatalf("ApplyProfile() = %v, want nil", err)
	}

	want := [][]string{
		{"config", "--global", "user.name", "work"},
		{"config", "--global", "user.email", "git-persona@corp.com"},
		{"config", "--global", "core.sshCommand", `ssh -i "/home/git-persona/.ssh/id_ed25519_work" -o IdentitiesOnly=yes`},
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("argv sequence =\n%q\nwant\n%q", r.calls, want)
	}
}

func TestApplyProfile_StopsOnFirstError(t *testing.T) {
	boom := errors.New("git exploded")
	r := &fakeRunner{errs: []error{boom}}
	c := git.New(r)

	err := c.ApplyProfile(testProfile())
	if err == nil {
		t.Fatal("ApplyProfile() = nil, want error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("ApplyProfile() error = %v, want it to wrap the runner error", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1 (must not keep writing after a failure)", len(r.calls))
	}
}

// A half-applied identity is worse than none, so validation happens before the
// first write rather than failing midway through.
func TestApplyProfile_RejectsInvalidProfileBeforeRunning(t *testing.T) {
	r := &fakeRunner{}
	c := git.New(r)

	bad := testProfile()
	bad.Email = "not-an-email"

	err := c.ApplyProfile(bad)
	if !errors.Is(err, config.ErrInvalidProfile) {
		t.Fatalf("ApplyProfile() error = %v, want ErrInvalidProfile", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("len(calls) = %d, want 0", len(r.calls))
	}
}

func TestSSHCommand_Format(t *testing.T) {
	got := git.SSHCommand("/home/git-persona/.ssh/id_ed25519_work")
	want := `ssh -i "/home/git-persona/.ssh/id_ed25519_work" -o IdentitiesOnly=yes`

	if got != want {
		t.Fatalf("SSHCommand() = %q, want %q", got, want)
	}
}

// Git parses core.sshCommand with shell-like quoting rules, where a backslash
// is an escape character. A raw Windows path would be silently mangled, so the
// separators are normalised to forward slashes, which Git and OpenSSH both
// accept on Windows.
func TestSSHCommand_NormalisesWindowsPath(t *testing.T) {
	got := git.SSHCommand(`C:\Users\git-persona\.ssh\id_ed25519_work`)
	want := `ssh -i "C:/Users/git-persona/.ssh/id_ed25519_work" -o IdentitiesOnly=yes`

	if got != want {
		t.Fatalf("SSHCommand() = %q, want %q", got, want)
	}
}

// The quotes also cover the common Windows case of a home directory whose name
// contains a space.
func TestSSHCommand_QuotesPathWithSpaces(t *testing.T) {
	got := git.SSHCommand(`C:\Users\git-persona Avila\.ssh\id_ed25519_work`)
	want := `ssh -i "C:/Users/git-persona Avila/.ssh/id_ed25519_work" -o IdentitiesOnly=yes`

	if got != want {
		t.Fatalf("SSHCommand() = %q, want %q", got, want)
	}
}

func TestCurrentGlobalIdentity(t *testing.T) {
	r := &fakeRunner{outputs: []string{"work\n", "git-persona@corp.com\n"}}
	c := git.New(r)

	name, email, err := c.CurrentGlobalIdentity()
	if err != nil {
		t.Fatalf("CurrentGlobalIdentity() = %v, want nil", err)
	}
	if name != "work" {
		t.Fatalf("name = %q, wanted the trailing newline trimmed to %q", name, "work")
	}
	if email != "git-persona@corp.com" {
		t.Fatalf("email = %q, want %q", email, "git-persona@corp.com")
	}

	want := [][]string{
		{"config", "--global", "--get", "user.name"},
		{"config", "--global", "--get", "user.email"},
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("argv sequence =\n%q\nwant\n%q", r.calls, want)
	}
}

// git config --get exits 1 when the key is simply not set. That is a normal
// state for a fresh machine, not a failure.
func TestCurrentGlobalIdentity_UnsetKeysAreNotAnError(t *testing.T) {
	r := &fakeRunner{errs: []error{&git.ExitError{Code: 1}, &git.ExitError{Code: 1}}}
	c := git.New(r)

	name, email, err := c.CurrentGlobalIdentity()
	if err != nil {
		t.Fatalf("CurrentGlobalIdentity() = %v, want nil for unset keys", err)
	}
	if name != "" || email != "" {
		t.Fatalf("got (%q, %q), want both empty", name, email)
	}
}

func TestCurrentGlobalIdentity_RealErrorPropagates(t *testing.T) {
	r := &fakeRunner{errs: []error{&git.ExitError{Code: 128, Stderr: "fatal: not in a git directory"}}}
	c := git.New(r)

	if _, _, err := c.CurrentGlobalIdentity(); err == nil {
		t.Fatal("CurrentGlobalIdentity() = nil, want error for exit code 128")
	}
}

func TestCleanLocal_DefaultPath(t *testing.T) {
	r := &fakeRunner{}
	c := git.New(r)

	if err := c.CleanLocal(""); err != nil {
		t.Fatalf("CleanLocal() = %v, want nil", err)
	}
	if len(r.calls) == 0 {
		t.Fatal("no git invocation recorded")
	}
	if got := r.calls[0][1]; got != "." {
		t.Fatalf("path argument = %q, want %q when no path is given", got, ".")
	}
}

func TestCleanLocal_ExplicitPath(t *testing.T) {
	r := &fakeRunner{}
	c := git.New(r)

	if err := c.CleanLocal("/srv/work/api"); err != nil {
		t.Fatalf("CleanLocal() = %v, want nil", err)
	}

	want := [][]string{
		{"-C", "/srv/work/api", "config", "--unset", "user.name"},
		{"-C", "/srv/work/api", "config", "--unset", "user.email"},
		{"-C", "/srv/work/api", "config", "--unset", "core.sshCommand"},
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("argv sequence =\n%q\nwant\n%q", r.calls, want)
	}
}

// git config --unset exits 5 when the key does not exist. Cleaning a repo that
// was already clean must succeed, and must not stop the remaining keys from
// being attempted.
func TestCleanLocal_IgnoresMissingKeyExit5(t *testing.T) {
	r := &fakeRunner{errs: []error{
		&git.ExitError{Code: 5},
		nil,
		&git.ExitError{Code: 5},
	}}
	c := git.New(r)

	if err := c.CleanLocal("/srv/work/api"); err != nil {
		t.Fatalf("CleanLocal() = %v, want nil when keys are absent", err)
	}
	if len(r.calls) != 3 {
		t.Fatalf("len(calls) = %d, want 3 (a missing key must not abort the sweep)", len(r.calls))
	}
}

func TestCleanLocal_RealErrorPropagates(t *testing.T) {
	r := &fakeRunner{errs: []error{
		&git.ExitError{Code: 128, Stderr: "fatal: not a git repository"},
	}}
	c := git.New(r)

	err := c.CleanLocal("/not/a/repo")
	if err == nil {
		t.Fatal("CleanLocal() = nil, want error for exit code 128")
	}

	var exitErr *git.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("CleanLocal() error = %v, want it to wrap *git.ExitError", err)
	}
	if exitErr.Code != 128 {
		t.Fatalf("exit code = %d, want 128", exitErr.Code)
	}
	if len(r.calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1 (a real failure must abort the sweep)", len(r.calls))
	}
}
