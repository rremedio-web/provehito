package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/provehito-project/provehito/core/canon"
	"github.com/provehito-project/provehito/core/clock"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/manifest"
	"github.com/provehito-project/provehito/core/workspace"
)

type listFlag struct {
	values []string
	set    bool
}

func (f *listFlag) String() string { return strings.Join(f.values, ",") }
func (f *listFlag) Set(value string) error {
	f.set = true
	if value == "" {
		f.values = append(f.values, "")
		return nil
	}
	f.values = append(f.values, value)
	return nil
}

func commandFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})
	return fs
}

// ioDiscard avoids flag package diagnostics reaching the public output. Run
// emits the fixed result envelope instead.
type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func addStateFlags(fs *flag.FlagSet) (*string, *bool) {
	state := fs.String("state", "", "absolute state root")
	jsonOutput := fs.Bool("json", false, "emit JSON")
	return state, jsonOutput
}

func seatIDFlag(fs *flag.FlagSet) *string {
	return fs.String("seat-id", os.Getenv("PROVEHITO_SEAT_ID"), "independent process seat identity")
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return usageError("invalid flags")
	}
	if fs.NArg() != 0 {
		return usageError("unexpected positional argument")
	}
	return nil
}

func requireStateRoot(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return usageError("state root must be an absolute path")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return failure.New(failure.Integrity, "state root missing")
	}
	if err != nil {
		return failure.Wrap(failure.Integrity, "state root inspect", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return failure.New(failure.Integrity, "state root is not a directory")
	}
	if info.Mode().Perm() != 0700 {
		return failure.New(failure.Integrity, "state root mode")
	}
	return nil
}

var laneIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func lanePath(state, id string) (string, error) {
	if !laneIDPattern.MatchString(id) {
		return "", usageError("lane id must be a lowercase slug")
	}
	if err := requireStateRoot(state); err != nil {
		return "", err
	}
	lanes, err := lanesPath(state)
	if err != nil {
		return "", err
	}
	return filepath.Join(lanes, id+".json"), nil
}

func lanesPath(state string) (string, error) {
	if err := requireStateRoot(state); err != nil {
		return "", err
	}
	lanes := filepath.Join(state, "lanes")
	info, err := os.Lstat(lanes)
	if err != nil {
		return "", failure.Wrap(failure.Integrity, "lanes directory inspect", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", failure.New(failure.Integrity, "lanes directory")
	}
	if info.Mode().Perm() != 0700 {
		return "", failure.New(failure.Integrity, "lanes directory mode")
	}
	return lanes, nil
}

func requireWorkspace(path string) error {
	if path == "" {
		return usageError("workspace is required")
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return usageError("workspace must exist")
	}
	if err != nil {
		return failure.Wrap(failure.Integrity, "workspace inspect", err)
	}
	if !info.IsDir() {
		return usageError("workspace must be a directory")
	}
	return nil
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := commandFlags("init")
	state, jsonOutput := addStateFlags(fs)
	work := fs.String("workspace", "", "assigned workspace")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "init", *jsonOutput, nil, err)
	}
	if *state == "" || !filepath.IsAbs(*state) {
		return writeResult(stdout, stderr, "init", *jsonOutput, nil, usageError("state root must be an absolute path"))
	}
	if *work == "" {
		return writeResult(stdout, stderr, "init", *jsonOutput, nil, usageError("workspace is required"))
	}
	if err := requireWorkspace(*work); err != nil {
		return writeResult(stdout, stderr, "init", *jsonOutput, nil, err)
	}
	// This is intentionally before every mkdir or chmod: overlap failures must
	// leave no state-root residue.
	if err := workspace.ValidateSeparation(*state, *work); err != nil {
		return writeResult(stdout, stderr, "init", *jsonOutput, nil, err)
	}
	if info, err := os.Lstat(*state); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
		return writeResult(stdout, stderr, "init", *jsonOutput, nil, failure.New(failure.Integrity, "state root is not a directory"))
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return writeResult(stdout, stderr, "init", *jsonOutput, nil, failure.Wrap(failure.Integrity, "state root inspect", err))
	}
	childExists := make(map[string]bool, 2)
	for _, name := range []string{"lanes", "evidence"} {
		childPath := filepath.Join(*state, name)
		info, err := os.Lstat(childPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return writeResult(stdout, stderr, "init", *jsonOutput, nil, failure.Wrap(failure.Integrity, "state directory inspect", err))
		case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
			return writeResult(stdout, stderr, "init", *jsonOutput, nil, failure.New(failure.Integrity, "state directory is not a directory"))
		default:
			childExists[name] = true
		}
	}
	if err := os.MkdirAll(*state, 0700); err != nil {
		return writeResult(stdout, stderr, "init", *jsonOutput, nil, failure.Wrap(failure.Integrity, "state root create", err))
	}
	if err := os.Chmod(*state, 0700); err != nil {
		return writeResult(stdout, stderr, "init", *jsonOutput, nil, failure.Wrap(failure.Integrity, "state root mode", err))
	}
	for _, name := range []string{"lanes", "evidence"} {
		childPath := filepath.Join(*state, name)
		if !childExists[name] {
			if err := os.Mkdir(childPath, 0700); err != nil {
				return writeResult(stdout, stderr, "init", *jsonOutput, nil, failure.Wrap(failure.Integrity, "state directory create", err))
			}
		}
		if err := os.Chmod(childPath, 0700); err != nil {
			return writeResult(stdout, stderr, "init", *jsonOutput, nil, failure.Wrap(failure.Integrity, "state directory create", err))
		}
	}
	return writeResult(stdout, stderr, "init", *jsonOutput, map[string]any{"state": *state, "directories": []string{"lanes", "evidence"}}, nil)
}

