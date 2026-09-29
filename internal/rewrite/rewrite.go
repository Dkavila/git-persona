// Package rewrite changes the authorship of existing commits, replacing an
// identity that leaked into the history with a registered persona.
//
// It works through Git's plumbing rather than filter-branch or filter-repo:
// commits are read with rev-list and show, rebuilt with commit-tree, and the
// branch is moved with a compare-and-swap update-ref. That keeps git-persona a
// self-contained binary and keeps every step behind the Runner seam, so the
// suite runs without a real repository.
//
// Rewriting is irreversible from Git's point of view: every rebuilt commit
// gets a new hash, and so does every descendant. Apply therefore records the
// original head under refs/git-persona/backup/ before moving anything.
package rewrite

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// metaFormat reads everything needed to rebuild a commit in one call.
//
// NUL separates the fields because it is the one byte a commit field cannot
// contain, so a message with newlines or percent signs cannot corrupt the
// parse.
//
// The trailing %x00 after %B is not decoration. git's --format always ends its
// output with a newline, so an unterminated last field would absorb it and
// every rewrite would add one more blank line to the message. Closing the
// field with a separator puts that newline in a tenth field nobody reads.
const metaFormat = "%T%x00%P%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%B%x00"

const backupPrefix = "refs/git-persona/backup/"

var (
	// ErrDirtyWorkingTree reports uncommitted changes. Rewriting with a dirty
	// tree risks losing work that no commit is holding.
	ErrDirtyWorkingTree = errors.New("working tree has uncommitted changes")

	// ErrDetachedHead reports that HEAD points at no branch, so there is
	// nothing to move once the commits are rebuilt.
	ErrDetachedHead = errors.New("HEAD is detached")
)

// Runner executes git. It carries an environment and stdin because rebuilding
// a commit needs both: the identity travels in GIT_AUTHOR_* and
// GIT_COMMITTER_*, and the message is piped to commit-tree.
type Runner interface {
	Run(env map[string]string, stdin string, args ...string) (stdout string, err error)
}

// Identity is a name and email pair.
type Identity struct {
	Name  string
	Email string
}

func (i Identity) String() string { return fmt.Sprintf("%s <%s>", i.Name, i.Email) }

func (i Identity) equal(other Identity) bool {
	return i.Name == other.Name && strings.EqualFold(i.Email, other.Email)
}

// Selector decides which commits are rewritten. A commit is selected when it
// matches any populated field. With none populated, every commit in Range is
// selected, which is the "rewrite this whole branch" mode.
type Selector struct {
	// FromEmail matches the author email, case-insensitively.
	FromEmail string
	// Commits are explicit revisions.
	Commits []string
	// Range limits the scope. Empty means HEAD.
	Range string
}

func (s Selector) filtered() bool {
	return s.FromEmail != "" || len(s.Commits) > 0
}

// Options tunes the rewrite.
type Options struct {
	// AuthorOnly leaves the committer identity untouched. The default rewrites
	// both, since a leaked identity is normally both author and committer.
	AuthorOnly bool
}

// Change is one commit the plan will rewrite.
type Change struct {
	OldHash   string
	NewHash   string
	Subject   string
	OldAuthor Identity
}

// Plan is the full picture: which branch, how many commits were examined, and
// which of them change.
type Plan struct {
	Branch  string
	Head    string
	Total   int
	Changes []Change

	commits  []commit
	selected map[string]bool
}

// commit is a rebuildable snapshot of one commit.
type commit struct {
	hash    string
	tree    string
	parents []string

	author    Identity
	authored  string
	committer Identity
	committed string

	message string
}

// BuildPlan inspects the repository and reports what would change, without
// touching anything.
func BuildPlan(r Runner, sel Selector, target Identity) (*Plan, error) {
	if err := assertClean(r); err != nil {
		return nil, err
	}

	branch, err := currentBranch(r)
	if err != nil {
		return nil, err
	}

	commits, err := readCommits(r, sel.Range)
	if err != nil {
		return nil, err
	}

	explicit := map[string]bool{}
	for _, h := range sel.Commits {
		explicit[h] = true
	}

	plan := &Plan{
		Branch:   branch,
		Total:    len(commits),
		commits:  commits,
		selected: map[string]bool{},
	}
	if len(commits) > 0 {
		plan.Head = commits[len(commits)-1].hash
	}

	for _, c := range commits {
		if !matches(sel, explicit, c) {
			continue
		}
		// A commit already carrying the target identity would be rebuilt byte
		// for byte, so reporting it as a change would be untrue.
		if c.author.equal(target) && c.committer.equal(target) {
			continue
		}

		plan.selected[c.hash] = true
		plan.Changes = append(plan.Changes, Change{
			OldHash:   c.hash,
			Subject:   subjectOf(c.message),
			OldAuthor: c.author,
		})
	}
	return plan, nil
}

