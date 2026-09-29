package rewrite_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Dkavila/git-persona/internal/rewrite"
)

// metaFormat is the exact --format string the implementation must use. NUL
// separates the fields because it is the one byte a commit field cannot hold,
// so a message containing newlines or percent signs cannot corrupt parsing.
//
// The trailing %x00 closes the message field. Without it the newline that
// --format always appends would be read as part of the message, and every
// rewrite would grow it by one blank line.
const metaFormat = "%T%x00%P%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%B%x00"

type fixture struct {
	hash, tree string
	parents    []string
	an, ae, ad string
	cn, ce, cd string
	message    string
}

// fakeGit is an in-memory commit graph that answers the plumbing commands the
// package issues. No test here touches a real repository.
type fakeGit struct {
	branch   string
	dirty    bool
	detached bool
	commits  []fixture // oldest first

	calls  [][]string
	envs   []map[string]string
	stdins []string

	written map[string]fixture // hash -> what commit-tree was asked to build
	refs    map[string]string
	counter int
	failOn  string // argv[0] that should return an error
}

func newFakeGit(branch string, commits ...fixture) *fakeGit {
	return &fakeGit{
		branch:  branch,
		commits: commits,
		written: map[string]fixture{},
		refs:    map[string]string{},
	}
}

func (f *fakeGit) find(hash string) (fixture, bool) {
	for _, c := range f.commits {
		if c.hash == hash {
			return c, true
		}
	}
	return fixture{}, false
}

func (f *fakeGit) Run(env map[string]string, stdin string, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	f.envs = append(f.envs, env)
	f.stdins = append(f.stdins, stdin)

	if f.failOn != "" && len(args) > 0 && args[0] == f.failOn {
		return "", errors.New("git exploded")
	}

	switch args[0] {
	case "status":
		if f.dirty {
			return " M internal/cli/add.go\n", nil
		}
		return "", nil

	case "rev-parse":
		if f.detached {
			return "HEAD\n", nil
		}
		return f.branch + "\n", nil

	case "rev-list":
		var out []string
		for _, c := range f.commits {
			out = append(out, c.hash)
		}
		return strings.Join(out, "\n") + "\n", nil

	case "show":
		c, ok := f.find(args[len(args)-1])
		if !ok {
			return "", fmt.Errorf("unknown commit %s", args[len(args)-1])
		}
		fields := []string{
			c.tree, strings.Join(c.parents, " "),
			c.an, c.ae, c.ad,
			c.cn, c.ce, c.cd,
			c.message,
		}
		// git's --format terminates its output with a newline, after the
		// final NUL. Reproducing that is what makes the message parsing
		// testable at all.
		return strings.Join(fields, "\x00") + "\x00\n", nil

	case "commit-tree":
		f.counter++
		newHash := fmt.Sprintf("new%03d", f.counter)

		built := fixture{hash: newHash, tree: args[1], message: stdin}
		for i := 2; i < len(args); i++ {
			if args[i] == "-p" && i+1 < len(args) {
				built.parents = append(built.parents, args[i+1])
			}
		}
		built.an, built.ae, built.ad = env["GIT_AUTHOR_NAME"], env["GIT_AUTHOR_EMAIL"], env["GIT_AUTHOR_DATE"]
		built.cn, built.ce, built.cd = env["GIT_COMMITTER_NAME"], env["GIT_COMMITTER_EMAIL"], env["GIT_COMMITTER_DATE"]
		f.written[newHash] = built

		return newHash + "\n", nil

	case "update-ref":
		f.refs[args[1]] = args[2]
		return "", nil
	}
	return "", nil
}

func (f *fakeGit) argvFor(cmd string) [][]string {
	var out [][]string
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == cmd {
			out = append(out, c)
		}
	}
	return out
}

var target = rewrite.Identity{Name: "Jane Doe", Email: "me@example.com"}

