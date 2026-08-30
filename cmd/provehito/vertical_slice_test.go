package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/process"
	"github.com/provehito-project/provehito/core/workspace"
)

func TestVerticalSlice(t *testing.T) {
	repo := newCleanGitFixture(t)
	state := t.TempDir()
	mustCLI(t, state, "init", "--workspace", repo)
	opened := mustCLI(t, state, "lane", "open", "--id", "demo", "--workspace", repo, "--writer", "writer-1", "--family", "family-a", "--seat-id", "writer-seat", "--source-control", "git", "--adapter", "local", "--cost-class", "economy", "--allowed-paths", "cmd", "--forbidden-paths", "none", "--non-goals", "deploy", "--required-checks", "fixture-check", "--review-policy", "independent", "--max-seconds", "5", "--max-output-bytes", "4096", "--max-memory-bytes", "0")
	profile := fakeProfile(t)
	run := mustCLI(t, state, "agent", "run", "--lane", "demo", "--profile", profile, "--profile-id", "local", "--family", "family-a", "--seat-id", "writer-seat", "--cost-class", "economy", "--capability", "writer", "--timeout", "5s", "--output-bytes", "4096")
	if run.String("receipt") == "" {
		t.Fatal("agent run did not return a receipt")
	}
	frozen := mustCLI(t, state, "freeze", "--lane", "demo", "--expected-hash", opened.String("hash"), "--base", "HEAD~1")
	receipt := mustCLI(t, state, "evidence", "add", "--lane", "demo", "--method", "fixture-check", "--result", "pass")
	mustCLI(t, state, "evidence", "verify", "--ref", receipt.String("ref"))
	mustCLI(t, state, "review", "record", "--lane", "demo", "--reviewer", "reviewer-1", "--family", "family-b", "--seat-id", "reviewer-seat", "--verdict", "approve", "--fingerprint", frozen.String("candidate_hash"))
	mustCLI(t, state, "ready", "--lane", "demo")
	mustCLI(t, state, "close", "--lane", "demo")
}

func TestReviewReusesTheExactFrozenBase(t *testing.T) {
	repo := newCleanGitFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "third.txt"), []byte("three\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", "third.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "commit", "-qm", "third").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	state := t.TempDir()
	mustCLI(t, state, "init", "--workspace", repo)
	opened := mustCLI(t, state, "lane", "open", "--id", "demo", "--workspace", repo, "--writer", "writer-1", "--family", "family-a", "--seat-id", "writer-seat", "--source-control", "git", "--adapter", "local", "--cost-class", "economy", "--allowed-paths", "cmd", "--forbidden-paths", "none", "--non-goals", "deploy", "--required-checks", "fixture-check", "--review-policy", "independent", "--max-seconds", "5", "--max-output-bytes", "4096", "--max-memory-bytes", "0")
	frozen := mustCLI(t, state, "freeze", "--lane", "demo", "--expected-hash", opened.String("hash"), "--base", "HEAD~2")
	receipt := mustCLI(t, state, "evidence", "add", "--lane", "demo", "--method", "fixture-check", "--result", "pass")
	mustCLI(t, state, "evidence", "verify", "--ref", receipt.String("ref"))
	mustCLI(t, state, "review", "record", "--lane", "demo", "--reviewer", "reviewer-1", "--family", "family-b", "--seat-id", "reviewer-seat", "--verdict", "PASS", "--fingerprint", frozen.String("candidate_hash"))
	mustCLI(t, state, "ready", "--lane", "demo")
}

