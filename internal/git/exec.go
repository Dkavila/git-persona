package git

import (
	"bytes"
	"errors"
	"os/exec"
)

// ExecRunner is the production Runner: it shells out to the real git binary.
//
// It is the only part of this package that touches the operating system, which
// is why every other function takes a Runner instead of calling exec directly.
type ExecRunner struct {
	// Bin is the git executable to invoke. Empty means "git" from PATH.
	Bin string
}

// Run executes git with the given arguments and returns its standard output.
// A non-zero exit is reported as *ExitError, carrying the code and stderr so
// callers can distinguish "key not set" from a real failure.
func (e ExecRunner) Run(args ...string) (string, error) {
	bin := e.Bin
	if bin == "" {
		bin = "git"
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), &ExitError{
				Code:   exitErr.ExitCode(),
				Stderr: stderr.String(),
			}
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

// compile-time assertion that the production runner satisfies the interface.
var _ Runner = ExecRunner{}
