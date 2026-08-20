package manifest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/provehito-project/provehito/core/canon"
	"github.com/provehito-project/provehito/core/clock"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/manifest"
)

func TestStoreLoadsCanonicalManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.json")
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	createdHash, err := s.Create(fixtureManifest(lifecycle.Planned))
	if err != nil {
		t.Fatal(err)
	}
	_, loadedHash, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loadedHash != createdHash {
		t.Fatalf("loaded hash %s, want %s", loadedHash, createdHash)
	}
}

func TestStoreRejectsManifestLockSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.json")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("target"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+".lock"); err != nil {
		t.Fatal(err)
	}
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(fixtureManifest(lifecycle.Planned)); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("expected integrity failure, got %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0644 {
		t.Fatalf("lock symlink redirected chmod: target mode %04o", mode)
	}
}

func TestStoreLoadClassifiesStoredShapeErrorsAsIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		raw   json.RawMessage
	}{
		{name: "unknown field", field: "unexpected", raw: json.RawMessage(`true`)},
		{name: "wrong field type", field: "lane_id", raw: json.RawMessage(`1`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "demo.json")
			s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
			if _, err := s.Create(fixtureManifest(lifecycle.Planned)); err != nil {
				t.Fatal(err)
			}
			writeCanonicalField(t, path, tc.field, tc.raw)
			if _, _, err := s.Load(); failure.ExitCodeFor(err) != 60 {
				t.Fatalf("stored %s: got %v", tc.name, err)
			}
		})
	}
}

func TestNormalizationDoesNotMutateCallerOwnedData(t *testing.T) {
	m := fixtureManifest(lifecycle.Reviewed)
	setOffsetTimestamps(&m)
	beforeCreate := marshalManifest(t, m)
	s := manifest.NewStore(filepath.Join(t.TempDir(), "demo.json"), clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(m); err != nil {
		t.Fatal(err)
	}
	if got := marshalManifest(t, m); !bytes.Equal(got, beforeCreate) {
		t.Fatalf("Create mutated caller: got %s want %s", got, beforeCreate)
	}

	loaded, hash, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	setOffsetTimestamps(&loaded)
	beforeUpdate := marshalManifest(t, loaded)
	if _, err := s.Update(hash, loaded); err != nil {
		t.Fatal(err)
	}
	if got := marshalManifest(t, loaded); !bytes.Equal(got, beforeUpdate) {
		t.Fatalf("Update mutated caller: got %s want %s", got, beforeUpdate)
	}

	beforeValidate, expectedHash := persistedManifest(t, lifecycle.Reviewed)
	setOffsetTimestamps(&beforeValidate)
	afterValidate := beforeValidate
	beforeValidateBytes := marshalManifest(t, beforeValidate)
	if err := manifest.ValidateUpdate(expectedHash, beforeValidate, afterValidate); err != nil {
		t.Fatal(err)
	}
	if got := marshalManifest(t, beforeValidate); !bytes.Equal(got, beforeValidateBytes) {
		t.Fatalf("ValidateUpdate mutated caller: got %s want %s", got, beforeValidateBytes)
	}
}

func TestStoreRootTimestampsComeFromInjectedClock(t *testing.T) {
	createdAt := fixedTime()
	updatedAt := createdAt.Add(2 * time.Hour)
	caller := fixtureManifest(lifecycle.Reviewed)
	caller.CreatedAt = "not-a-timestamp"
	caller.UpdatedAt = "not-a-timestamp"
	beforeCreate := marshalManifest(t, caller)
	path := filepath.Join(t.TempDir(), "demo.json")
	creator := manifest.NewStore(path, clock.Fixed{Time: createdAt})
	if _, err := creator.Create(caller); err != nil {
		t.Fatal(err)
	}
	if got := marshalManifest(t, caller); !bytes.Equal(got, beforeCreate) {
		t.Fatalf("Create mutated root timestamps: got %s want %s", got, beforeCreate)
	}
	stored, hash, err := creator.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.CreatedAt != createdAt.Format(time.RFC3339) || stored.UpdatedAt != createdAt.Format(time.RFC3339) {
		t.Fatalf("created timestamps: got %s %s", stored.CreatedAt, stored.UpdatedAt)
	}

	stored.CreatedAt = "not-the-stored-timestamp"
	stored.UpdatedAt = "not-the-clock-timestamp"
	beforeUpdate := marshalManifest(t, stored)
	updater := manifest.NewStore(path, clock.Fixed{Time: updatedAt})
	if _, err := updater.Update(hash, stored); err != nil {
		t.Fatal(err)
	}
	if got := marshalManifest(t, stored); !bytes.Equal(got, beforeUpdate) {
		t.Fatalf("Update mutated root timestamps: got %s want %s", got, beforeUpdate)
	}
	stored, _, err = updater.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.CreatedAt != createdAt.Format(time.RFC3339) || stored.UpdatedAt != updatedAt.Format(time.RFC3339) {
		t.Fatalf("updated timestamps: got %s %s", stored.CreatedAt, stored.UpdatedAt)
	}
}

func TestStoreRejectsOptionalPresenceDrift(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		raw   json.RawMessage
	}{
		{name: "empty evidence", field: "evidence", raw: json.RawMessage(`[]`)},
		{name: "empty failures", field: "failures", raw: json.RawMessage(`[]`)},
		{name: "null evidence", field: "evidence", raw: json.RawMessage(`null`)},
		{name: "null failures", field: "failures", raw: json.RawMessage(`null`)},
		{name: "null freeze", field: "freeze", raw: json.RawMessage(`null`)},
		{name: "null review", field: "review", raw: json.RawMessage(`null`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "demo.json")
			s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
			if _, err := s.Create(fixtureManifest(lifecycle.Reviewed)); err != nil {
				t.Fatal(err)
			}
			writeCanonicalField(t, path, tc.field, tc.raw)
			if _, _, err := s.Load(); failure.ExitCodeFor(err) != 60 {
				t.Fatalf("%s presence drift: got %v", tc.name, err)
			}
		})
	}
}