func TestVerticalSliceFailureClasses(t *testing.T) {
	t.Run("dirty freeze", func(t *testing.T) {
		repo, state, opened := setupLane(t)
		if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertCLIClass(t, state, 30, "WORKSPACE_DRIFT", "freeze", "--lane", "demo", "--expected-hash", opened.String("hash"))
	})
	t.Run("same family review", func(t *testing.T) {
		_, state, frozen, receipt := setupFrozen(t)
		assertCLIClass(t, state, 20, "POLICY_OR_TRANSITION", "review", "record", "--lane", "demo", "--reviewer", "reviewer", "--family", "family-a", "--seat-id", "reviewer-seat", "--verdict", "PASS", "--fingerprint", frozen.String("candidate_hash"), "--source", "approved")
		_ = receipt
	})
	t.Run("same seat review", func(t *testing.T) {
		_, state, frozen, _ := setupFrozen(t)
		assertCLIClass(t, state, 20, "POLICY_OR_TRANSITION", "review", "record", "--lane", "demo", "--reviewer", "reviewer", "--family", "family-b", "--seat-id", "writer-seat", "--verdict", "PASS", "--fingerprint", frozen.String("candidate_hash"))
	})
	t.Run("prose is not approval", func(t *testing.T) {
		_, state, frozen, _ := setupFrozen(t)
		assertCLIClass(t, state, 20, "POLICY_OR_TRANSITION", "review", "record", "--lane", "demo", "--reviewer", "reviewer", "--family", "family-b", "--seat-id", "reviewer-seat", "--source", "APPROVED; ship it", "--fingerprint", frozen.String("candidate_hash"))
	})
	t.Run("unrequired evidence cannot satisfy a required check", func(t *testing.T) {
		_, state, opened := setupLane(t)
		frozen := mustCLI(t, state, "freeze", "--lane", "demo", "--expected-hash", opened.String("hash"), "--base", "HEAD~1")
		mustCLI(t, state, "evidence", "add", "--lane", "demo", "--method", "different-check", "--result", "pass")
		assertCLIClass(t, state, 50, "CANDIDATE_OR_REVIEW", "review", "record", "--lane", "demo", "--reviewer", "reviewer", "--family", "family-b", "--seat-id", "reviewer-seat", "--verdict", "PASS", "--fingerprint", frozen.String("candidate_hash"))
	})
	t.Run("failed evidence cannot satisfy a required check", func(t *testing.T) {
		_, state, opened := setupLane(t)
		frozen := mustCLI(t, state, "freeze", "--lane", "demo", "--expected-hash", opened.String("hash"), "--base", "HEAD~1")
		mustCLI(t, state, "evidence", "add", "--lane", "demo", "--method", "fixture-check", "--result", "fail")
		assertCLIClass(t, state, 50, "CANDIDATE_OR_REVIEW", "review", "record", "--lane", "demo", "--reviewer", "reviewer", "--family", "family-b", "--seat-id", "reviewer-seat", "--verdict", "PASS", "--fingerprint", frozen.String("candidate_hash"))
	})
	t.Run("candidate drift after review", func(t *testing.T) {
		repo, state, frozen, _ := setupFrozen(t)
		mustCLI(t, state, "review", "record", "--lane", "demo", "--reviewer", "reviewer", "--family", "family-b", "--seat-id", "reviewer-seat", "--verdict", "PASS", "--fingerprint", frozen.String("candidate_hash"))
		if err := os.WriteFile(filepath.Join(repo, "drift.txt"), []byte("drift\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", repo, "add", "drift.txt").CombinedOutput(); err != nil {
			t.Fatalf("git add: %v %s", err, out)
		}
		if out, err := exec.Command("git", "-C", repo, "commit", "-qm", "drift").CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v %s", err, out)
		}
		assertCLIClass(t, state, 50, "CANDIDATE_OR_REVIEW", "ready", "--lane", "demo")
	})
	t.Run("tampered receipt", func(t *testing.T) {
		_, state, _, receipt := setupFrozen(t)
		path := filepath.Join(state, "evidence", "sha256", receipt.String("ref")[:2], receipt.String("ref")+".json")
		if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertCLIClass(t, state, 60, "INTEGRITY", "evidence", "verify", "--ref", receipt.String("ref"))
	})
	t.Run("missing executable", func(t *testing.T) {
		_, state, _ := setupLane(t)
		assertCLIClass(t, state, 40, "TOOLING_OR_ADAPTER", "agent", "run", "--lane", "demo", "--profile", filepath.Join(t.TempDir(), "missing"), "--profile-id", "local", "--family", "family-a", "--seat-id", "writer-seat", "--cost-class", "economy", "--capability", "writer", "--timeout", "1s", "--output-bytes", "32")
	})
	t.Run("timeout", func(t *testing.T) {
		_, state, _ := setupLane(t)
		restore := injectRunner(memoryRunner{})
		defer restore()
		assertCLIClass(t, state, 40, "TOOLING_OR_ADAPTER", "agent", "run", "--lane", "demo", "--profile", "/bin/true", "--profile-id", "local", "--family", "family-a", "--seat-id", "writer-seat", "--cost-class", "economy", "--capability", "writer", "--timeout", "1ms", "--output-bytes", "32", "--arg", "--sleep-ms", "--arg", "100")
	})
	t.Run("profile exceeds dispatch limits", func(t *testing.T) {
		_, state, _ := setupLane(t)
		assertCLIClass(t, state, 20, "POLICY_OR_TRANSITION", "agent", "run", "--lane", "demo", "--profile", fakeProfile(t), "--profile-id", "local", "--family", "family-a", "--seat-id", "writer-seat", "--cost-class", "economy", "--capability", "writer", "--timeout", "6s", "--output-bytes", "4097")
	})
	t.Run("second writer", func(t *testing.T) {
		repo, state, _ := setupLane(t)
		lease, err := workspace.NewLeaseManager(state).Acquire("other", repo)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Release()
		assertCLIClass(t, state, 70, "CONCURRENCY", "agent", "run", "--lane", "demo", "--profile", fakeProfile(t), "--profile-id", "local", "--family", "family-a", "--seat-id", "writer-seat", "--cost-class", "economy", "--capability", "writer", "--timeout", "1s", "--output-bytes", "32")
	})
}

