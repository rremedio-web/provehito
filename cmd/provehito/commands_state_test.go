package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnknownCommandIsUsageFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"unknown"}, &out, &errOut)
	if code != 10 {
		t.Fatalf("got %d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
}

func TestInitRejectsStateInsideWorkspaceWithoutWriting(t *testing.T) {
	work := t.TempDir()
	state := filepath.Join(work, "state")
	code, result, _ := runJSON(t, "init", "--state", state, "--workspace", work)
	if code != 60 || result["class"] != "INTEGRITY" {
		t.Fatalf("%d %#v", code, result)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("state was written: %v", err)
	}
}

func TestInitEnforcesExactStateDirectoryModesAndRejectsSymlinks(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime")
	workspace := t.TempDir()
	if code := Run([]string{"init", "--state", state, "--workspace", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init: %d", code)
	}
	for _, name := range []string{"", "lanes", "evidence"} {
		path := state
		if name != "" {
			path = filepath.Join(state, name)
		}
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if code := Run([]string{"init", "--state", state, "--workspace", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("wrong modes should be corrected: %d", code)
	}
	for _, name := range []string{"", "lanes", "evidence"} {
		path := state
		if name != "" {
			path = filepath.Join(state, name)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s stat err=%v", path, err)
		}
		if info.Mode().Perm() != 0700 {
			t.Fatalf("%s mode=%v", path, info.Mode().Perm())
		}
	}

	if err := os.Remove(filepath.Join(state, "lanes")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideBefore, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(state, "lanes")); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"init", "--state", state, "--workspace", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 60 {
		t.Fatalf("symlink lanes: got %d", code)
	}
	if info, err := os.Stat(outside); err != nil {
		t.Fatalf("symlink target changed: err=%v", err)
	} else if info.Mode().Perm() != outsideBefore.Mode().Perm() {
		t.Fatalf("symlink target changed: before=%v after=%v", outsideBefore.Mode().Perm(), info.Mode().Perm())
	}
}

func TestInitAndLaneOpenRejectMissingOrFileWorkspaceWithoutWrites(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "missing-workspace")
	state := filepath.Join(parent, "state-missing")
	code, result, _ := runJSON(t, "init", "--state", state, "--workspace", missing)
	if code == 0 || result["ok"] == true {
		t.Fatalf("missing workspace accepted: %d %#v", code, result)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("missing workspace wrote state: %v", err)
	}

	fileWorkspace := filepath.Join(parent, "workspace-file")
	if err := os.WriteFile(fileWorkspace, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	state = filepath.Join(parent, "state-file")
	code, result, _ = runJSON(t, "init", "--state", state, "--workspace", fileWorkspace)
	if code == 0 || result["ok"] == true {
		t.Fatalf("file workspace accepted: %d %#v", code, result)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("file workspace wrote state: %v", err)
	}

	validState := filepath.Join(parent, "state-valid")
	validWorkspace := t.TempDir()
	if code := Run([]string{"init", "--state", validState, "--workspace", validWorkspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("valid init: %d", code)
	}
	args := completeOpenArgs(validState, "demo", validWorkspace)
	if err := os.RemoveAll(validWorkspace); err != nil {
		t.Fatal(err)
	}
	code, result, _ = runJSON(t, args...)
	if code == 0 || result["ok"] == true {
		t.Fatalf("lane open accepted missing workspace: %d %#v", code, result)
	}
	if _, err := os.Stat(filepath.Join(validState, "lanes", "demo.json")); !os.IsNotExist(err) {
		t.Fatalf("missing workspace wrote lane: %v", err)
	}
}

func TestLaneOpenRejectsEmptyRepeatableDispatchValuesWithoutWriting(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	workspace := t.TempDir()
	if code := Run([]string{"init", "--state", state, "--workspace", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init: %d", code)
	}
	for _, flagName := range []string{"allowed-paths", "required-checks"} {
		args := completeOpenArgs(state, "demo-"+strings.TrimSuffix(flagName, "-paths"), workspace)
		args = append(args, "--"+flagName, "")
		code, result, _ := runJSON(t, args...)
		if code != 10 || result["class"] != "USAGE_OR_SCHEMA" {
			t.Fatalf("empty %s: %d %#v", flagName, code, result)
		}
		entries, err := os.ReadDir(filepath.Join(state, "lanes"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("empty %s wrote lane: entries=%v err=%v", flagName, entries, err)
		}
	}
}

func TestLaneOpenRejectsTraversalAndIncompleteInputWithoutWriting(t *testing.T) {
	state := t.TempDir()
	if code := Run([]string{"init", "--state", filepath.Join(state, "runtime"), "--workspace", t.TempDir()}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init: %d", code)
	}
	root := filepath.Join(state, "runtime")
	code, result, _ := runJSON(t, "lane", "open", "--state", root, "--id", "../escape")
	if code != 10 || result["class"] != "USAGE_OR_SCHEMA" {
		t.Fatalf("traversal: %d %#v", code, result)
	}
	if _, err := os.Stat(filepath.Join(root, "lanes", "escape.json")); !os.IsNotExist(err) {
		t.Fatalf("traversal wrote a lane: %v", err)
	}

	code, result, _ = runJSON(t, "lane", "open", "--state", root, "--id", "demo")
	if code != 10 || result["class"] != "USAGE_OR_SCHEMA" {
		t.Fatalf("incomplete: %d %#v", code, result)
	}
	entries, err := os.ReadDir(filepath.Join(root, "lanes"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("incomplete input changed lanes: entries=%v err=%v", entries, err)
	}
}

func TestLaneOpenProducesActiveManifestAndStableJSON(t *testing.T) {
	stateParent := t.TempDir()
	state := filepath.Join(stateParent, "runtime")
	workspace := t.TempDir()
	if code := Run([]string{"init", "--state", state, "--workspace", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init: %d", code)
	}
	args := []string{"lane", "open", "--state", state, "--id", "demo-lane", "--workspace", workspace,
		"--source-control", "git", "--writer", "writer", "--adapter", "local", "--family", "family",
		"--cost-class", "economy", "--allowed-paths", "cmd", "--forbidden-paths", "secrets",
		"--non-goals", "deploy", "--required-checks", "test", "--review-policy", "one",
		"--max-seconds", "10", "--max-output-bytes", "100", "--max-memory-bytes", "200", "--json"}
	var first, second bytes.Buffer
	if code := Run(args, &first, &bytes.Buffer{}); code != 0 {
		t.Fatalf("open: %d %s", code, first.String())
	}
	if code := Run(args, &second, &bytes.Buffer{}); code == 0 {
		t.Fatal("duplicate open unexpectedly succeeded")
	}
	if first.String() == "" || !strings.HasPrefix(first.String(), `{"ok":true,"command":"lane open","class":"OK"`) {
		t.Fatalf("unstable/invalid JSON: %s", first.String())
	}
	var result map[string]any
	if err := json.Unmarshal(first.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["class"] != "OK" {
		t.Fatalf("result=%#v", result)
	}
	data, err := os.ReadFile(filepath.Join(state, "lanes", "demo-lane.json"))
	if err != nil || !bytes.Contains(data, []byte(`"state":"ACTIVE"`)) {
		t.Fatalf("manifest not active: err=%v data=%s", err, data)
	}
}

func TestLaneLifecycleUsesExpectedHashAndRejectsIllegalTransitions(t *testing.T) {
	stateParent := t.TempDir()
	state := filepath.Join(stateParent, "runtime")
	workspace := t.TempDir()
	if code := Run([]string{"init", "--state", state, "--workspace", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init: %d", code)
	}
	open := []string{"lane", "open", "--state", state, "--id", "demo", "--workspace", workspace,
		"--source-control", "git", "--writer", "writer", "--adapter", "local", "--family", "family",
		"--cost-class", "economy", "--allowed-paths", "cmd", "--forbidden-paths", "none",
		"--non-goals", "deploy", "--required-checks", "test", "--review-policy", "one",
		"--max-seconds", "1", "--max-output-bytes", "1", "--max-memory-bytes", "1"}
	code, opened, _ := runJSON(t, open...)
	if code != 0 {
		t.Fatalf("open: %d %#v", code, opened)
	}
	hash := opened["data"].(map[string]any)["hash"].(string)
	code, blocked, _ := runJSON(t, "lane", "block", "--state", state, "--id", "demo", "--expected-hash", strings.Repeat("0", 64))
	if code != 60 || blocked["class"] != "INTEGRITY" {
		t.Fatalf("stale block: %d %#v", code, blocked)
	}
	code, blocked, _ = runJSON(t, "lane", "block", "--state", state, "--id", "demo", "--expected-hash", hash)
	if code != 0 || blocked["data"].(map[string]any)["state"] != "BLOCKED" {
		t.Fatalf("block: %d %#v", code, blocked)
	}
	blockedHash := blocked["data"].(map[string]any)["hash"].(string)
	code, resumed, _ := runJSON(t, "lane", "resume", "--state", state, "--id", "demo", "--expected-hash", blockedHash)
	if code != 0 || resumed["data"].(map[string]any)["state"] != "ACTIVE" {
		t.Fatalf("resume: %d %#v", code, resumed)
	}
	activeHash := resumed["data"].(map[string]any)["hash"].(string)
	code, illegal, _ := runJSON(t, "lane", "resume", "--state", state, "--id", "demo", "--expected-hash", activeHash)
	if code != 20 || illegal["class"] != "POLICY_OR_TRANSITION" {
		t.Fatalf("illegal resume: %d %#v", code, illegal)
	}
}

func TestDoctorIsReadOnlyAndHumanOutputStartsWithResult(t *testing.T) {
	stateParent := t.TempDir()
	state := filepath.Join(stateParent, "runtime")
	workspace := t.TempDir()
	if code := Run([]string{"init", "--state", state, "--workspace", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init: %d", code)
	}
	before, err := snapshotTree(state)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Run([]string{"doctor", "--state", state, "--workspace", workspace}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("doctor: %d %s", code, out.String())
	}
	if !strings.HasPrefix(out.String(), "RESULT: OK doctor\n") {
		t.Fatalf("human output=%q", out.String())
	}
	after, err := snapshotTree(state)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("doctor mutated state: before=%s after=%s", before, after)
	}
}

func snapshotTree(root string) (string, error) {
	var records []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		contentHash := ""
		if !info.IsDir() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			contentHash = fmt.Sprintf("%x", sha256.Sum256(content))
		}
		records = append(records, fmt.Sprintf("%s|%04o|%d|%s", rel, info.Mode().Perm(), info.Size(), contentHash))
		return nil
	})
	return strings.Join(records, "\n"), err
}

func completeOpenArgs(state, id, workspace string) []string {
	return []string{"lane", "open", "--state", state, "--id", id, "--workspace", workspace,
		"--source-control", "git", "--writer", "writer", "--adapter", "local", "--family", "family",
		"--cost-class", "economy", "--allowed-paths", "cmd", "--forbidden-paths", "none",
		"--non-goals", "deploy", "--required-checks", "test", "--review-policy", "one",
		"--max-seconds", "1", "--max-output-bytes", "1", "--max-memory-bytes", "1"}
}

func runJSON(t *testing.T, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(append(args, "--json"), &out, &errOut)
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output=%q stderr=%q: %v", out.String(), errOut.String(), err)
	}
	return code, result, errOut.String()
}