func TestStoreRoundTripsNonemptyOptionalArrays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.json")
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	want := fixtureManifest(lifecycle.Planned)
	if _, err := s.Create(want); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Evidence, want.Evidence) || !reflect.DeepEqual(got.Failures, want.Failures) {
		t.Fatalf("optional arrays: got %#v %#v", got.Evidence, got.Failures)
	}
}

func TestStoreNormalizesDeclaredTimestamps(t *testing.T) {
	m := fixtureManifest(lifecycle.Reviewed)
	m.CreatedAt = "2026-08-19T13:00:00.987654321+01:00"
	m.UpdatedAt = "2026-08-19T11:00:00.123456789-01:00"
	m.Freeze.At = "2026-08-19T12:00:00.999999999Z"
	m.Review.At = "2026-08-19T12:00:00.000000001Z"
	m.Failures[0].At = "2026-08-19T13:00:00.500+01:00"
	s := manifest.NewStore(filepath.Join(t.TempDir(), "demo.json"), clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(m); err != nil {
		t.Fatal(err)
	}
	got, currentHash, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{got.CreatedAt, got.UpdatedAt, got.Freeze.At, got.Review.At, got.Failures[0].At} {
		if value != fixedTimestamp() {
			t.Fatalf("timestamp: got %s want %s", value, fixedTimestamp())
		}
	}
	updated := got
	updated.State = lifecycle.Active
	updated.CreatedAt = "2026-08-19T13:00:00+01:00"
	updated.UpdatedAt = "2026-08-19T13:00:00.500+01:00"
	if _, err := s.Update(currentHash, updated); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedAt != fixedTimestamp() || got.UpdatedAt != fixedTimestamp() {
		t.Fatalf("updated timestamps: got %s %s", got.CreatedAt, got.UpdatedAt)
	}
}

func TestStoreRejectsInvalidDeclaredTimestamp(t *testing.T) {
	m := fixtureManifest(lifecycle.Planned)
	m.Failures[0].At = "not-a-timestamp"
	s := manifest.NewStore(filepath.Join(t.TempDir(), "demo.json"), clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(m); failure.ExitCodeFor(err) != 10 {
		t.Fatalf("invalid timestamp: got %v", err)
	}
	valid := fixtureManifest(lifecycle.Planned)
	if _, err := s.Create(valid); err != nil {
		t.Fatal(err)
	}
	loaded, hash, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	loaded.State = lifecycle.Active
	loaded.Failures[0].At = "not-a-timestamp"
	if _, err := s.Update(hash, loaded); failure.ExitCodeFor(err) != 10 {
		t.Fatalf("invalid update timestamp: got %v", err)
	}
}

func TestDispatchCannotChangeAfterActivation(t *testing.T) {
	before, expectedHash := persistedManifest(t, lifecycle.Active)
	after := before
	after.Dispatch.Workspace = "/different"
	if err := manifest.ValidateUpdate(expectedHash, before, after); failure.ExitCodeFor(err) != 20 {
		t.Fatalf("expected policy failure, got %v", err)
	}
}

func TestFreezeAndReviewRecordsAreImmutableAfterTheirStates(t *testing.T) {
	before, expectedHash := persistedManifest(t, lifecycle.Reviewed)
	after := before
	after.Freeze = cloneFreeze(before.Freeze)
	after.Freeze.Candidate = "different"
	if err := manifest.ValidateUpdate(expectedHash, before, after); failure.ExitCodeFor(err) != 20 {
		t.Fatalf("freeze mutation: got %v", err)
	}
	after = before
	after.Review = cloneReview(before.Review)
	after.Review.Verdict = "FAIL"
	if err := manifest.ValidateUpdate(expectedHash, before, after); failure.ExitCodeFor(err) != 20 {
		t.Fatalf("review mutation: got %v", err)
	}
}

func TestValidateUpdateRejectsAliasedMutableFields(t *testing.T) {
	tests := []struct {
		name   string
		state  lifecycle.State
		mutate func(*manifest.Manifest)
	}{
		{
			name:  "dispatch slice",
			state: lifecycle.Active,
			mutate: func(m *manifest.Manifest) {
				m.Dispatch.AllowedPaths[0] = "changed"
			},
		},
		{
			name:  "freeze pointer",
			state: lifecycle.Reviewed,
			mutate: func(m *manifest.Manifest) {
				m.Freeze.Candidate = "changed"
			},
		},
		{
			name:  "review pointer",
			state: lifecycle.Reviewed,
			mutate: func(m *manifest.Manifest) {
				m.Review.Verdict = "FAIL"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before, expectedHash := persistedManifest(t, tc.state)
			after := before
			tc.mutate(&after)
			if code := failure.ExitCodeFor(manifest.ValidateUpdate(expectedHash, before, after)); code != 20 && code != 60 {
				t.Fatalf("aliased mutation exit code: got %d want policy or integrity", code)
			}
		})
	}
}

func TestStoreCreateUpdateAndStaleHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.json")
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	m := fixtureManifest(lifecycle.Planned)
	hash, err := s.Create(m)
	if err != nil {
		t.Fatal(err)
	}
	loaded, loadedHash, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loadedHash != hash || !reflect.DeepEqual(loaded.Dispatch, m.Dispatch) || !isLowerHexHash(loaded.DispatchHash) ||
		loaded.SchemaVersion != m.SchemaVersion || loaded.LaneID != m.LaneID || loaded.State != m.State ||
		loaded.CreatedAt != fixedTimestamp() || loaded.UpdatedAt != fixedTimestamp() || !loaded.ExternalActionsHumanOnly {
		t.Fatalf("loaded manifest/hash mismatch: %#v %s", loaded, loadedHash)
	}
	m.State = lifecycle.Active
	newHash, err := s.Update(hash, m)
	if err != nil {
		t.Fatal(err)
	}
	if newHash == hash {
		t.Fatal("update hash did not change")
	}
	if _, err := s.Update(hash, m); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("stale update: got %v", err)
	}
}

