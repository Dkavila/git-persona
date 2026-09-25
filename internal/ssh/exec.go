package ssh

import (
	"context"
	"os/exec"
)

// ExecRunner is the production Runner: it shells out to a real binary and
// returns its combined output.
//
// Combining stdout and stderr is required, not incidental. OpenSSH writes the
// GitHub authentication banner to stderr, so reading stdout alone would see an
// empty transcript and report every working key as broken.
type ExecRunner struct {
	// Bin is the executable to invoke, for example "ssh" or "ssh-keygen".
	Bin string
}

// Run executes the binary with the given arguments under ctx. A non-zero exit
// is returned as an error *together with* whatever output was produced, since
// for the GitHub probe the output is the verdict and the exit code is noise.
func (e ExecRunner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// NewExecManager returns a Manager wired to the real ssh-keygen and ssh
// binaries on PATH.
func NewExecManager() *Manager {
	return New(ExecRunner{Bin: "ssh-keygen"}, ExecRunner{Bin: "ssh"})
}

// compile-time assertion that the production runner satisfies the interface.
var _ Runner = ExecRunner{}
