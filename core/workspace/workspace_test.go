package workspace_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/workspace"
)

func TestStateRootCannotOverlapWorkspace(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	work := filepath.Join(state, "work")
	if err := workspace.ValidateSeparation(state, work); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("expected integrity failure, got %v", err)
	}
}

func TestStateRootCannotAliasWorkspaceThroughSymlink(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	work := filepath.Join(root, "work")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	stateAlias := filepath.Join(root, "state-alias")
	if err := os.Symlink(state, stateAlias); err != nil {
		t.Fatal(err)
	}
	if err := workspace.ValidateSeparation(stateAlias, state); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("expected integrity failure for symlink alias, got %v", err)
	}
}

func TestResolveContainedRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside")
	outside := t.TempDir()
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.ResolveContained(root, filepath.Join("inside", "child")); err != nil {
		t.Fatalf("contained path: %v", err)
	}
	if _, err := workspace.ResolveContained(root, filepath.Join("..", filepath.Base(outside))); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("traversal: got %v", err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.ResolveContained(root, filepath.Join("escape", "child")); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("symlink escape: got %v", err)
	}
}

func TestSecondWriterFails(t *testing.T) {
	work := t.TempDir()
	manager := workspace.NewLeaseManager(t.TempDir())
	first, err := manager.Acquire("lane-a", work)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if _, err := manager.Acquire("lane-b", work); failure.ExitCodeFor(err) != 70 {
		t.Fatalf("expected concurrency failure, got %v", err)
	}
}

func TestLeaseActiveWorkspaceRejectsReplacedDirectory(t *testing.T) {
	parent := t.TempDir()
	work := filepath.Join(parent, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewLeaseManager(t.TempDir())
	lease, err := manager.Acquire("lane-a", work)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if path, ok := lease.ActiveWorkspace(); !ok || path == "" {
		t.Fatalf("acquired lease inactive: path=%q ok=%v", path, ok)
	}
	renamed := filepath.Join(parent, "work-moved")
	if err := os.Rename(work, renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if path, ok := lease.ActiveWorkspace(); ok || path != "" {
		t.Fatalf("replaced directory still active: path=%q ok=%v", path, ok)
	}
}

func TestLeaseActiveWorkspaceRejectsNilReleasedAndForged(t *testing.T) {
	var nilLease *workspace.Lease
	if path, ok := nilLease.ActiveWorkspace(); ok || path != "" {
		t.Fatalf("nil lease active: path=%q ok=%v", path, ok)
	}
	work := t.TempDir()
	manager := workspace.NewLeaseManager(t.TempDir())
	lease, err := manager.Acquire("lane-a", work)
	if err != nil {
		t.Fatal(err)
	}
	if path, ok := lease.ActiveWorkspace(); !ok || path == "" {
		t.Fatalf("acquired lease inactive: path=%q ok=%v", path, ok)
	}
	entries, err := os.ReadDir(manager.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".lock" {
			if err := os.Remove(filepath.Join(manager.Root, entry.Name())); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if path, ok := lease.ActiveWorkspace(); ok || path != "" {
		t.Fatalf("lease with missing lock active: path=%q ok=%v", path, ok)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if path, ok := lease.ActiveWorkspace(); ok || path != "" {
		t.Fatalf("released lease active: path=%q ok=%v", path, ok)
	}
	if path, ok := (&workspace.Lease{}).ActiveWorkspace(); ok || path != "" {
		t.Fatalf("forged lease active: path=%q ok=%v", path, ok)
	}
}

func TestSecondWriterAliasFails(t *testing.T) {
	work := t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(work, alias); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewLeaseManager(t.TempDir())
	first, err := manager.Acquire("lane-a", work)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if _, err := manager.Acquire("lane-b", alias); failure.ExitCodeFor(err) != 70 {
		t.Fatalf("expected alias concurrency failure, got %v", err)
	}
}

func TestLeaseAPIsRejectOverlappingStateAndWorkspaceBeforeStateAccess(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		stateRoot string
		workspace string
		checkRoot string
	}{
		{name: "equal", stateRoot: state, workspace: state, checkRoot: state},
		{name: "state ancestor", stateRoot: state, workspace: filepath.Join(state, "work"), checkRoot: state},
		{name: "workspace ancestor", stateRoot: filepath.Join(state, "nested-state"), workspace: state, checkRoot: state},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager := workspace.NewLeaseManager(tc.stateRoot)
			lease, err := manager.Acquire("lane-a", tc.workspace)
			if err == nil {
				_ = lease.Release()
			}
			if failure.ExitCodeFor(err) != 60 {
				t.Fatalf("Acquire: expected integrity failure, got %v", err)
			}
			if err := manager.DetectAbandoned(tc.workspace); failure.ExitCodeFor(err) != 60 {
				t.Fatalf("DetectAbandoned: expected integrity failure, got %v", err)
			}
			entries, err := os.ReadDir(tc.checkRoot)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("overlap created state files: %v", entries)
			}
		})
	}

	alias := filepath.Join(root, "state-alias")
	if err := os.Symlink(state, alias); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewLeaseManager(alias)
	lease, err := manager.Acquire("lane-a", state)
	if err == nil {
		_ = lease.Release()
	}
	if failure.ExitCodeFor(err) != 60 {
		t.Fatalf("Acquire alias: expected integrity failure, got %v", err)
	}
	if err := manager.DetectAbandoned(state); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("DetectAbandoned alias: expected integrity failure, got %v", err)
	}
	entries, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("alias overlap created state files: %v", entries)
	}
}