func TestStoreRejectsIncorrectSuppliedDispatchHash(t *testing.T) {
	m := fixtureManifest(lifecycle.Planned)
	m.DispatchHash = strings.Repeat("0", 64)
	s := manifest.NewStore(filepath.Join(t.TempDir(), "demo.json"), clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(m); failure.ExitCodeFor(err) != 10 {
		t.Fatalf("lying dispatch hash: got %v", err)
	}
}

func TestStoreRejectsSchemaInvalidManifest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*manifest.Manifest)
	}{
		{
			name: "unknown state",
			mutate: func(m *manifest.Manifest) {
				m.State = "UNKNOWN"
			},
		},
		{
			name: "review fingerprint differs from freeze",
			mutate: func(m *manifest.Manifest) {
				m.Review.Fingerprint = "different"
			},
		},
		{
			name: "evidence hash is not sha256",
			mutate: func(m *manifest.Manifest) {
				m.Evidence = []manifest.EvidenceReference{{Name: "check", Hash: "not-a-hash"}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureManifest(lifecycle.Reviewed)
			tc.mutate(&m)
			s := manifest.NewStore(filepath.Join(t.TempDir(), "demo.json"), clock.Fixed{Time: fixedTime()})
			if _, err := s.Create(m); failure.ExitCodeFor(err) != 10 {
				t.Fatalf("invalid manifest: got %v", err)
			}
		})
	}
}

