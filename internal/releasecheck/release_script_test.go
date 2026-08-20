package releasecheck_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func worktreeClean(repo string) bool {
	out, err := exec.Command("git", "-C", repo, "-c", "advice.statusHints=false", "-c", "core.quotepath=false", "status", "--porcelain=v1", "-uall").CombinedOutput()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) == 0
}

func nonexistentReleaseOut(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "release-out")
}

func TestReleaseScriptTwoBuildEquality(t *testing.T) {
	repo := repoRoot(t)
	if !worktreeClean(repo) {
		t.Skip("worktree not clean")
	}

	out := nonexistentReleaseOut(t)
	cmd := exec.Command(filepath.Join(repo, "scripts/release.sh"), "--structural-only", out)
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("release.sh: %v\n%s", err, output)
	}
	receipt := filepath.Join(out, "receipt.json")
	data, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"archive_hash_build1"`) || !strings.Contains(text, `"archive_hash_build2"`) {
		t.Fatalf("receipt missing build hashes: %s", text)
	}
	if !worktreeClean(repo) {
		t.Fatalf("release left repository dirty")
	}
}

func TestReleaseScriptDirtyWorktreeStops(t *testing.T) {
	repo := repoRoot(t)
	dirty := filepath.Join(repo, ".release-dirty-probe")
	if err := os.WriteFile(dirty, []byte("probe\n"), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(dirty) })

	out := nonexistentReleaseOut(t)
	cmd := exec.Command(filepath.Join(repo, "scripts/release.sh"), "--structural-only", out)
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected dirty worktree failure, got success: %s", output)
	}
	if !strings.Contains(string(output), "dirty worktree") {
		t.Fatalf("expected dirty worktree message, got: %s", output)
	}
}

func TestReleaseScriptOutputDirMustNotExist(t *testing.T) {
	repo := repoRoot(t)
	if !worktreeClean(repo) {
		t.Skip("worktree not clean")
	}
	parent := t.TempDir()
	existing := filepath.Join(parent, "release-out")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cmd := exec.Command(filepath.Join(repo, "scripts/release.sh"), "--structural-only", existing)
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected existing output dir failure, got success: %s", output)
	}
	if !strings.Contains(string(output), "output directory already exists") {
		t.Fatalf("expected output exists message, got: %s", output)
	}
}

func TestReleaseScriptFsmonitorConfigDoesNotExecute(t *testing.T) {
	repo := repoRoot(t)

	cloneRoot := filepath.Join(t.TempDir(), "clone")
	cloneCmd := exec.Command("git", "clone", "--no-hardlinks", repo, cloneRoot)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}

	root := t.TempDir()
	marker := filepath.Join(root, "fsmonitor-invoked")
	script := filepath.Join(root, "fsmonitor-hook")
	contents := "#!/bin/sh\nprintf invoked > " + marker + "\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	configCmd := exec.Command("git", "-C", cloneRoot, "config", "core.fsmonitor", script)
	if out, err := configCmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}

	out := nonexistentReleaseOut(t)
	cmd := exec.Command(filepath.Join(cloneRoot, "scripts/release.sh"), "--structural-only", out)
	cmd.Dir = cloneRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("release.sh: %v\n%s", err, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("fsmonitor hook executed and created marker: %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}
