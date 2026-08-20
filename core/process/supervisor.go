// Package process runs one explicitly configured local process in the
// foreground. It does not provide an OS sandbox for that process.
package process

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/provehito-project/provehito/core/adapter"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/workspace"
)

// Request describes one foreground launch. Args are appended to the profile's
// fixed argument vector and are passed literally to the executable.
type Request struct {
	Workspace string
	Lease     *workspace.Lease
	Profile   adapter.Profile
	Args      []string
}

// Result records process exit and bounded stream evidence.
type Result struct {
	ExitCode        int
	Duration        time.Duration
	Stdout          []byte
	Stderr          []byte
	StdoutHash      string
	StderrHash      string
	StdoutTruncated bool
	StderrTruncated bool
	TimedOut        bool
	Canceled        bool
	Signaled        bool
}

// Supervisor is a stateless foreground process supervisor.
type Supervisor struct{}

// Run starts, waits for, and records one configured process.
func (s *Supervisor) Run(ctx context.Context, request Request) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRequest(request); err != nil {
		return Result{}, err
	}
	args := make([]string, 0, len(request.Profile.Args)+len(request.Args))
	args = append(args, request.Profile.Args...)
	args = append(args, request.Args...)
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(runContext, request.Profile.Executable, args...)
	command.Dir = request.Workspace
	command.Env = allowlistedEnvironment(request.Profile.EnvAllowlist)
	configureProcessGroup(command)

	budget := &outputBudget{remaining: request.Profile.OutputLimit}
	stdout := newBoundedWriter(budget, request.Profile.OutputLimit)
	stderr := newBoundedWriter(budget, request.Profile.OutputLimit)
	command.Stdout = stdout
	command.Stderr = stderr
	started := time.Now()
	if err := command.Start(); err != nil {
		empty := emptyStreamHash()
		return Result{
			ExitCode:   -1,
			Duration:   time.Since(started),
			StdoutHash: empty,
			StderrHash: empty,
		}, failure.Wrap(failure.ToolingOrAdapter, "launch agent process", err)
	}

	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	timer := time.NewTimer(request.Profile.Timeout)
	defer timer.Stop()
	var waitErr error
	var terminationErr error
	var timedOut bool
	var canceled bool
	select {
	case waitErr = <-wait:
	case <-timer.C:
		timedOut = true
		terminationErr = terminateProcessGroup(command.Process.Pid)
		cancel()
		waitErr = <-wait
	case <-ctx.Done():
		canceled = true
		terminationErr = terminateProcessGroup(command.Process.Pid)
		cancel()
		waitErr = <-wait
	}

	result := Result{
		ExitCode:        processExitCode(waitErr),
		Duration:        time.Since(started),
		Stdout:          stdout.Bytes(),
		Stderr:          stderr.Bytes(),
		StdoutHash:      stdout.Hash(),
		StderrHash:      stderr.Hash(),
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
		Signaled:        processSignaled(waitErr),
	}
	if terminationErr != nil {
		return result, failure.Wrap(failure.ToolingOrAdapter, "terminate agent process group", terminationErr)
	}
	if canceled {
		result.Canceled = true
		return result, failure.Wrap(failure.ToolingOrAdapter, "agent process canceled", ctx.Err())
	}
	if timedOut {
		result.TimedOut = true
		return result, failure.Wrap(failure.ToolingOrAdapter, "agent process timeout", context.DeadlineExceeded)
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return result, failure.Wrap(failure.ToolingOrAdapter, "wait for agent process", waitErr)
		}
	}
	return result, nil
}

func validateRequest(request Request) error {
	if request.Workspace == "" {
		return failure.New(failure.UsageOrSchema, "agent workspace")
	}
	if err := adapter.Validate(request.Profile); err != nil {
		return err
	}
	if err := validateArguments(request.Args); err != nil {
		return err
	}
	leasePath, ok := request.Lease.ActiveWorkspace()
	if !ok || leasePath == "" {
		return failure.New(failure.UsageOrSchema, "agent workspace lease")
	}
	requested, err := workspace.CanonicalPath(request.Workspace)
	if err != nil {
		return err
	}
	leased, err := workspace.CanonicalPath(leasePath)
	if err != nil {
		return err
	}
	same, err := workspace.SamePath(requested, leased)
	if err != nil {
		return err
	}
	if !same {
		return failure.New(failure.Integrity, "agent workspace lease identity")
	}
	return nil
}

func validateArguments(args []string) error {
	for _, arg := range args {
		if strings.IndexByte(arg, 0) >= 0 {
			return failure.New(failure.UsageOrSchema, "agent arguments")
		}
	}
	return nil
}

func allowlistedEnvironment(names []string) []string {
	environment := os.Environ()
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = entry
		}
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if value, ok := values[name]; ok {
			result = append(result, value)
		}
	}
	return result
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ProcessState.ExitCode()
	}
	return -1
}

func processSignaled(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ProcessState.Sys() != nil && exitErr.ProcessState.ExitCode() < 0
}

type outputBudget struct {
	mu        sync.Mutex
	remaining int64
}

func (b *outputBudget) take(size int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining <= 0 {
		return 0
	}
	if int64(size) <= b.remaining {
		b.remaining -= int64(size)
		return size
	}
	taken := int(b.remaining)
	b.remaining = 0
	return taken
}

type boundedWriter struct {
	budget    *outputBudget
	data      bytes.Buffer
	hasher    hashWriter
	truncated bool
}

type hashWriter struct {
	hash hash.Hash
}

func newBoundedWriter(budget *outputBudget, _ int64) *boundedWriter {
	return &boundedWriter{budget: budget, hasher: hashWriter{hash: sha256.New()}}
}

func (w *boundedWriter) Write(value []byte) (int, error) {
	_, _ = w.hasher.hash.Write(value)
	taken := w.budget.take(len(value))
	if taken > 0 {
		_, _ = w.data.Write(value[:taken])
	}
	if taken != len(value) {
		w.truncated = true
	}
	return len(value), nil
}

func (w *boundedWriter) Bytes() []byte { return append([]byte(nil), w.data.Bytes()...) }

func (w *boundedWriter) Hash() string {
	return hex.EncodeToString(w.hasher.hash.Sum(nil))
}

func (w *boundedWriter) Truncated() bool { return w.truncated }

func emptyStreamHash() string {
	return hex.EncodeToString(sha256.New().Sum(nil))
}
