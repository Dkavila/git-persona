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

// recoveryPrompt is the exact question shown when a key already exists. The
// capital R marks the default, which is deliberately the non-destructive one.
const recoveryPrompt = "An SSH key for this profile already exists. " +
	"Do you want to (R)ecover the existing key or (O)verwrite it with a new one? [R/o]: "

func newAddCmd(d Deps) *cobra.Command {
	var name, email string

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Register a new profile and generate its ed25519 key",
		Long: "add registers a profile and creates its ed25519 key pair.\n\n" +
			"If a key for the profile name already exists on disk, add shows the\n" +
			"existing public key and asks whether to reuse it or replace it, rather\n" +
			"than deciding for you.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAdd(cmd, d, name, email)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "profile name (prompted when omitted)")
	cmd.Flags().StringVar(&email, "email", "", "git email for this profile (prompted when omitted)")
	return cmd
}

func runAdd(cmd *cobra.Command, d Deps, name, email string) error {
	// One scanner for the whole command. A second scanner over the same reader
	// would discard whatever the first had already buffered, silently eating
	// the answer to the recovery prompt.
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

	// Validate and reject duplicates before touching any key material, so a
	// rejected profile never leaves orphaned files behind.
	if err := profile.Validate(); err != nil {
		return err
	}
	if _, exists := store.Get(name); exists {
		return fmt.Errorf("%w: %q", config.ErrDuplicateProfile, name)
	}

	keyPath, err := resolveKey(cmd, d, in, out, name, email)
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

// resolveKey returns the key path to record, generating, reusing or replacing
// the key pair as the situation requires.
func resolveKey(cmd *cobra.Command, d Deps, in *bufio.Scanner, out io.Writer, name, email string) (string, error) {
	keyPath := ssh.KeyPathFor(d.Home, name)

	exists, err := ssh.KeyExists(keyPath)
	if err != nil {
		return "", err
	}
	if !exists {
		return d.Keys.Generate(cmd.Context(), d.Home, name, email)
	}

	// A key with no profile behind it usually means a removed profile, or one
	// carried over from another machine. Either way the choice is the user's,
	// so show them what is on disk before asking.
	showExistingKey(out, keyPath)

	if askOverwrite(in, out) {
		return d.Keys.Overwrite(cmd.Context(), d.Home, name, email)
	}
	fmt.Fprintf(out, "\nRecovering the existing key.\n")
	return keyPath, nil
}

// showExistingKey prints the public half so the user can read the comment at
// the end of it, which is normally the email the key was created for.
func showExistingKey(out io.Writer, keyPath string) {
	fmt.Fprintf(out, "\nA key already exists at:\n  %s\n", keyPath)

	pub, err := ssh.PublicKey(keyPath)
	if err != nil {
		// The private key is what matters; a missing or unreadable .pub is not
		// a reason to skip the question.
		fmt.Fprintf(out, "\nIts public key could not be read (%v).\n", err)
		return
	}
	fmt.Fprintf(out, "\nIts public key is:\n\n%s\n", pub)
}

// askOverwrite reports whether the user chose to replace the key. Anything
// unrecognised re-asks; an empty answer or end of input takes the default,
// which is to recover, so an unattended run can never destroy a key.
func askOverwrite(in *bufio.Scanner, out io.Writer) bool {
	for {
		fmt.Fprint(out, "\n"+recoveryPrompt)

		if !in.Scan() {
			fmt.Fprintln(out)
			return false
		}

		switch strings.ToLower(strings.TrimSpace(strings.TrimRight(in.Text(), "\r"))) {
		case "", "r", "recover":
			return false
		case "o", "overwrite":
			return true
		default:
			fmt.Fprintln(out, "Please answer R to recover or O to overwrite.")
		}
	}
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
