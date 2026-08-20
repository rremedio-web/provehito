package process_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/provehito-project/provehito/core/adapter"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/process"
	"github.com/provehito-project/provehito/core/workspace"
)

func TestSupervisorDoesNotPassAmbientSecret(t *testing.T) {
	t.Setenv("UNDECLARED_SECRET", "must-not-pass")
	result, err := runFakeAgent(t, context.Background(), adapter.Profile{EnvAllowlist: []string{"PATH"}}, "--print-env")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Stdout), "must-not-pass") {
		t.Fatal("ambient environment leaked")
	}
}

func runFakeAgent(t *testing.T, ctx context.Context, profile adapter.Profile, args ...string) (process.Result, error) {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	work := t.TempDir()
	binary := filepath.Join(t.TempDir(), "fake-agent")
	build := exec.Command("go", "build", "-o", binary, "./testdata/fake-agent")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake agent: %v\n%s", err, output)
	}
	manager := workspace.NewLeaseManager(t.TempDir())
	lease, err := manager.Acquire("test", work)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	profile.ID = "fake-agent"
	if profile.Family == "" {
		profile.Family = "test"
	}
	if len(profile.Capabilities) == 0 {
		profile.Capabilities = []string{"writer"}
	}
	if profile.Timeout == 0 {
		profile.Timeout = 2 * time.Second
	}
	if profile.OutputLimit == 0 {
		profile.OutputLimit = 4096
	}
	if profile.Executable == "" {
		profile.Executable = binary
	}
	result, err := (&process.Supervisor{}).Run(ctx, process.Request{
		Workspace: work,
		Lease:     lease,
		Profile:   profile,
		Args:      args,
	})
	return result, err
}

func TestSupervisorUsesWorkspaceAndLiteralArguments(t *testing.T) {
	literalPath := filepath.Join(t.TempDir(), "should-not-exist")
	literal := "$(touch " + literalPath + ")"
	result, err := runFakeAgent(t, context.Background(), adapter.Profile{EnvAllowlist: []string{"PATH"}}, "--pwd", "--args", literal)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(result.Stdout)); got == "" || !strings.Contains(got, literal) {
		t.Fatalf("literal args not preserved: %q", got)
	}
	if _, err := os.Stat(literalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("literal argument invoked a shell: stat err=%v", err)
	}
}

func TestSupervisorReportsSuccessAndChildNonzeroAsResult(t *testing.T) {
	result, err := runFakeAgent(t, context.Background(), adapter.Profile{}, "--exit", "0")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("success: result=%#v err=%v", result, err)
	}
	result, err = runFakeAgent(t, context.Background(), adapter.Profile{}, "--exit", "7")
	if err != nil || result.ExitCode != 7 {
		t.Fatalf("nonzero: result=%#v err=%v", result, err)
	}
}

func TestSupervisorAllowlistIncludesDeclaredValueOnly(t *testing.T) {
	t.Setenv("DECLARED_VALUE", "visible")
	t.Setenv("UNDECLARED_SECRET", "hidden")
	result, err := runFakeAgent(t, context.Background(), adapter.Profile{EnvAllowlist: []string{"DECLARED_VALUE"}}, "--print-env")
	if err != nil {
		t.Fatal(err)
	}
	text := string(result.Stdout)
	if !strings.Contains(text, "DECLARED_VALUE=visible") || strings.Contains(text, "UNDECLARED_SECRET=hidden") {
		t.Fatalf("allowlist mismatch: %q", text)
	}
}

func TestSupervisorBoundsCombinedOutputAndHashesFullStreams(t *testing.T) {
	result, err := runFakeAgent(t, context.Background(), adapter.Profile{OutputLimit: 17}, "--write-bytes", "25")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) != 17 || !result.StdoutTruncated {
		t.Fatalf("bounded stdout: %#v", result)
	}
	want := sha256.Sum256([]byte(strings.Repeat("x", 25)))
	if result.StdoutHash != hex.EncodeToString(want[:]) {
		t.Fatalf("stdout hash: got %s want %x", result.StdoutHash, want)
	}
}