func TestLeaseProcessStartTimeIsStableAcrossSequentialLeases(t *testing.T) {
	manager := workspace.NewLeaseManager(t.TempDir())
	work := t.TempDir()
	first, err := manager.Acquire("lane-a", work)
	if err != nil {
		t.Fatal(err)
	}
	firstStart := first.ProcessStartTime
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	second, err := manager.Acquire("lane-b", work)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if !second.ProcessStartTime.Equal(firstStart) || second.ProcessStartTime.After(second.AcquiredAt) {
		t.Fatalf("process start time: first=%s second=%s acquired=%s", firstStart, second.ProcessStartTime, second.AcquiredAt)
	}
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

func TestFilesystemSemanticsSamePath(t *testing.T) {
	lower, upper, distinct := createWorkCasePair(t)
	same, err := workspace.SamePath(lower, upper)
	if err != nil {
		t.Fatal(err)
	}
	if distinct && same {
		t.Fatalf("distinct case siblings must not compare equal: %q %q", lower, upper)
	}
	if !distinct && !same {
		t.Fatalf("case aliases must compare equal on this filesystem: %q %q", lower, upper)
	}
	if sameLower, err := workspace.SamePath(lower, lower); err != nil || !sameLower {
		t.Fatalf("path self-identity: same=%v err=%v", sameLower, err)
	}
}

func TestFilesystemSemanticsResolveContainedCaseChild(t *testing.T) {
	lower, upper, distinct := createWorkCasePair(t)
	child := filepath.Join(upper, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := workspace.ResolveContained(lower, child)
	if distinct {
		if failure.ExitCodeFor(err) != 60 {
			t.Fatalf("distinct siblings: expected integrity failure, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("case alias child containment: %v", err)
	}
}

func TestFilesystemSemanticsContainmentRejectsCaseSiblings(t *testing.T) {
	lower, upper, distinct := createWorkCasePair(t)
	_, err := workspace.ResolveContained(lower, upper)
	if distinct {
		if failure.ExitCodeFor(err) != 60 {
			t.Fatalf("distinct siblings: expected integrity failure, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("case alias containment: %v", err)
	}
}

func TestFilesystemSemanticsLeaseIdentity(t *testing.T) {
	lower, upper, distinct := createWorkCasePair(t)
	manager := workspace.NewLeaseManager(t.TempDir())
	first, err := manager.Acquire("lane-a", lower)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := manager.Acquire("lane-b", upper)
	if distinct {
		if err != nil {
			t.Fatalf("distinct workspaces should both acquire leases: %v", err)
		}
		if err := second.Release(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if failure.ExitCodeFor(err) != 70 {
		t.Fatalf("case aliases should share one lease: got %v", err)
	}
}

func TestDurableLeaseMetadataModeReleaseAndReacquire(t *testing.T) {
	state := t.TempDir()
	work := t.TempDir()
	manager := workspace.NewLeaseManager(state)
	lease, err := manager.Acquire("lane-a", work)
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(state, "*.lease"))
	if err != nil || len(files) != 1 {
		t.Fatalf("lease files: files=%v err=%v", files, err)
	}
	info, err := os.Stat(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("lease mode: got %04o want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	assertJSONString(t, fields, "lane_id", lease.LaneID)
	assertJSONInt(t, fields, "pid", lease.PID)
	assertJSONString(t, fields, "process_start_time", lease.ProcessStartTime.Format(time.RFC3339Nano))
	assertJSONString(t, fields, "workspace_identity", lease.WorkspaceIdentity)
	assertJSONString(t, fields, "acquired_at", lease.AcquiredAt.Format(time.RFC3339Nano))
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatalf("lease record after release: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("repeated release: %v", err)
	}
	reacquired, err := manager.Acquire("lane-b", work)
	if err != nil {
		t.Fatal(err)
	}
	if err := reacquired.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseSymlinkFailureStillUnlocksAndPreservesTarget(t *testing.T) {
	state := t.TempDir()
	work := t.TempDir()
	manager := workspace.NewLeaseManager(state)
	lease, err := manager.Acquire("lane-a", work)
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(state, "*.lease"))
	if err != nil || len(files) != 1 {
		t.Fatalf("lease files: files=%v err=%v", files, err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("preserve"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, files[0]); err != nil {
		t.Fatal(err)
	}
	if failure.ExitCodeFor(lease.Release()) != 60 {
		t.Fatalf("expected integrity failure from replaced lease record")
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "preserve" {
		t.Fatalf("symlink target changed: %q", contents)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	reacquired, err := manager.Acquire("lane-b", work)
	if err != nil {
		t.Fatalf("OS lock leaked after failed release: %v", err)
	}
	if err := reacquired.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveSecondWriterFailsUntilChildReleases(t *testing.T) {
	work := t.TempDir()
	state := t.TempDir()
	ready := filepath.Join(t.TempDir(), "ready")
	release := filepath.Join(t.TempDir(), "release")
	cmd := exec.Command(os.Args[0], "-test.run", "TestLeaseChildProcess")
	cmd.Env = append(os.Environ(),
		"PROVEHITO_LEASE_CHILD=1",
		"PROVEHITO_LEASE_STATE="+state,
		"PROVEHITO_LEASE_WORK="+work,
		"PROVEHITO_LEASE_READY="+ready,
		"PROVEHITO_LEASE_RELEASE="+release,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, ready)
	manager := workspace.NewLeaseManager(state)
	_, acquireErr := manager.Acquire("lane-b", work)
	if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child release: %v", err)
	}
	if failure.ExitCodeFor(acquireErr) != 70 {
		t.Fatalf("live second writer: expected concurrency failure, got %v", acquireErr)
	}
	reacquired, err := manager.Acquire("lane-c", work)
	if err != nil {
		t.Fatalf("reacquire after child release: %v", err)
	}
	if err := reacquired.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAbandonedDurableLeaseBlocksReuse(t *testing.T) {
	work := t.TempDir()
	state := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run", "TestLeaseChildProcess")
	cmd.Env = append(os.Environ(),
		"PROVEHITO_LEASE_CHILD=1",
		"PROVEHITO_LEASE_STATE="+state,
		"PROVEHITO_LEASE_WORK="+work,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child lease: %v\n%s", err, output)
	}
	manager := workspace.NewLeaseManager(state)
	if err := manager.DetectAbandoned(work); failure.ExitCodeFor(err) != 70 {
		t.Fatalf("expected abandoned concurrency failure, got %v", err)
	}
	if _, err := manager.Acquire("lane-b", work); failure.ExitCodeFor(err) != 70 {
		t.Fatalf("expected reuse to remain blocked, got %v", err)
	}
}

func TestLeaseChildProcess(t *testing.T) {
	if os.Getenv("PROVEHITO_LEASE_CHILD") != "1" {
		return
	}
	manager := workspace.NewLeaseManager(os.Getenv("PROVEHITO_LEASE_STATE"))
	lease, err := manager.Acquire("lane-a", os.Getenv("PROVEHITO_LEASE_WORK"))
	if err != nil {
		t.Fatalf("acquire child lease: %v", err)
	}
	readyPath := os.Getenv("PROVEHITO_LEASE_READY")
	if readyPath == "" {
		return
	}
	if err := os.WriteFile(readyPath, []byte("ready"), 0600); err != nil {
		t.Fatalf("signal child ready: %v", err)
	}
	for {
		if _, err := os.Stat(os.Getenv("PROVEHITO_LEASE_RELEASE")); err == nil {
			if err := lease.Release(); err != nil {
				t.Fatalf("release child lease: %v", err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func assertJSONString(t *testing.T, fields map[string]json.RawMessage, name, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(fields[name], &got); err != nil || got != want {
		t.Fatalf("%s: got %q err=%v want %q", name, got, err, want)
	}
}

func assertJSONInt(t *testing.T, fields map[string]json.RawMessage, name string, want int) {
	t.Helper()
	var got int
	if err := json.Unmarshal(fields[name], &got); err != nil || got != want {
		t.Fatalf("%s: got %d err=%v want %d", name, got, err, want)
	}
}