func runLane(operation string, args []string, stdout, stderr io.Writer) int {
	switch operation {
	case "open":
		return runLaneOpen(args, stdout, stderr)
	case "list":
		return runLaneList(args, stdout, stderr)
	case "validate", "status":
		return runLaneRead(operation, args, stdout, stderr)
	case "block", "resume", "abandon", "incident":
		return runLaneTransition(operation, args, stdout, stderr)
	default:
		return writeResult(stdout, stderr, "lane "+operation, false, nil, usageError("unknown lane operation"))
	}
}

func runLaneOpen(args []string, stdout, stderr io.Writer) int {
	fs := commandFlags("lane open")
	state, jsonOutput := addStateFlags(fs)
	id := fs.String("id", "", "lane identifier")
	fs.StringVar(id, "lane", "", "lane identifier")
	workspacePath := fs.String("workspace", "", "assigned workspace")
	sourceControl := fs.String("source-control", "", "source control identity")
	writer := fs.String("writer", "", "writer identity")
	adapter := fs.String("adapter", "", "adapter profile")
	family := fs.String("family", "", "review family")
	seatID := seatIDFlag(fs)
	costClass := fs.String("cost-class", "", "cost class")
	reviewPolicy := fs.String("review-policy", "", "review policy")
	maxSeconds := fs.Int64("max-seconds", -1, "maximum seconds")
	maxOutput := fs.Int64("max-output-bytes", -1, "maximum output bytes")
	maxMemory := fs.Int64("max-memory-bytes", -1, "maximum memory bytes")
	var allowed, forbidden, nonGoals, required listFlag
	fs.Var(&allowed, "allowed-paths", "allowed path (repeatable)")
	fs.Var(&allowed, "allowed-path", "allowed path (repeatable)")
	fs.Var(&forbidden, "forbidden-paths", "forbidden path (repeatable)")
	fs.Var(&forbidden, "forbidden-path", "forbidden path (repeatable)")
	fs.Var(&nonGoals, "non-goals", "non-goal (repeatable)")
	fs.Var(&nonGoals, "non-goal", "non-goal (repeatable)")
	fs.Var(&required, "required-checks", "required check (repeatable)")
	fs.Var(&required, "required-check", "required check (repeatable)")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "lane open", *jsonOutput, nil, err)
	}
	if !laneIDPattern.MatchString(*id) {
		return writeResult(stdout, stderr, "lane open", *jsonOutput, nil, usageError("lane id must be a lowercase slug"))
	}
	if err := completeDispatch(*id, *workspacePath, *sourceControl, *writer, *adapter, *family, *seatID, *costClass, *reviewPolicy, *maxSeconds, *maxOutput, *maxMemory, allowed, forbidden, nonGoals, required); err != nil {
		return writeResult(stdout, stderr, "lane open", *jsonOutput, nil, err)
	}
	if err := requireWorkspace(*workspacePath); err != nil {
		return writeResult(stdout, stderr, "lane open", *jsonOutput, nil, err)
	}
	path, err := lanePath(*state, *id)
	if err != nil {
		return writeResult(stdout, stderr, "lane open", *jsonOutput, nil, err)
	}
	if err := workspace.ValidateSeparation(*state, *workspacePath); err != nil {
		return writeResult(stdout, stderr, "lane open", *jsonOutput, nil, err)
	}
	m := manifest.Manifest{SchemaVersion: 1, LaneID: *id, State: lifecycle.Planned, Dispatch: manifest.Dispatch{
		Workspace: *workspacePath, SourceControl: *sourceControl, Writer: *writer, Adapter: *adapter, Family: *family, SeatID: *seatID, CostClass: *costClass,
		AllowedPaths: allowed.values, ForbiddenPaths: forbidden.values, NonGoals: nonGoals.values, RequiredChecks: required.values,
		ReviewPolicy: *reviewPolicy, MaxSeconds: *maxSeconds, MaxOutputBytes: *maxOutput, MaxMemoryBytes: *maxMemory,
	}, ExternalActionsHumanOnly: true}
	snapshot, err := lifecycle.Apply(lifecycle.Snapshot{State: m.State}, lifecycle.Activate)
	if err != nil {
		return writeResult(stdout, stderr, "lane open", *jsonOutput, nil, err)
	}
	m.State = snapshot.State
	hash, err := manifest.NewStore(path, clock.System{}).Create(m)
	if err != nil {
		return writeResult(stdout, stderr, "lane open", *jsonOutput, nil, err)
	}
	return writeResult(stdout, stderr, "lane open", *jsonOutput, map[string]any{"id": *id, "state": string(m.State), "seat_id": *seatID, "hash": hash, "path": path}, nil)
}

