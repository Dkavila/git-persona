package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Dkavila/git-pilot/internal/config"
	"github.com/Dkavila/git-pilot/internal/ssh"
)

func newRemoveCmd(d Deps) *cobra.Command {
	var purgeKey bool

	cmd := &cobra.Command{
		Use:     "remove <profile>",
		Aliases: []string{"rm"},
		Short:   "Remove a profile, optionally deleting its SSH key",
		Long: "remove deletes a profile from the store. By default the SSH key pair is\n" +
			"left on disk, because it may already be registered with a Git provider.\n" +
			"Pass --purge-key to delete it as well, which cannot be undone.\n\n" +
			"Removing the active profile also unsets user.name, user.email and\n" +
			"core.sshCommand from the global Git config, so Git is not left pointing\n" +
			"at an identity that no longer exists.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(cmd, d, args[0], purgeKey)
		},
	}

	cmd.Flags().BoolVar(&purgeKey, "purge-key", false,
		"also delete the profile's SSH key pair from disk (irreversible)")
	return cmd
}

func runRemove(cmd *cobra.Command, d Deps, name string, purgeKey bool) error {
	out := cmd.OutOrStdout()

	store, err := config.Load(d.Home)
	if err != nil {
		return err
	}

	profile, ok := store.Get(name)
	if !ok {
		return fmt.Errorf("%w: %q", config.ErrProfileNotFound, name)
	}

	wasActive := strings.EqualFold(store.Active, profile.Name)

	// Clear the global identity before touching the store. If this fails, the
	// profile stays recorded: a Git config pointing at an identity with no
	// record behind it is worse than a profile that outlives one failed
	// removal.
	if wasActive {
		if err := d.Git.UnsetGlobal(); err != nil {
			return fmt.Errorf("clear global identity: %w", err)
		}
	}

	if err := store.Remove(profile.Name); err != nil {
		return err
	}
	if err := store.Save(d.Home); err != nil {
		return err
	}

	if purgeKey {
		if err := ssh.RemoveKeyPair(profile.KeyPath); err != nil {
			return fmt.Errorf("profile %q was removed but its key could not be deleted: %w", profile.Name, err)
		}
		fmt.Fprintf(out, "Removed profile %q and deleted its key pair.\n", profile.Name)
	} else {
		fmt.Fprintf(out, "Removed profile %q.\n", profile.Name)
		fmt.Fprintf(out, "Its SSH key was kept at: %s\n", profile.KeyPath)
		fmt.Fprintf(out, "Re-run with --purge-key to delete it, or remove it yourself.\n")
	}

	if wasActive {
		fmt.Fprintf(out, "\nWarning: %q was the active profile. user.name, user.email and\n", profile.Name)
		fmt.Fprintln(out, "core.sshCommand have been unset from your global Git config.")
		fmt.Fprintln(out, `Run "git-pilot use <profile>" to select another identity.`)
	}
	return nil
}