func TestStoreRejectsMalformedOptionalFreezeRecord(t *testing.T) {
	m := fixtureManifest(lifecycle.Planned)
	m.Freeze = &manifest.FreezeRecord{Base: "base", Head: "head", Candidate: "candidate", Tree: "tree", Diff: "diff", At: "not-a-timestamp"}
	s := manifest.NewStore(filepath.Join(t.TempDir(), "demo.json"), clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(m); failure.ExitCodeFor(err) != 10 {
		t.Fatalf("invalid optional freeze: got %v", err)
	}
}

func TestStoreRejectsMalformedBytesAndLeavesNoRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.json")
	bad := []byte(`{"schema_version":1,"schema_version":2}`)
	if err := os.WriteFile(path, bad, 0600); err != nil {
		t.Fatal(err)
	}
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	if _, _, err := s.Load(); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("malformed load: got %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b, bad) {
		t.Fatalf("store changed malformed bytes: %s", b)
	}
}

func TestStoreRejectsTemporaryResidue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.json")
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), ".demo.json.tmp-crash"), []byte("residue"), 0600); err != nil {
		t.Fatal(err)
	}
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(fixtureManifest(lifecycle.Planned)); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("residue create: got %v", err)
	}
}

func TestStoreTreatsMetacharacterBasenamesLiterally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest[1]*?.json")
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(fixtureManifest(lifecycle.Planned)); err != nil {
		t.Fatalf("clean metacharacter path: %v", err)
	}

	blockedPath := filepath.Join(dir, "blocked[1]*?.json")
	residuePath := filepath.Join(dir, ".blocked[1]*?.json.tmp-crash")
	if err := os.WriteFile(residuePath, []byte("residue"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked := manifest.NewStore(blockedPath, clock.Fixed{Time: fixedTime()})
	if _, err := blocked.Create(fixtureManifest(lifecycle.Planned)); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("metacharacter residue: got %v", err)
	}
}

func TestStoreSerializesConcurrentUpdatesFromOnePriorHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.json")
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	prior, err := s.Create(fixtureManifest(lifecycle.Planned))
	if err != nil {
		t.Fatal(err)
	}

	candidates := []manifest.Manifest{fixtureManifest(lifecycle.Active), fixtureManifest(lifecycle.Active)}
	candidates[0].Failures[0].Error = "first"
	candidates[1].Failures[0].Error = "second"
	start := make(chan struct{})
	results := make(chan struct {
		hash string
		err  error
	}, len(candidates))
	var workers sync.WaitGroup
	for _, candidate := range candidates {
		workers.Add(1)
		go func(candidate manifest.Manifest) {
			defer workers.Done()
			<-start
			hash, err := s.Update(prior, candidate)
			results <- struct {
				hash string
				err  error
			}{hash, err}
		}(candidate)
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	var successfulHash string
	for result := range results {
		if result.err == nil {
			successes++
			successfulHash = result.hash
			continue
		}
		if failure.ExitCodeFor(result.err) != 60 {
			t.Fatalf("concurrent update error: %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes: got %d want 1", successes)
	}
	_, loadedHash, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loadedHash != successfulHash {
		t.Fatalf("lost update: loaded %s want successful %s", loadedHash, successfulHash)
	}
}

func TestStoreWritesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.json")
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(fixtureManifest(lifecycle.Planned)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode: got %o want 600", info.Mode().Perm())
	}
	lockInfo, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if lockInfo.Mode().Perm() != 0600 {
		t.Fatalf("lock mode: got %o want 600", lockInfo.Mode().Perm())
	}
}

