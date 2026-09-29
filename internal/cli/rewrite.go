package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Dkavila/git-persona/internal/config"
	"github.com/Dkavila/git-persona/internal/rewrite"
)

func newRewriteCmd(d Deps) *cobra.Command {
	var (
		fromEmail  string
		commits    []string
		revRange   string
		repo       string
		apply      bool
		authorOnly bool
	)

	cmd := &cobra.Command{
		Use:   "rewrite <profile>",
		Short: "Rewrite commit authorship to a registered profile",
		Long: "rewrite replaces the author of existing commits with a registered\n" +
			"profile's identity, for when the wrong persona was active while\n" +
			"committing.\n\n" +
			"Selection: --from matches an author email, --commit names a specific\n" +
			"commit, and --range limits the scope. With none of them, every commit\n" +
			"in the range is rewritten.\n\n" +
			"This is a dry run unless --apply is passed. Applying changes the hash\n" +
			"of every rewritten commit and of every commit after it, so a branch\n" +
			"that is already published will need a force push.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			store, err := config.Load(d.Home)
			if err != nil {
				return err
			}

			profile, ok := store.Get(args[0])
			if !ok {
				return fmt.Errorf("%w: %q", config.ErrProfileNotFound, args[0])
			}
			target := rewrite.Identity{Name: profile.Name, Email: profile.Email}

			plan, err := d.Rewriter.BuildPlan(repo, rewrite.Selector{
				FromEmail: fromEmail,
				Commits:   commits,
				Range:     revRange,
			}, target)
			if err != nil {
				return err
			}

			if len(plan.Changes) == 0 {
				fmt.Fprintf(out, "Nothing to rewrite: none of the %d commits examined on %s need changing.\n",
					plan.Total, plan.Branch)
				return nil
			}

			if err := printPlan(out, plan, target, apply); err != nil {
				return err
			}

			if !apply {
				fmt.Fprintf(out, "\nNothing has changed. Re-run with --apply to rewrite.\n")
				return nil
			}

			backupRef, err := d.Rewriter.Apply(repo, plan, target, rewrite.Options{AuthorOnly: authorOnly})
			if err != nil {
				return err
			}

			printAftermath(out, plan, backupRef)
			return nil
		},
	}

	cmd.Flags().StringVar(&fromEmail, "from", "", "rewrite commits whose author email matches")
	cmd.Flags().StringArrayVar(&commits, "commit", nil, "rewrite this commit (repeatable)")
	cmd.Flags().StringVar(&revRange, "range", "", "limit the scope to this revision range (default HEAD)")
	cmd.Flags().StringVar(&repo, "repo", ".", "repository to rewrite")
	cmd.Flags().BoolVar(&apply, "apply", false, "actually rewrite; without this the command only reports")
	cmd.Flags().BoolVar(&authorOnly, "author-only", false, "leave the committer identity untouched")
	return cmd
}

func printPlan(out interface{ Write([]byte) (int, error) }, plan *rewrite.Plan, target rewrite.Identity, apply bool) error {
	verb := "Would rewrite"
	if apply {
		verb = "Rewriting"
	}
	fmt.Fprintf(out, "%s %d of %d commits on %s to %q\n\n",
		verb, len(plan.Changes), plan.Total, plan.Branch, target.String())

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "COMMIT\tSUBJECT\tCURRENT AUTHOR")
	for _, c := range plan.Changes {
		fmt.Fprintf(w, "%s\t%s\t%s\n", short(c.OldHash), c.Subject, c.OldAuthor.String())
	}
	return w.Flush()
}

// printAftermath explains the two things the user now has to know: how to
// publish the result, and how to undo it.
func printAftermath(out interface{ Write([]byte) (int, error) }, plan *rewrite.Plan, backupRef string) {
	fmt.Fprintf(out, "\nRewrote %d commits on %s.\n", len(plan.Changes), plan.Branch)
	fmt.Fprintf(out, "Backup: %s\n", backupRef)

	fmt.Fprintf(out, "\nEvery commit from the oldest rewritten one onward has a new hash.\n")
	fmt.Fprintf(out, "If this branch is already published, update it with:\n\n")
	fmt.Fprintf(out, "    git push --force-with-lease\n")

	fmt.Fprintf(out, "\nTo undo:\n\n")
	fmt.Fprintf(out, "    git reset --hard %s\n", backupRef)
}

// short trims a hash for display while staying unambiguous in practice.
func short(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}
