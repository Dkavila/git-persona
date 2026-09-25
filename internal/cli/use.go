package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Dkavila/git-persona/internal/config"
)

func newUseCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "use <profile>",
		Short: "Apply a profile to the global Git configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			store, err := config.Load(d.Home)
			if err != nil {
				return err
			}

			profile, ok := store.Get(name)
			if !ok {
				return fmt.Errorf("%w: %q", config.ErrProfileNotFound, name)
			}

			// Apply first: the active marker is only truthful once the global
			// config actually carries the identity.
			if err := d.Git.ApplyProfile(profile); err != nil {
				return err
			}
			if err := store.SetActive(profile.Name); err != nil {
				return err
			}
			if err := store.Save(d.Home); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Now using %q (%s)\n", profile.Name, profile.Email)
			return nil
		},
	}
}