func completeDispatch(id, work, source, writer, adapter, family, seatID, cost, review string, seconds, output, memory int64, lists ...listFlag) error {
	if !laneIDPattern.MatchString(id) || work == "" || source == "" || writer == "" || adapter == "" || family == "" || seatID == "" || cost == "" || review == "" || seconds < 0 || output < 0 || memory < 0 {
		return usageError("lane open requires complete dispatch")
	}
	for _, list := range lists {
		if !list.set {
			return usageError("lane open requires complete dispatch")
		}
		for _, value := range list.values {
			if value == "" {
				return usageError("lane open requires nonempty dispatch values")
			}
		}
	}
	return nil
}

type laneSummary struct {
	LaneID      string          `json:"lane_id"`
	State       lifecycle.State `json:"state"`
	UpdatedAt   string          `json:"updated_at"`
	BlockedFrom lifecycle.State `json:"blocked_from,omitempty"`
}

func runLaneList(args []string, stdout, stderr io.Writer) int {
	fs := commandFlags("lane list")
	state, jsonOutput := addStateFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "lane list", *jsonOutput, nil, err)
	}
	laneDir, err := lanesPath(*state)
	if err != nil {
		return writeResult(stdout, stderr, "lane list", *jsonOutput, nil, err)
	}
	entries, err := os.ReadDir(laneDir)
	if err != nil {
		return writeResult(stdout, stderr, "lane list", *jsonOutput, nil, failure.Wrap(failure.Integrity, "lanes directory read", err))
	}
	rows := make([]laneSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path, err := laneEntryPath(*state, entry.Name())
		if err != nil {
			return writeResult(stdout, stderr, "lane list", *jsonOutput, nil, err)
		}
		m, _, err := manifest.NewStore(path, clock.System{}).Load()
		if err != nil {
			return writeResult(stdout, stderr, "lane list", *jsonOutput, nil, err)
		}
		rows = append(rows, laneSummary{LaneID: m.LaneID, State: m.State, UpdatedAt: m.UpdatedAt, BlockedFrom: m.BlockedFrom})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].LaneID < rows[j].LaneID })
	return writeResult(stdout, stderr, "lane list", *jsonOutput, map[string]any{"lanes": rows}, nil)
}

func laneEntryPath(state, name string) (string, error) {
	if filepath.Base(name) != name || !strings.HasSuffix(name, ".json") {
		return "", failure.New(failure.Integrity, "lane manifest filename")
	}
	id := strings.TrimSuffix(name, ".json")
	if !laneIDPattern.MatchString(id) {
		return "", failure.New(failure.Integrity, "lane manifest filename")
	}
	return lanePath(state, id)
}

func runLaneRead(operation string, args []string, stdout, stderr io.Writer) int {
	fs := commandFlags("lane " + operation)
	state, jsonOutput := addStateFlags(fs)
	id := fs.String("id", "", "lane identifier")
	fs.StringVar(id, "lane", "", "lane identifier")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, nil, err)
	}
	path, err := lanePath(*state, *id)
	if err != nil {
		return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, nil, err)
	}
	m, hash, err := manifest.NewStore(path, clock.System{}).Load()
	if err != nil {
		return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, nil, err)
	}
	return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, map[string]any{"id": m.LaneID, "state": string(m.State), "hash": hash, "path": path}, nil)
}

