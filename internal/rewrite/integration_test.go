//go:build integration

// Integration coverage for the rewrite package, run against a real git binary
// in a throwaway repository:
//
//	go test -tags=integration -v ./internal/rewrite/
//
// It is behind a build tag because it spawns processes and is therefore slower
// and more environment-dependent than the rest of the suite. The unit tests
// prove the algorithm; this proves the plumbing commands are the ones git
// actually accepts.
package rewrite_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dkavila/git-pilot/internal/rewrite"
)

// newRepo builds a temporary repository whose commits carry three different
// identities, mirroring the situation this feature exists to repair.
func newRepo(t *testing.T) (dir string, run func(env map[string]string, args ...string) string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}

	dir = t.TempDir()
	r := rewrite.ExecRunner{Dir: dir}

	run = func(env map[string]string, args ...string) string {
		t.Helper()
		out, err := r.Run(env, "", args...)
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(out)
	}

	run(nil, "init", "-q", "-b", "main")
	// Keep the developer's real configuration out of the test.
	run(nil, "config", "user.name", "seed")
	run(nil, "config", "user.email", "seed@example.com")
	run(nil, "config", "commit.gpgsign", "false")

	commit := func(file, content, name, email, date string) {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
		run(nil, "add", file)
		run(map[string]string{
			"GIT_AUTHOR_NAME":     name,
			"GIT_AUTHOR_EMAIL":    email,
			"GIT_AUTHOR_DATE":     date,
			"GIT_COMMITTER_NAME":  name,
			"GIT_COMMITTER_EMAIL": email,
			"GIT_COMMITTER_DATE":  date,
		}, "commit", "-q", "-m", "add "+file)
	}

	commit("a.txt", "a\n", "Jane Doe", "me@example.com", "2026-09-20T10:00:00-03:00")
	commit("b.txt", "b\n", "work", "dev@acme-corp.com", "2026-09-21T11:30:00-03:00")
	commit("c.txt", "c\n", "personal", "me@example.com", "2026-09-22T09:00:00-03:00")

	return dir, run
}

func TestIntegration_RewritesByAuthorEmail(t *testing.T) {
	dir, run := newRepo(t)
	r := rewrite.ExecRunner{Dir: dir}
	target := rewrite.Identity{Name: "Jane Doe", Email: "me@example.com"}

	before := run(nil, "log", "--format=%H %ae %ad", "--date=iso-strict")
	t.Logf("before:\n%s", before)

	plan, err := rewrite.BuildPlan(r, rewrite.Selector{FromEmail: "dev@acme-corp.com"}, target)
	if err != nil {
		t.Fatalf("BuildPlan() = %v", err)
	}
	if plan.Total != 3 {
		t.Fatalf("Total = %d, want 3", plan.Total)
	}
	if len(plan.Changes) != 1 {
		t.Fatalf("Changes = %d, want 1", len(plan.Changes))
	}

	backup, err := rewrite.Apply(r, plan, target, rewrite.Options{})
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	after := run(nil, "log", "--format=%H %ae %ad", "--date=iso-strict")
	t.Logf("after:\n%s", after)

	// No trace of the old address is left anywhere in the history.
	if strings.Contains(run(nil, "log", "--format=%ae %ce"), "acme-corp") {
		t.Fatal("the old email survived the rewrite")
	}

	// Precision matters as much as the rewrite itself: --from targets one
	// address, so the commit authored by "personal" must be left alone even
	// though it shares the target's email.
	authors := strings.Split(run(nil, "log", "--reverse", "--format=%an <%ae>"), "\n")
	want := []string{
		"Jane Doe <me@example.com>", // untouched, already the target
		"Jane Doe <me@example.com>", // rewritten from work <...acme-corp...>
		"personal <me@example.com>", // untouched: a different author name
	}
	if len(authors) != len(want) {
		t.Fatalf("got %d commits, want %d", len(authors), len(want))
	}
	for i := range want {
		if authors[i] != want[i] {
			t.Errorf("commit %d author = %q, want %q", i, authors[i], want[i])
		}
	}

	// The tree contents are untouched: rewriting authorship must not change
	// a single byte of the working tree.
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s vanished: %v", f, err)
		}
	}
	if status := run(nil, "status", "--porcelain"); status != "" {
		t.Errorf("working tree is dirty after the rewrite:\n%s", status)
	}

	// The backup ref still points at the original history.
	if run(nil, "rev-parse", backup) == run(nil, "rev-parse", "HEAD") {
		t.Error("the backup ref points at the rewritten head")
	}
	oldLog := run(nil, "log", "--format=%ae", backup)
	if !strings.Contains(oldLog, "acme-corp") {
		t.Error("the backup does not hold the original identities")
	}
}

// Dates are the invariant most easily broken by a rewrite, so they get their
// own check against real git output.
func TestIntegration_PreservesDates(t *testing.T) {
	dir, run := newRepo(t)
	r := rewrite.ExecRunner{Dir: dir}
	target := rewrite.Identity{Name: "Jane Doe", Email: "me@example.com"}

	before := run(nil, "log", "--reverse", "--format=%ad|%cd", "--date=iso-strict")

	plan, err := rewrite.BuildPlan(r, rewrite.Selector{}, target)
	if err != nil {
		t.Fatalf("BuildPlan() = %v", err)
	}
	if _, err := rewrite.Apply(r, plan, target, rewrite.Options{}); err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	after := run(nil, "log", "--reverse", "--format=%ad|%cd", "--date=iso-strict")
	if before != after {
		t.Fatalf("dates changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestIntegration_RefusesADirtyWorkingTree(t *testing.T) {
	dir, _ := newRepo(t)
	r := rewrite.ExecRunner{Dir: dir}

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := rewrite.BuildPlan(r, rewrite.Selector{}, rewrite.Identity{Name: "X", Email: "x@example.com"})
	if err == nil {
		t.Fatal("BuildPlan() = nil, want a refusal")
	}
}

// The message is the field git's --format most easily corrupts, so it is
// checked against real git output, byte for byte, across two rewrites.
func TestIntegration_PreservesMessagesAcrossRepeatedRewrites(t *testing.T) {
	dir, run := newRepo(t)
	r := rewrite.ExecRunner{Dir: dir}

	// A message with a body and a blank line: the shape that shows newline
	// drift immediately.
	run(nil, "commit", "-q", "--allow-empty", "-m", "feat: something", "-m", "With a body paragraph.")

	before := run(nil, "log", "--format=%B%x00")

	for i, id := range []rewrite.Identity{
		{Name: "First Pass", Email: "first@example.com"},
		{Name: "Second Pass", Email: "second@example.com"},
	} {
		plan, err := rewrite.BuildPlan(r, rewrite.Selector{}, id)
		if err != nil {
			t.Fatalf("pass %d BuildPlan() = %v", i, err)
		}
		if _, err := rewrite.Apply(r, plan, id, rewrite.Options{}); err != nil {
			t.Fatalf("pass %d Apply() = %v", i, err)
		}
	}

	after := run(nil, "log", "--format=%B%x00")
	if before != after {
		t.Fatalf("messages drifted after two rewrites.\nbefore: %q\nafter:  %q", before, after)
	}
}