func matches(sel Selector, explicit map[string]bool, c commit) bool {
	if !sel.filtered() {
		return true
	}
	if explicit[c.hash] {
		return true
	}
	return sel.FromEmail != "" && strings.EqualFold(c.author.Email, sel.FromEmail)
}

// Apply rebuilds the history and moves the branch. It returns the backup ref
// holding the original head, or an empty string when the plan was empty.
//
// Every commit in range is rebuilt, not only the selected ones: rewriting an
// ancestor changes its hash, so each descendant has to be rebuilt against the
// new parent.
func Apply(r Runner, plan *Plan, target Identity, opts Options) (string, error) {
	if plan == nil || len(plan.Changes) == 0 {
		return "", nil
	}

	// Rebuild first. Nothing is referenced yet, so a failure here leaves the
	// repository exactly as it was.
	remap := map[string]string{}
	newHead := ""

	for _, c := range plan.commits {
		env := identityEnv(c, plan.selected[c.hash], target, opts)

		args := []string{"commit-tree", c.tree}
		for _, p := range c.parents {
			mapped, ok := remap[p]
			if !ok {
				// A parent outside the range keeps its original hash.
				mapped = p
			}
			args = append(args, "-p", mapped)
		}
		args = append(args, "-F", "-")

		out, err := r.Run(env, c.message, args...)
		if err != nil {
			return "", fmt.Errorf("rebuild %s: %w", c.hash, err)
		}

		newHash := strings.TrimSpace(out)
		remap[c.hash] = newHash
		newHead = newHash
	}

	for i := range plan.Changes {
		plan.Changes[i].NewHash = remap[plan.Changes[i].OldHash]
	}

	// Record where the branch was before moving it. This is the only way back.
	backupRef := backupPrefix + time.Now().UTC().Format("20060102-150405")
	if _, err := r.Run(nil, "", "update-ref", backupRef, plan.Head); err != nil {
		return "", fmt.Errorf("record the backup ref: %w", err)
	}

	// The three-argument form is a compare-and-swap: it fails if the branch
	// moved while the rewrite was running.
	ref := "refs/heads/" + plan.Branch
	if _, err := r.Run(nil, "", "update-ref", ref, newHead, plan.Head); err != nil {
		return "", fmt.Errorf("move %s (the history is rebuilt but unreferenced; recover from %s): %w", ref, backupRef, err)
	}
	return backupRef, nil
}

// identityEnv builds the environment for one commit-tree call, keeping both
// original dates so rewriting authorship never moves the timeline.
func identityEnv(c commit, selected bool, target Identity, opts Options) map[string]string {
	author, committer := c.author, c.committer
	if selected {
		author = target
		if !opts.AuthorOnly {
			committer = target
		}
	}

	return map[string]string{
		"GIT_AUTHOR_NAME":     author.Name,
		"GIT_AUTHOR_EMAIL":    author.Email,
		"GIT_AUTHOR_DATE":     c.authored,
		"GIT_COMMITTER_NAME":  committer.Name,
		"GIT_COMMITTER_EMAIL": committer.Email,
		"GIT_COMMITTER_DATE":  c.committed,
	}
}

func assertClean(r Runner) error {
	out, err := r.Run(nil, "", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("inspect the working tree: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return ErrDirtyWorkingTree
	}
	return nil
}

func currentBranch(r Runner) (string, error) {
	out, err := r.Run(nil, "", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve the current branch: %w", err)
	}

	branch := strings.TrimSpace(out)
	if branch == "" || branch == "HEAD" {
		return "", ErrDetachedHead
	}
	return branch, nil
}

func readCommits(r Runner, rng string) ([]commit, error) {
	if strings.TrimSpace(rng) == "" {
		rng = "HEAD"
	}

	// --reverse gives oldest first, which is the order a rebuild needs: a
	// parent must exist before its child is built.
	out, err := r.Run(nil, "", "rev-list", "--reverse", "--topo-order", rng)
	if err != nil {
		return nil, fmt.Errorf("list commits: %w", err)
	}

	var commits []commit
	for _, hash := range strings.Fields(out) {
		c, err := readCommit(r, hash)
		if err != nil {
			return nil, err
		}
		commits = append(commits, c)
	}
	return commits, nil
}

func readCommit(r Runner, hash string) (commit, error) {
	out, err := r.Run(nil, "", "show", "-s", "--format="+metaFormat, hash)
	if err != nil {
		return commit{}, fmt.Errorf("read %s: %w", hash, err)
	}

	fields := strings.Split(out, "\x00")
	if len(fields) < 9 {
		return commit{}, fmt.Errorf("read %s: got %d fields, want 9", hash, len(fields))
	}

	return commit{
		hash:      hash,
		tree:      fields[0],
		parents:   strings.Fields(fields[1]),
		author:    Identity{Name: fields[2], Email: fields[3]},
		authored:  fields[4],
		committer: Identity{Name: fields[5], Email: fields[6]},
		committed: fields[7],
		message:   fields[8],
	}, nil
}

func subjectOf(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		return message[:i]
	}
	return message
}