func TestJSONFailureIncludesOperationCorrection(t *testing.T) {
	_, state, frozen, _ := setupFrozen(t)
	code, result, _ := runJSON(t, "review", "record", "--lane", "demo", "--reviewer", "reviewer", "--family", "family-a", "--seat-id", "reviewer-seat", "--verdict", "PASS", "--fingerprint", frozen.String("candidate_hash"), "--state", state)
	if code != 20 || result["class"] != "POLICY_OR_TRANSITION" {
		t.Fatalf("review failure: %d %#v", code, result)
	}
	if result["correction"] != "set --family on review record to a value different from the dispatch family used by the writer" {
		t.Fatalf("correction: %#v", result["correction"])
	}
}

func setupLane(t *testing.T) (string, string, cliResult) {
	t.Helper()
	repo := newCleanGitFixture(t)
	state := t.TempDir()
	mustCLI(t, state, "init", "--workspace", repo)
	opened := mustCLI(t, state, "lane", "open", "--id", "demo", "--workspace", repo, "--writer", "writer-1", "--family", "family-a", "--seat-id", "writer-seat", "--source-control", "git", "--adapter", "local", "--cost-class", "economy", "--allowed-paths", "cmd", "--forbidden-paths", "none", "--non-goals", "deploy", "--required-checks", "fixture-check", "--review-policy", "independent", "--max-seconds", "5", "--max-output-bytes", "4096", "--max-memory-bytes", "0")
	return repo, state, opened
}

func setupFrozen(t *testing.T) (string, string, cliResult, cliResult) {
	t.Helper()
	repo, state, opened := setupLane(t)
	frozen := mustCLI(t, state, "freeze", "--lane", "demo", "--expected-hash", opened.String("hash"), "--base", "HEAD~1")
	receipt := mustCLI(t, state, "evidence", "add", "--lane", "demo", "--method", "fixture-check", "--result", "pass")
	return repo, state, frozen, receipt
}

func assertCLIClass(t *testing.T, state string, code int, class string, args ...string) {
	t.Helper()
	args = append(args, "--state", state, "--json")
	var out bytes.Buffer
	got := Run(args, &out, &bytes.Buffer{})
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("%v output=%q: %v", args, out.String(), err)
	}
	if got != code || result["class"] != class {
		t.Fatalf("%v: code=%d class=%v want code=%d class=%s", args, got, result["class"], code, class)
	}
}

type cliResult struct{ Data map[string]any }

func (r cliResult) String(key string) string {
	value, _ := r.Data[key].(string)
	return value
}

func mustCLI(t *testing.T, state string, args ...string) cliResult {
	t.Helper()
	args = append(args, "--state", state, "--json")
	var out, errOut bytes.Buffer
	if code := Run(args, &out, &errOut); code != 0 {
		t.Fatalf("%v: code=%d stdout=%s stderr=%s", args, code, out.String(), errOut.String())
	}
	var envelope struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("%v output=%q: %v", args, out.String(), err)
	}
	if !envelope.OK {
		t.Fatalf("%v returned %#v", args, envelope)
	}
	return cliResult{Data: envelope.Data}
}

func newCleanGitFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "Provehito Test")
	if err := os.WriteFile(filepath.Join(repo, "fixture.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "fixture.txt")
	git("commit", "-qm", "initial")
	if err := os.WriteFile(filepath.Join(repo, "fixture.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "fixture.txt")
	git("commit", "-qm", "candidate")
	return repo
}

func fakeProfile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-agent")
	if out, err := exec.Command("go", "build", "-o", path, "../../testdata/fake-agent").CombinedOutput(); err != nil {
		t.Fatalf("build fake agent: %v %s", err, out)
	}
	return path
}

// memoryRunner is the in-memory process-runner adapter. Two adapters make
// the runner seam real; this one stays test-scoped and replaces a compiled
// fake agent per subtest. It honors the fake agent's --sleep-ms convention:
// a sleep beyond the profile timeout reports the supervisor's timeout
// failure without spawning a process.
type memoryRunner struct{}

func (memoryRunner) Run(ctx context.Context, request process.Request) (process.Result, error) {
	args := append(append([]string(nil), request.Profile.Args...), request.Args...)
	sleep := time.Duration(0)
	for i, arg := range args {
		if arg == "--sleep-ms" && i+1 < len(args) {
			if ms, err := strconv.Atoi(args[i+1]); err == nil {
				sleep = time.Duration(ms) * time.Millisecond
			}
		}
	}
	if sleep > request.Profile.Timeout {
		return process.Result{
			ExitCode: -1, TimedOut: true,
			StdoutHash: process.EmptyStreamHash(), StderrHash: process.EmptyStreamHash(),
		}, failure.Wrap(failure.ToolingOrAdapter, "agent process timeout", context.DeadlineExceeded)
	}
	hash := sha256.Sum256([]byte("ok"))
	streamHash := hex.EncodeToString(hash[:])
	return process.Result{
		ExitCode: 0, Stdout: []byte("ok"),
		StdoutHash: streamHash, StderrHash: process.EmptyStreamHash(),
	}, nil
}

func injectRunner(runner process.Runner) func() {
	previous := agentRunner
	agentRunner = runner
	return func() { agentRunner = previous }
}