// A three-commit line: the middle one carries the address to replace.
func threeCommits() *fakeGit {
	return newFakeGit("main",
		fixture{
			hash: "aaa111", tree: "tree1", parents: nil,
			an: "Jane Doe", ae: "me@example.com", ad: "2026-09-20T10:00:00-03:00",
			cn: "Jane Doe", ce: "me@example.com", cd: "2026-09-20T10:00:00-03:00",
			message: "chore: scaffold\n",
		},
		fixture{
			hash: "bbb222", tree: "tree2", parents: []string{"aaa111"},
			an: "work", ae: "dev@acme-corp.com", ad: "2026-09-21T11:30:00-03:00",
			cn: "work", ce: "dev@acme-corp.com", cd: "2026-09-21T11:35:00-03:00",
			message: "feat: add remove command\n\nWith a body.\n",
		},
		fixture{
			hash: "ccc333", tree: "tree3", parents: []string{"bbb222"},
			an: "personal", ae: "me@example.com", ad: "2026-09-22T09:00:00-03:00",
			cn: "personal", ce: "me@example.com", cd: "2026-09-22T09:00:00-03:00",
			message: "docs: readme\n",
		},
	)
}

// --- plan --------------------------------------------------------------------

func TestBuildPlan_SelectsByAuthorEmail(t *testing.T) {
	g := threeCommits()

	plan, err := rewrite.BuildPlan(g, rewrite.Selector{FromEmail: "dev@acme-corp.com"}, target)
	if err != nil {
		t.Fatalf("BuildPlan() = %v, want nil", err)
	}

	if len(plan.Changes) != 1 {
		t.Fatalf("selected %d commits, want 1", len(plan.Changes))
	}
	if plan.Changes[0].OldHash != "bbb222" {
		t.Fatalf("selected %q, want %q", plan.Changes[0].OldHash, "bbb222")
	}
	if plan.Total != 3 {
		t.Fatalf("Total = %d, want 3 commits examined", plan.Total)
	}
	if plan.Branch != "main" {
		t.Fatalf("Branch = %q, want %q", plan.Branch, "main")
	}
}

func TestBuildPlan_EmailMatchIgnoresCase(t *testing.T) {
	g := threeCommits()

	plan, err := rewrite.BuildPlan(g, rewrite.Selector{FromEmail: "DEV@ACME-CORP.COM"}, target)
	if err != nil {
		t.Fatalf("BuildPlan() = %v, want nil", err)
	}
	if len(plan.Changes) != 1 {
		t.Fatalf("selected %d commits, want 1", len(plan.Changes))
	}
}

func TestBuildPlan_SelectsExplicitCommits(t *testing.T) {
	g := threeCommits()

	plan, err := rewrite.BuildPlan(g, rewrite.Selector{Commits: []string{"bbb222", "ccc333"}}, target)
	if err != nil {
		t.Fatalf("BuildPlan() = %v, want nil", err)
	}

	got := []string{}
	for _, c := range plan.Changes {
		got = append(got, c.OldHash)
	}
	if len(got) != 2 || got[0] != "bbb222" || got[1] != "ccc333" {
		t.Fatalf("selected %v, want [bbb222 ccc333]", got)
	}
}

// Naming a commit that already carries the target identity is a no-op, not an
// error: the user asked for an outcome that is already true.
func TestBuildPlan_ExplicitCommitAlreadyOwnedIsSkipped(t *testing.T) {
	g := threeCommits()

	plan, err := rewrite.BuildPlan(g, rewrite.Selector{Commits: []string{"aaa111"}}, target)
	if err != nil {
		t.Fatalf("BuildPlan() = %v, want nil", err)
	}
	if len(plan.Changes) != 0 {
		t.Fatalf("selected %d commits, want 0", len(plan.Changes))
	}
}

