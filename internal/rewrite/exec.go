package rewrite

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ExecRunner is the production Runner: it shells out to the real git binary.
//
// It is the only part of this package that touches the operating system, which
// is why everything else takes a Runner instead of calling exec directly.
type ExecRunner struct {
	// Bin is the git executable. Empty means "git" from PATH.
	Bin string
	// Dir is the repository to operate in. Empty means the current directory.
	Dir string
}

// Run executes git with the given arguments, environment additions and stdin.
//
// env is merged onto the process environment rather than replacing it: git
// needs PATH, HOME and the rest to function, and only the GIT_AUTHOR_* and
// GIT_COMMITTER_* entries are being overridden.
func (e ExecRunner) Run(env map[string]string, stdin string, args ...string) (string, error) {
	bin := e.Bin
	if bin == "" {
		bin = "git"
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = e.Dir

	if len(env) > 0 {
		merged := os.Environ()
		for k, v := range env {
			merged = append(merged, k+"="+v)
		}
		cmd.Env = merged
	}

	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.String(), err
		}
		return stdout.String(), fmt.Errorf("%w: %s", err, msg)
	}
	return stdout.String(), nil
}

// compile-time assertion that the production runner satisfies the interface.
var _ Runner = ExecRunner{}
