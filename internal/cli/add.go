package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Dkavila/git-persona/internal/config"
	"github.com/Dkavila/git-persona/internal/ssh"
)

func newAddCmd(d Deps) *cobra.Command {
	var name, email string

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Register a new profile and generate its ed25519 key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAdd(cmd, d, name, email)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "profile name (prompted when omitted)")
	cmd.Flags().StringVar(&email, "email", "", "git email for this profile (prompted when omitted)")
	return cmd
}

func runAdd(cmd *cobra.Command, d Deps, name, email string) error {
	in := bufio.NewScanner(cmd.InOrStdin())
	out := cmd.OutOrStdout()

	var err error
	if name, err = resolve(in, out, name, "Profile name: "); err != nil {
		return err
	}
	if email, err = resolve(in, out, email, "Git email: "); err != nil {
		return err
	}

	store, err := config.Load(d.Home)
	if err != nil {
		return err
	}

	profile := config.Profile{Name: name, Email: email, CreatedAt: d.now()}

	// Validate and reject duplicates before generating a key, so a rejected
	// profile never leaves orphaned key material on disk.
	if err := profile.Validate(); err != nil {
		return err
	}
	if _, exists := store.Get(name); exists {
		return fmt.Errorf("%w: %q", config.ErrDuplicateProfile, name)
	}

	keyPath, err := d.Keys.Generate(cmd.Context(), d.Home, name, email)
	if err != nil {
		return err
	}
	profile.KeyPath = keyPath

	if err := store.Add(profile); err != nil {
		return err
	}
	if err := store.Save(d.Home); err != nil {
		return err
	}

	fmt.Fprintf(out, "\nProfile %q created.\n", name)
	fmt.Fprintf(out, "Key: %s\n", keyPath)

	if pub, err := ssh.PublicKey(keyPath); err == nil {
		fmt.Fprintf(out, "\nAdd this public key to your Git provider:\n\n%s\n", pub)
	}
	fmt.Fprintf(out, "\nThen run: git-persona use %s\n", name)
	return nil
}

// resolve returns the flag value when set, otherwise prompts for one.
func resolve(in *bufio.Scanner, out io.Writer, value, prompt string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}

	fmt.Fprint(out, prompt)
	if !in.Scan() {
		if err := in.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("no input for %q", strings.TrimSuffix(prompt, ": "))
	}
	// Trim CR as well as spaces: Windows terminals send CRLF, and a stray
	// carriage return would end up inside the name and the key filename.
	return strings.TrimSpace(strings.TrimRight(in.Text(), "\r")), nil
}