// No filter at all means the whole range: the "rewrite this branch" mode.
// aaa111 is excluded because it already carries the target identity, so the
// plan lists the two commits that genuinely change out of the three examined.
func TestBuildPlan_NoFilterSelectsTheWholeRange(t *testing.T) {
	g := threeCommits()

	plan, err := rewrite.BuildPlan(g, rewrite.Selector{}, target)
	if err != nil {
		t.Fatalf("BuildPlan() = %v, want nil", err)
	}
	if plan.Total != 3 {
		t.Fatalf("Total = %d, want all 3 examined", plan.Total)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("selected %d commits, want the 2 that actually change", len(plan.Changes))
	}
	for _, c := range plan.Changes {
		if c.OldHash == "aaa111" {
			t.Fatal("aaa111 already carries the target identity and must not be listed")
		}
	}
}

// A commit already owned by the target identity would be rebuilt byte for byte,
// so listing it as a change would be a lie.
func TestBuildPlan_SkipsCommitsAlreadyOwnedByTheTarget(t *testing.T) {
	g := threeCommits()

	plan, err := rewrite.BuildPlan(g, rewrite.Selector{}, rewrite.Identity{
		Name: "Jane Doe", Email: "me@example.com",
	})
	if err != nil {
		t.Fatalf("BuildPlan() = %v, want nil", err)
	}

	for _, c := range plan.Changes {
		if c.OldHash == "aaa111" {
			t.Fatal("aaa111 already carries the target identity and must not be listed as a change")
		}
	}
}

func TestBuildPlan_RecordsTheOldIdentityForReporting(t *testing.T) {
	g := threeCommits()

	plan, err := rewrite.BuildPlan(g, rewrite.Selector{FromEmail: "dev@acme-corp.com"}, target)
	if err != nil {
		t.Fatalf("BuildPlan() = %v, want nil", err)
	}

	c := plan.Changes[0]
	if c.OldAuthor.Name != "work" || c.OldAuthor.Email != "dev@acme-corp.com" {
		t.Fatalf("OldAuthor = %+v, want work <dev@acme-corp.com>", c.OldAuthor)
	}
	if !strings.HasPrefix(c.Subject, "feat: add remove command") {
		t.Fatalf("Subject = %q, want the first line of the message", c.Subject)
	}
}

func TestBuildPlan_UsesTheNulSeparatedFormat(t *testing.T) {
	g := threeCommits()

	if _, err := rewrite.BuildPlan(g, rewrite.Selector{}, target); err != nil {
		t.Fatalf("BuildPlan() = %v, want nil", err)
	}

	shows := g.argvFor("show")
	if len(shows) == 0 {
		t.Fatal("no metadata was read")
	}
	if !contains(shows[0], "--format="+metaFormat) {
		t.Fatalf("argv = %q, want it to carry --format=%s", shows[0], metaFormat)
	}
}

func TestBuildPlan_RefusesADirtyWorkingTree(t *testing.T) {
	g := threeCommits()
	g.dirty = true

	_, err := rewrite.BuildPlan(g, rewrite.Selector{}, target)
	if !errors.Is(err, rewrite.ErrDirtyWorkingTree) {
		t.Fatalf("BuildPlan() error = %v, want ErrDirtyWorkingTree", err)
	}
}

func TestBuildPlan_RefusesDetachedHead(t *testing.T) {
	g := threeCommits()
	g.detached = true

	_, err := rewrite.BuildPlan(g, rewrite.Selector{}, target)
	if !errors.Is(err, rewrite.ErrDetachedHead) {
		t.Fatalf("BuildPlan() error = %v, want ErrDetachedHead", err)
	}
}

// --- apply -------------------------------------------------------------------

