package cli

import "github.com/spf13/cobra"

// NewRootCmd builds the full command tree.
func NewRootCmd(d Deps) *cobra.Command {
	root := &cobra.Command{
		Use:   "git-persona",
		Short: "Manage and switch between multiple Git and SSH identities",
		Long: "git-persona switches Git identities by writing user.name, user.email\n" +
			"and core.sshCommand into your global Git config, so your ~/.ssh/config\n" +
			"is never touched.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newAddCmd(d),
		newUseCmd(d),
		newListCmd(d),
		newCleanCmd(d),
	)
	return root
}
