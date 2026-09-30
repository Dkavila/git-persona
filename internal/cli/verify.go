package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Dkavila/git-pilot/internal/config"
	"github.com/Dkavila/git-pilot/internal/verify"
)

func newVerifyCmd(d Deps) *cobra.Command {
	var timeout time.Duration
	var concurrency int

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check every profile's SSH connection to GitHub, concurrently",
		Long: "verify probes each registered profile against GitHub at the same time,\n" +
			"so checking ten identities takes about as long as the slowest one\n" +
			"rather than the sum of all of them.\n\n" +
			"Exits non-zero if any profile fails to authenticate, which makes it\n" +
			"usable as a health check in a script.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			store, err := config.Load(d.Home)
			if err != nil {
				return err
			}
			if len(store.Profiles) == 0 {
				fmt.Fprintln(out, "No profiles yet. Create one with: git-pilot add")
				return nil
			}

			results := verify.All(cmd.Context(), d.Prober, store.Profiles, verify.Options{
				Timeout:     timeout,
				Concurrency: concurrency,
			})

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "STATUS\tPROFILE\tACCOUNT\tTOOK")

			failed := 0
			for _, r := range results {
				if r.Status != verify.StatusOK {
					failed++
				}

				account := r.Username
				if account == "" {
					account = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					r.Status, r.Profile.Name, account, r.Duration.Round(time.Millisecond))
			}
			if err := w.Flush(); err != nil {
				return err
			}

			// Print the reasons after the table so the table stays aligned.
			for _, r := range results {
				if r.Err != nil {
					fmt.Fprintf(out, "\n%s: %v\n", r.Profile.Name, r.Err)
				}
			}

			if failed > 0 {
				return fmt.Errorf("%d of %d profiles failed to authenticate", failed, len(results))
			}
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "per-profile probe timeout")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "max simultaneous probes (0 = one per profile)")
	return cmd
}