func TestApply_RewritesTheSelectedAuthor(t *testing.T) {
	g := threeCommits()
	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{FromEmail: "dev@acme-corp.com"}, target)

	if _, err := rewrite.Apply(g, plan, target, rewrite.Options{}); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	var rewritten fixture
	for _, c := range g.written {
		if strings.HasPrefix(c.message, "feat: add remove command") {
			rewritten = c
		}
	}
	if rewritten.an != "Jane Doe" || rewritten.ae != "me@example.com" {
		t.Fatalf("author = %s <%s>, want Jane Doe <me@example.com>", rewritten.an, rewritten.ae)
	}
	if rewritten.cn != "Jane Doe" || rewritten.ce != "me@example.com" {
		t.Fatalf("committer = %s <%s>, want it rewritten too by default", rewritten.cn, rewritten.ce)
	}
}

// Rewriting authorship must not move anything on the timeline.
func TestApply_PreservesBothDates(t *testing.T) {
	g := threeCommits()
	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{FromEmail: "dev@acme-corp.com"}, target)

	if _, err := rewrite.Apply(g, plan, target, rewrite.Options{}); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	for _, c := range g.written {
		if strings.HasPrefix(c.message, "feat: add remove command") {
			if c.ad != "2026-09-21T11:30:00-03:00" {
				t.Errorf("author date = %q, want the original", c.ad)
			}
			if c.cd != "2026-09-21T11:35:00-03:00" {
				t.Errorf("committer date = %q, want the original", c.cd)
			}
		}
	}
}

func TestApply_AuthorOnlyLeavesTheCommitterAlone(t *testing.T) {
	g := threeCommits()
	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{FromEmail: "dev@acme-corp.com"}, target)

	if _, err := rewrite.Apply(g, plan, target, rewrite.Options{AuthorOnly: true}); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	for _, c := range g.written {
		if strings.HasPrefix(c.message, "feat: add remove command") {
			if c.ae != "me@example.com" {
				t.Errorf("author was not rewritten: %q", c.ae)
			}
			if c.ce != "dev@acme-corp.com" {
				t.Errorf("committer = %q, want it untouched under AuthorOnly", c.ce)
			}
		}
	}
}

// Rewriting an ancestor changes every descendant's hash, so the children must
// be rebuilt against the new parent rather than the old one.
func TestApply_RemapsParents(t *testing.T) {
	g := threeCommits()
	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{FromEmail: "dev@acme-corp.com"}, target)

	if _, err := rewrite.Apply(g, plan, target, rewrite.Options{}); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	if len(g.written) != 3 {
		t.Fatalf("built %d commits, want all 3 rebuilt: the middle one changed, so its child must follow", len(g.written))
	}

	for _, c := range g.written {
		for _, p := range c.parents {
			if strings.HasPrefix(p, "aaa") || strings.HasPrefix(p, "bbb") || strings.HasPrefix(p, "ccc") {
				t.Errorf("commit %s still points at the original parent %s", c.hash, p)
			}
		}
	}
}

func TestApply_RemapsEveryParentOfAMerge(t *testing.T) {
	g := newFakeGit("main",
		fixture{hash: "aaa111", tree: "t1", an: "work", ae: "dev@acme-corp.com",
			ad: "2026-09-20T10:00:00-03:00", cn: "work", ce: "dev@acme-corp.com",
			cd: "2026-09-20T10:00:00-03:00", message: "base\n"},
		fixture{hash: "bbb222", tree: "t2", parents: []string{"aaa111"}, an: "work", ae: "dev@acme-corp.com",
			ad: "2026-09-20T11:00:00-03:00", cn: "work", ce: "dev@acme-corp.com",
			cd: "2026-09-20T11:00:00-03:00", message: "side\n"},
		fixture{hash: "ccc333", tree: "t3", parents: []string{"aaa111", "bbb222"}, an: "work", ae: "dev@acme-corp.com",
			ad: "2026-09-20T12:00:00-03:00", cn: "work", ce: "dev@acme-corp.com",
			cd: "2026-09-20T12:00:00-03:00", message: "merge\n"},
	)

	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{}, target)
	if _, err := rewrite.Apply(g, plan, target, rewrite.Options{}); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	for _, c := range g.written {
		if c.message == "merge\n" {
			if len(c.parents) != 2 {
				t.Fatalf("merge rebuilt with %d parents, want 2", len(c.parents))
			}
		}
	}
}