func runLaneTransition(operation string, args []string, stdout, stderr io.Writer) int {
	fs := commandFlags("lane " + operation)
	state, jsonOutput := addStateFlags(fs)
	id := fs.String("id", "", "lane identifier")
	fs.StringVar(id, "lane", "", "lane identifier")
	expected := fs.String("expected-hash", "", "expected prior manifest hash")
	fs.StringVar(expected, "hash", "", "expected prior manifest hash")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, nil, err)
	}
	if *expected == "" {
		return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, nil, failure.New(failure.Integrity, "expected prior hash required"))
	}
	path, err := lanePath(*state, *id)
	if err != nil {
		return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, nil, err)
	}
	store := manifest.NewStore(path, clock.System{})
	m, hash, err := store.Load()
	if err != nil {
		return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, nil, err)
	}
	if *expected != hash {
		return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, nil, failure.New(failure.Integrity, "manifest prior hash mismatch"))
	}
	event, _ := lifecycle.ParseEvent(operationEvent(operation))
	snapshot, err := lifecycle.Apply(lifecycle.Snapshot{State: m.State, BlockedFrom: m.BlockedFrom}, event)
	if err != nil {
		return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, nil, err)
	}
	m.State, m.BlockedFrom = snapshot.State, snapshot.BlockedFrom
	newHash, err := store.Update(*expected, m)
	if err != nil {
		return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, nil, err)
	}
	return writeResult(stdout, stderr, "lane "+operation, *jsonOutput, map[string]any{"id": m.LaneID, "state": string(m.State), "previous_hash": hash, "hash": newHash}, nil)
}

func operationEvent(operation string) string {
	switch operation {
	case "block":
		return "block"
	case "resume":
		return "resume"
	case "abandon":
		return "abandon"
	default:
		return "incident"
	}
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := commandFlags("doctor")
	state, jsonOutput := addStateFlags(fs)
	work := fs.String("workspace", "", "optional workspace")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "doctor", *jsonOutput, nil, err)
	}
	if *state == "" {
		return writeResult(stdout, stderr, "doctor", *jsonOutput, nil, usageError("state root is required"))
	}
	checks := map[string]any{"os": runtime.GOOS, "git": map[string]any{}, "state": map[string]any{}, "schema": map[string]any{}}
	var first error
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		first = failure.New(failure.ToolingOrAdapter, "unsupported operating system")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		if first == nil {
			first = failure.Wrap(failure.ToolingOrAdapter, "git executable", err)
		}
	} else {
		gitPath, _ = filepath.Abs(gitPath)
		versionBytes, versionErr := exec.Command(gitPath, "--version").Output()
		version := strings.TrimSpace(string(versionBytes))
		if versionErr != nil || version == "" {
			if first == nil {
				first = failure.New(failure.ToolingOrAdapter, "git version")
			}
		} else {
			checks["git"] = map[string]any{"executable": gitPath, "version": version}
		}
	}
	if err := doctorSchema(checks); err != nil && first == nil {
		first = err
	}
	if *state != "" {
		if !filepath.IsAbs(*state) {
			if first == nil {
				first = usageError("state root must be an absolute path")
			}
		} else if err := doctorState(*state, checks); err != nil && first == nil {
			first = err
		}
	}
	if *work != "" && *state != "" {
		if err := workspace.ValidateSeparation(*state, *work); err != nil && first == nil {
			first = err
		}
	}
	return writeResult(stdout, stderr, "doctor", *jsonOutput, checks, first)
}

func doctorSchema(checks map[string]any) error {
	candidates := []string{filepath.Join("schema", "v1", "manifest.schema.json")}
	if _, source, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(source), "..", "..", "schema", "v1", "manifest.schema.json"))
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		canonical, err := canon.Bytes(data)
		if err != nil {
			return failure.Wrap(failure.ToolingOrAdapter, "schema JSON", err)
		}
		var value map[string]any
		if err := json.Unmarshal(canonical, &value); err != nil || value["$id"] == nil {
			return failure.New(failure.ToolingOrAdapter, "schema JSON")
		}
		checks["schema"] = map[string]any{"readable": true, "valid_json": true}
		return nil
	}
	return failure.New(failure.ToolingOrAdapter, "schema readable")
}

func doctorState(state string, checks map[string]any) error {
	info, err := os.Lstat(state)
	if errors.Is(err, os.ErrNotExist) {
		checks["state"] = map[string]any{"present": false}
		return nil
	}
	if err != nil {
		return failure.Wrap(failure.Integrity, "state root inspect", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return failure.New(failure.Integrity, "state root directory")
	}
	mode := fmt.Sprintf("%04o", info.Mode().Perm())
	checks["state"] = map[string]any{"present": true, "mode": mode, "mode_ok": info.Mode().Perm() == 0700}
	if info.Mode().Perm() != 0700 {
		return failure.New(failure.Integrity, "state root mode")
	}
	lanes := filepath.Join(state, "lanes")
	entries, err := os.ReadDir(lanes)
	if err != nil {
		return failure.Wrap(failure.Integrity, "lanes directory", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if _, _, err := manifest.NewStore(filepath.Join(lanes, entry.Name()), clock.System{}).Load(); err != nil {
			return err
		}
	}
	return nil
}