func TestSupervisorBoundsCombinedStdoutAndStderr(t *testing.T) {
	result, err := runFakeAgent(t, context.Background(), adapter.Profile{OutputLimit: 17}, "--write-bytes", "25", "--write-stderr-bytes", "31")
	if err != nil {
		t.Fatal(err)
	}
	if retained := len(result.Stdout) + len(result.Stderr); retained > 17 {
		t.Fatalf("combined output exceeded cap: stdout=%d stderr=%d", len(result.Stdout), len(result.Stderr))
	}
	stdoutHash := sha256.Sum256([]byte(strings.Repeat("x", 25)))
	stderrHash := sha256.Sum256([]byte(strings.Repeat("y", 31)))
	if result.StdoutHash != hex.EncodeToString(stdoutHash[:]) || result.StderrHash != hex.EncodeToString(stderrHash[:]) {
		t.Fatalf("full stream hashes: result=%#v", result)
	}
	if !result.StdoutTruncated && !result.StderrTruncated {
		t.Fatalf("combined output was not marked truncated: %#v", result)
	}
}

func TestSupervisorWorkingDirectory(t *testing.T) {
	work := t.TempDir()
	result, err := runFakeAgentInWorkspace(t, context.Background(), work, adapter.Profile{}, "--pwd")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(result.Stdout)) != want {
		t.Fatalf("working directory: got %q want %q", result.Stdout, want)
	}
}

func TestSupervisorTimeoutKillsDescendantProcessGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant.pid")
	result, err := runFakeAgent(t, context.Background(), adapter.Profile{Timeout: 300 * time.Millisecond}, "--spawn-descendant", "--marker", marker, "--sleep-ms", "5000")
	if err == nil || result.TimedOut == false {
		t.Fatalf("timeout: result=%#v err=%v", result, err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		value, readErr := os.ReadFile(marker)
		if readErr == nil {
			pid, parseErr := strconv.Atoi(string(value))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) || processIsZombie(pid) {
				return
			}
			if time.Since(deadline.Add(-time.Second)) < 20*time.Millisecond {
				t.Logf("descendant pid=%d still present; ps=%s", pid, processState(pid))
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	ps, _ := exec.Command("ps", "-o", "pid,ppid,pgid,stat,command", "-p", stringValueFromFile(marker)).CombinedOutput()
	t.Fatalf("descendant process survived timeout; result=%#v ps=%s", result, ps)
}

func stringValueFromFile(path string) string {
	value, _ := os.ReadFile(path)
	return strings.TrimSpace(string(value))
}

func TestSupervisorCancellationKillsProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1 * time.Second)
		cancel()
	}()
	result, err := runFakeAgent(t, ctx, adapter.Profile{}, "--sleep-ms", "5000")
	if err == nil || !result.Canceled {
		t.Fatalf("cancellation: result=%#v err=%v", result, err)
	}
}

func processIsZombie(pid int) bool {
	return strings.HasPrefix(processState(pid), "Z")
}

func processState(pid int) string {
	output, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return strings.TrimSpace(string(output))
}

func TestSupervisorMissingExecutableIsTooling(t *testing.T) {
	result, err := runFakeAgent(t, context.Background(), adapter.Profile{Executable: filepath.Join(t.TempDir(), "missing")}, "--exit", "0")
	if err == nil || result.ExitCode != -1 {
		t.Fatalf("missing executable: result=%#v err=%v", result, err)
	}
}

func TestSupervisorNaturalExitIsNotRetroactivelyCanceled(t *testing.T) {
	result, err := runFakeAgent(t, lateCanceledContext{}, adapter.Profile{}, "--exit", "0")
	if err != nil || result.Canceled || result.ExitCode != 0 {
		t.Fatalf("natural exit: result=%#v err=%v", result, err)
	}
}

type lateCanceledContext struct{ context.Context }

func (lateCanceledContext) Done() <-chan struct{} { return nil }
func (lateCanceledContext) Err() error            { return context.Canceled }

func TestSupervisorRequiresActiveLease(t *testing.T) {
	work := t.TempDir()
	request := process.Request{
		Workspace: work,
		Profile: adapter.Profile{
			ID: "agent", Executable: "/bin/true", Family: "local", Capabilities: []string{"writer"},
			Timeout: time.Second, OutputLimit: 128,
		},
	}
	if _, err := (&process.Supervisor{}).Run(context.Background(), request); err == nil {
		t.Fatal("nil lease accepted")
	}
	manager := workspace.NewLeaseManager(t.TempDir())
	lease, err := manager.Acquire("test", work)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	request.Lease = lease
	if _, err := (&process.Supervisor{}).Run(context.Background(), request); err == nil {
		t.Fatal("released lease accepted")
	}
	request.Lease = &workspace.Lease{}
	if _, err := (&process.Supervisor{}).Run(context.Background(), request); err == nil {
		t.Fatal("forged zero-value lease accepted")
	}
}