func TestApply_CreatesABackupRefBeforeMovingTheBranch(t *testing.T) {
	g := threeCommits()
	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{}, target)

	backup, err := rewrite.Apply(g, plan, target, rewrite.Options{})
	if err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}
	if !strings.HasPrefix(backup, "refs/git-persona/backup/") {
		t.Fatalf("backup ref = %q, want it under refs/git-persona/backup/", backup)
	}
	if g.refs[backup] != "ccc333" {
		t.Fatalf("backup points at %q, want the original head ccc333", g.refs[backup])
	}
}

func TestApply_UpdatesTheBranchToTheNewHead(t *testing.T) {
	g := threeCommits()
	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{}, target)

	if _, err := rewrite.Apply(g, plan, target, rewrite.Options{}); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	got := g.refs["refs/heads/main"]
	if got == "" || got == "ccc333" {
		t.Fatalf("refs/heads/main = %q, want the rewritten head", got)
	}
	if _, ok := g.written[got]; !ok {
		t.Fatalf("refs/heads/main = %q, which is not one of the commits just built", got)
	}
}

// The compare-and-swap form of update-ref refuses if the branch moved while
// the rewrite was running.
func TestApply_UpdatesTheBranchWithTheExpectedOldValue(t *testing.T) {
	g := threeCommits()
	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{}, target)

	if _, err := rewrite.Apply(g, plan, target, rewrite.Options{}); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	var branchUpdate []string
	for _, c := range g.argvFor("update-ref") {
		if len(c) > 1 && c[1] == "refs/heads/main" {
			branchUpdate = c
		}
	}
	if branchUpdate == nil {
		t.Fatal("the branch ref was never updated")
	}
	if len(branchUpdate) != 4 || branchUpdate[3] != "ccc333" {
		t.Fatalf("argv = %q, want update-ref <ref> <new> <old-value>", branchUpdate)
	}
}

func TestApply_EmptyPlanTouchesNothing(t *testing.T) {
	g := threeCommits()
	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{FromEmail: "nobody@example.com"}, target)

	backup, err := rewrite.Apply(g, plan, target, rewrite.Options{})
	if err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}
	if backup != "" {
		t.Fatalf("backup ref = %q, want none for an empty plan", backup)
	}
	if len(g.written) != 0 {
		t.Fatalf("built %d commits, want 0", len(g.written))
	}
	if len(g.refs) != 0 {
		t.Fatalf("touched %d refs, want 0", len(g.refs))
	}
}

func TestApply_PropagatesAFailureFromCommitTree(t *testing.T) {
	g := threeCommits()
	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{}, target)
	g.failOn = "commit-tree"

	if _, err := rewrite.Apply(g, plan, target, rewrite.Options{}); err == nil {
		t.Fatal("Apply() = nil, want the underlying failure")
	}
	if _, ok := g.refs["refs/heads/main"]; ok {
		t.Fatal("the branch was moved even though the rewrite failed")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// The message must survive a rewrite byte for byte. It is the field most
// easily corrupted, because git's --format appends a newline of its own.
func TestApply_PreservesTheMessageExactly(t *testing.T) {
	g := threeCommits()
	plan, _ := rewrite.BuildPlan(g, rewrite.Selector{FromEmail: "dev@acme-corp.com"}, target)

	if _, err := rewrite.Apply(g, plan, target, rewrite.Options{}); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	const want = "feat: add remove command\n\nWith a body.\n"
	for _, c := range g.written {
		if strings.HasPrefix(c.message, "feat: add remove command") {
			if c.message != want {
				t.Fatalf("message = %q, want %q", c.message, want)
			}
			return
		}
	}
	t.Fatal("the rewritten commit was not found")
}
