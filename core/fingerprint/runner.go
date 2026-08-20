package fingerprint

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

// Runner executes one Git process with an argument vector and no shell.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// ExecRunner is the standard-library Git process runner. Its environment is
// rebuilt for every invocation so host locale and timezone settings cannot
// change candidate identity.
type ExecRunner struct {
	Executable string
	Err        error
}

// NewRunner is the public constructor for the standard-library Git runner.
// An optional executable is useful for deterministic adapter tests; normal
// callers should omit it so Git is resolved from the host PATH once.
func NewRunner(executable ...string) Runner {
	if len(executable) != 0 && executable[0] != "" {
		return &ExecRunner{Executable: executable[0]}
	}
	return NewExecRunner()
}

// NewExecRunner locates Git once and retains only its absolute executable
// path. If lookup fails, the returned runner preserves the error until use.
func NewExecRunner() *ExecRunner {
	executable, err := exec.LookPath("git")
	if err != nil {
		return &ExecRunner{Err: err}
	}
	return &ExecRunner{Executable: executable}
}

// Run executes Git in dir with args and returns stdout exactly as produced.
func (r *ExecRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("git executable is not configured")
	}
	if r.Err != nil {
		return nil, fmt.Errorf("locate git: %w", r.Err)
	}
	if r.Executable == "" {
		return nil, fmt.Errorf("git executable is not configured")
	}
	hardened := hardenedGitArgs(args)
	cmd := exec.CommandContext(ctx, r.Executable, hardened...)
	cmd.Dir = dir
	cmd.Env = explicitEnvironment(r.Executable)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("git %v: %w: %s", hardened, err, exitErr.Stderr)
		}
		return nil, fmt.Errorf("git %v: %w", hardened, err)
	}
	return out, nil
}

func hardenedGitArgs(args []string) []string {
	prefix := []string{
		"--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false",
	}
	return append(prefix, args...)
}

func explicitEnvironment(executable string) []string {
	pathValue := filepath.Dir(executable)
	return []string{
		"PATH=" + pathValue,
		"LC_ALL=C",
		"LANG=C",
		"TZ=UTC",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
	}
}
