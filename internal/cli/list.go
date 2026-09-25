package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Dkavila/git-persona/internal/config"
)

func newListCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List registered profiles and show the active one",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			store, err := config.Load(d.Home)
			if err != nil {
				return err
			}

			// An empty store is a normal first-run state, so it gets a hint
			// rather than an error or a blank screen.
			if len(store.Profiles) == 0 {
				fmt.Fprintln(out, "No profiles yet. Create one with: git-persona add")
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "\tNAME\tEMAIL\tKEY")
			for _, p := range store.Profiles {
				marker := " "
				if p.Name == store.Active {
					marker = "*"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", marker, p.Name, p.Email, p.KeyPath)
			}
			return w.Flush()
		},
	}
}
