package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCleanCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "clean [path]",
		Short: "Unset local Git identity so the global profile applies again",
		Long: "clean removes user.name, user.email and core.sshCommand from a\n" +
			"repository's local config. Without a path it cleans the current\n" +
			"directory.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}

			if err := d.Git.CleanLocal(path); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Cleared local identity in %s\n", path)
			return nil
		},
	}
}