func runFakeAgentInWorkspace(t *testing.T, ctx context.Context, work string, profile adapter.Profile, args ...string) (process.Result, error) {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	binary := filepath.Join(t.TempDir(), "fake-agent")
	build := exec.Command("go", "build", "-o", binary, "./testdata/fake-agent")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake agent: %v\n%s", err, output)
	}
	manager := workspace.NewLeaseManager(t.TempDir())
	lease, err := manager.Acquire("test", work)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	profile.ID = "fake-agent"
	profile.Executable = binary
	if profile.Family == "" {
		profile.Family = "test"
	}
	if len(profile.Capabilities) == 0 {
		profile.Capabilities = []string{"writer"}
	}
	if profile.Timeout == 0 {
		profile.Timeout = 2 * time.Second
	}
	if profile.OutputLimit == 0 {
		profile.OutputLimit = 4096
	}
	return (&process.Supervisor{}).Run(ctx, process.Request{Workspace: work, Lease: lease, Profile: profile, Args: args})
}

func createWorkCasePair(t *testing.T) (lower, upper string, distinct bool) {
	t.Helper()
	parent := t.TempDir()
	lower = filepath.Join(parent, "work")
	upper = filepath.Join(parent, "WORK")
	if err := os.Mkdir(lower, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(upper, 0700); err != nil {
		if os.IsExist(err) {
			return lower, upper, false
		}
		t.Fatal(err)
	}
	return lower, upper, true
}

func TestSupervisorFilesystemSemanticsLeaseIdentity(t *testing.T) {
	lower, upper, distinct := createWorkCasePair(t)
	root := filepath.Clean(filepath.Join("..", ".."))
	binary := filepath.Join(t.TempDir(), "fake-agent")
	build := exec.Command("go", "build", "-o", binary, "./testdata/fake-agent")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake agent: %v\n%s", err, output)
	}
	manager := workspace.NewLeaseManager(t.TempDir())
	lease, err := manager.Acquire("test", lower)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	profile := adapter.Profile{
		ID: "fake-agent", Executable: binary, Family: "test", Capabilities: []string{"writer"},
		Timeout: 2 * time.Second, OutputLimit: 4096, EnvAllowlist: []string{"PATH"},
	}
	_, err = (&process.Supervisor{}).Run(context.Background(), process.Request{
		Workspace: upper,
		Lease:     lease,
		Profile:   profile,
		Args:      []string{"--exit", "0"},
	})
	if distinct {
		if failure.ExitCodeFor(err) != 60 {
			t.Fatalf("distinct workspace lease mismatch: got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("case alias workspace accepted: %v", err)
	}
}

func TestSupervisorRejectsReplacedWorkspaceLease(t *testing.T) {
	parent := t.TempDir()
	work := filepath.Join(parent, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	binary := filepath.Join(t.TempDir(), "fake-agent")
	build := exec.Command("go", "build", "-o", binary, "./testdata/fake-agent")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake agent: %v\n%s", err, output)
	}
	manager := workspace.NewLeaseManager(t.TempDir())
	lease, err := manager.Acquire("test", work)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	renamed := filepath.Join(parent, "work-moved")
	if err := os.Rename(work, renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	profile := adapter.Profile{
		ID: "fake-agent", Executable: binary, Family: "test", Capabilities: []string{"writer"},
		Timeout: 2 * time.Second, OutputLimit: 4096, EnvAllowlist: []string{"PATH"},
	}
	_, err = (&process.Supervisor{}).Run(context.Background(), process.Request{
		Workspace: work,
		Lease:     lease,
		Profile:   profile,
		Args:      []string{"--exit", "0"},
	})
	if err == nil {
		t.Fatal("supervisor launched with replaced workspace lease")
	}
}

func TestSupervisorRequiresLease(t *testing.T) {
	_, err := (&process.Supervisor{}).Run(context.Background(), process.Request{
		Workspace: t.TempDir(),
		Profile: adapter.Profile{
			ID: "agent", Executable: "/bin/true", Family: "local", Capabilities: []string{"writer"},
			Timeout: 1, OutputLimit: 1,
		},
	})
	if err == nil {
		t.Fatal("missing lease accepted")
	}
}
