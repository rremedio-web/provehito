package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectsOversizedArchiveBeforeRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archive := filepath.Join(dir, "large.zip")
	if err := os.WriteFile(archive, make([]byte, 32*1024*1024+1), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	cmd := exec.Command("go", "run", ".", archive)
	cmd.Dir = filepath.Join(repoRoot(t), "internal/releasecheck/cmd/releasecheck")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure for oversized archive")
	}
	text := string(output)
	if !strings.Contains(text, "32 MiB") {
		t.Fatalf("expected 32 MiB limit message, got: %s", text)
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