func TestManifestSchemaPropertyParity(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "schema", "v1", "manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		ID         string                     `json:"$id"`
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.ID != "urn:provehito:schema:manifest:v1" {
		t.Fatalf("schema ID: got %q", schema.ID)
	}
	encoded, err := json.Marshal(fixtureManifest(lifecycle.Reviewed))
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	blocked, err := json.Marshal(manifest.Manifest{State: lifecycle.Blocked, BlockedFrom: lifecycle.Planned})
	if err != nil {
		t.Fatal(err)
	}
	var blockedFixture map[string]json.RawMessage
	if err := json.Unmarshal(blocked, &blockedFixture); err != nil {
		t.Fatal(err)
	}
	for name, value := range blockedFixture {
		fixture[name] = value
	}
	assertSameKeys(t, "manifest", schema.Properties, fixture)
	assertSameKeys(t, "dispatch", schema.Defs["dispatch"].Properties, jsonObject(t, fixture["dispatch"]))
	assertSameKeys(t, "freeze", schema.Defs["freeze"].Properties, jsonObject(t, fixture["freeze"]))
	assertSameKeys(t, "review", schema.Defs["review"].Properties, jsonObject(t, fixture["review"]))
	assertSameKeys(t, "evidence", schema.Defs["evidence_reference"].Properties, jsonArrayObject(t, fixture["evidence"]))
	assertSameKeys(t, "failure", schema.Defs["failure_record"].Properties, jsonArrayObject(t, fixture["failures"]))
}

func TestManifestSchemaRequiresNonemptyOptionalArrays(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "schema", "v1", "manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			MinItems int `json:"minItems"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"evidence", "failures"} {
		if schema.Properties[name].MinItems != 1 {
			t.Fatalf("%s minItems: got %d want 1", name, schema.Properties[name].MinItems)
		}
	}
}

func assertSameKeys(t *testing.T, name string, schema, fixture map[string]json.RawMessage) {
	t.Helper()
	if !reflect.DeepEqual(keySet(schema), keySet(fixture)) {
		t.Fatalf("%s property mismatch: schema=%v fixture=%v", name, keySet(schema), keySet(fixture))
	}
}

func jsonObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func jsonArrayObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 {
		t.Fatalf("expected one JSON object, got %d", len(values))
	}
	return jsonObject(t, values[0])
}

func keySet(values map[string]json.RawMessage) map[string]struct{} {
	keys := make(map[string]struct{}, len(values))
	for key := range values {
		keys[key] = struct{}{}
	}
	return keys
}

func fixtureManifest(state lifecycle.State) manifest.Manifest {
	m := manifest.Manifest{
		SchemaVersion:            1,
		LaneID:                   "demo",
		State:                    state,
		Dispatch:                 fixtureDispatch(),
		Evidence:                 []manifest.EvidenceReference{{Name: "check", Hash: strings.Repeat("e", 64)}},
		Failures:                 []manifest.FailureRecord{{Class: "INTEGRITY", Op: "fixture", At: fixedTimestamp(), Error: "recorded"}},
		CreatedAt:                fixedTimestamp(),
		UpdatedAt:                fixedTimestamp(),
		ExternalActionsHumanOnly: true,
	}
	if state == lifecycle.Frozen || state == lifecycle.Reviewed || state == lifecycle.Ready || state == lifecycle.Closed {
		m.Freeze = &manifest.FreezeRecord{Base: "base", Head: "head", Candidate: "candidate", Tree: "tree", Diff: "diff", At: fixedTimestamp()}
	}
	if state == lifecycle.Reviewed || state == lifecycle.Ready || state == lifecycle.Closed {
		m.Review = &manifest.ReviewRecord{Reviewer: "reviewer", Family: "independent", Verdict: "PASS", Fingerprint: "candidate", EvidenceHashes: []string{strings.Repeat("e", 64)}, At: fixedTimestamp()}
	}
	return m
}

func fixtureDispatch() manifest.Dispatch {
	return manifest.Dispatch{
		Workspace:      "/tmp/workspace",
		SourceControl:  "git:abc",
		Writer:         "writer",
		Adapter:        "fake",
		Family:         "local",
		CostClass:      "economy",
		AllowedPaths:   []string{"core"},
		ForbiddenPaths: []string{},
		NonGoals:       []string{},
		RequiredChecks: []string{"go test ./..."},
		ReviewPolicy:   "required",
	}
}

func fixedTime() time.Time { return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC) }

func fixedTimestamp() string { return fixedTime().Format(time.RFC3339) }

func isLowerHexHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func cloneFreeze(in *manifest.FreezeRecord) *manifest.FreezeRecord {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneReview(in *manifest.ReviewRecord) *manifest.ReviewRecord {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func persistedManifest(t *testing.T, state lifecycle.State) (manifest.Manifest, string) {
	t.Helper()
	s := manifest.NewStore(filepath.Join(t.TempDir(), "demo.json"), clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(fixtureManifest(state)); err != nil {
		t.Fatal(err)
	}
	loaded, hash, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	return loaded, hash
}

func writeCanonicalField(t *testing.T, path, field string, value json.RawMessage) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object[field] = value
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canon.Bytes(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, canonical, 0600); err != nil {
		t.Fatal(err)
	}
}

func setOffsetTimestamps(m *manifest.Manifest) {
	m.CreatedAt = "2026-08-19T13:00:00+01:00"
	m.UpdatedAt = "2026-08-19T11:00:00-01:00"
	m.Freeze.At = "2026-08-19T12:00:00.999999999Z"
	m.Review.At = "2026-08-19T13:00:00.500+01:00"
	m.Failures[0].At = "2026-08-19T11:00:00.123-01:00"
}

func marshalManifest(t *testing.T, m manifest.Manifest) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStoreLoadDoesNotTreatMissingFileAsValid(t *testing.T) {
	s := manifest.NewStore(filepath.Join(t.TempDir(), "missing.json"), clock.Fixed{Time: fixedTime()})
	if _, _, err := s.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing load error %v does not retain os.ErrNotExist", err)
	}
}

func TestStoreLoadRejectsFinalPathSymlinkEvenWhenTargetValid(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	canonicalPath := filepath.Join(dir, "canonical.json")
	s := manifest.NewStore(canonicalPath, clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(fixtureManifest(lifecycle.Planned)); err != nil {
		t.Fatal(err)
	}
	canonical, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, canonical, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "demo.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	loader := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	if _, _, err := loader.Load(); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("symlink load: got %v", err)
	}
}

func TestStoreCreateRejectsFinalSymlinkWithoutFollowingOrMutatingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte("preserve-me"), 0644); err != nil {
		t.Fatal(err)
	}
	beforeMode, beforeBytes := fileState(t, target)
	path := filepath.Join(dir, "demo.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(fixtureManifest(lifecycle.Planned)); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("symlink create: got %v", err)
	}
	afterMode, afterBytes := fileState(t, target)
	if afterMode != beforeMode || !bytes.Equal(afterBytes, beforeBytes) {
		t.Fatalf("create followed or mutated symlink target: mode %o->%o bytes %q->%q", beforeMode, afterMode, beforeBytes, afterBytes)
	}
}

func TestStoreCreateRejectsNonRegularFinalEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(fixtureManifest(lifecycle.Planned)); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("directory create: got %v", err)
	}
}

func TestStoreCreateRejectsPreexistingRegularFinalEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.json")
	if err := os.WriteFile(path, []byte("preexisting"), 0600); err != nil {
		t.Fatal(err)
	}
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(fixtureManifest(lifecycle.Planned)); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("preexisting create: got %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "preexisting" {
		t.Fatalf("create replaced preexisting bytes: %q", b)
	}
}

func TestStoreLoadRejectsFinalPathWithWrongMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.json")
	s := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	if _, err := s.Create(fixtureManifest(lifecycle.Planned)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load(); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("wrong mode load: got %v", err)
	}
}

func TestStoreUpdateRejectsFinalSymlinkViaLoadAndLeavesTargetUnchanged(t *testing.T) {
	dir := t.TempDir()
	canonicalPath := filepath.Join(dir, "canonical.json")
	s := manifest.NewStore(canonicalPath, clock.Fixed{Time: fixedTime()})
	hash, err := s.Create(fixtureManifest(lifecycle.Planned))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, canonical, 0600); err != nil {
		t.Fatal(err)
	}
	beforeMode, beforeBytes := fileState(t, target)
	path := filepath.Join(dir, "demo.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	updater := manifest.NewStore(path, clock.Fixed{Time: fixedTime()})
	updated := fixtureManifest(lifecycle.Active)
	if _, err := updater.Update(hash, updated); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("symlink update: got %v", err)
	}
	afterMode, afterBytes := fileState(t, target)
	if afterMode != beforeMode || !bytes.Equal(afterBytes, beforeBytes) {
		t.Fatalf("update followed or mutated symlink target: mode %o->%o bytes %q->%q", beforeMode, afterMode, beforeBytes, afterBytes)
	}
}

func fileState(t *testing.T, path string) (os.FileMode, []byte) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm(), data
}
