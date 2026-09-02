package evidence_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rremedio-web/provehito/core/clock"
	"github.com/rremedio-web/provehito/core/evidence"
	"github.com/rremedio-web/provehito/core/failure"
)

func TestVerifyDetectsTampering(t *testing.T) {
	store := evidence.NewStore(t.TempDir())
	ref, err := store.Add(fixtureReceipt())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref.Path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(ref); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("expected integrity failure, got %v", err)
	}
}

func TestStoreOwnsTimestampAndCanonicalBytes(t *testing.T) {
	root := t.TempDir()
	fixed := time.Date(2026, 8, 19, 12, 34, 56, 987654321, time.FixedZone("west", -2*60*60))
	store := evidence.NewStore(root, clock.Fixed{Time: fixed})
	input := fixtureReceipt()
	input.Timestamp = "caller timestamp must not win"
	before := input
	ref, err := store.Add(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatalf("Add mutated caller: got %#v want %#v", input, before)
	}
	loaded, err := store.Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	wantTime := fixed.UTC().Truncate(time.Second).Format(time.RFC3339)
	if loaded.Timestamp != wantTime {
		t.Fatalf("timestamp: got %s want %s", loaded.Timestamp, wantTime)
	}
	data, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, canonical) {
		t.Fatalf("stored receipt is not canonical: %s", data)
	}
	if filepath.Base(filepath.Dir(ref.Path)) != ref.Hash[:2] || filepath.Base(ref.Path) != ref.Hash+".json" {
		t.Fatalf("content address path: %s", ref.Path)
	}
}

func TestStoreAddIsConcurrentAndIdempotent(t *testing.T) {
	store := evidence.NewStore(t.TempDir(), clock.Fixed{Time: time.Date(2026, 8, 19, 12, 34, 56, 0, time.UTC)})
	const workers = 24
	refs := make([]evidence.Reference, workers)
	errs := make([]error, workers)
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			refs[index], errs[index] = store.Add(fixtureReceipt())
		}(i)
	}
	group.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
		if refs[i].Hash != refs[0].Hash || refs[i].Path != refs[0].Path {
			t.Fatalf("worker %d reference differs: %#v %#v", i, refs[i], refs[0])
		}
	}
}

func TestStoreRejectsInvalidReceiptAndExitMismatch(t *testing.T) {
	store := evidence.NewStore(t.TempDir())
	cases := []evidence.Receipt{
		func() evidence.Receipt { r := fixtureReceipt(); r.CandidateHash = "bad"; return r }(),
		func() evidence.Receipt {
			r := fixtureReceipt()
			r.ResultClass = evidence.ResultToolingOrAdapter
			return r
		}(),
		func() evidence.Receipt { r := fixtureReceipt(); r.ResultClass = "UNKNOWN"; return r }(),
	}
	for _, receipt := range cases {
		if _, err := store.Add(receipt); failure.ExitCodeFor(err) != 10 {
			t.Fatalf("expected usage/schema failure, got %v", err)
		}
	}
}

func TestStoreUsesPrivateModes(t *testing.T) {
	store := evidence.NewStore(t.TempDir(), clock.Fixed{Time: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)})
	ref, err := store.Add(fixtureReceipt())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{store.Root, filepath.Join(store.Root, "evidence"), filepath.Join(store.Root, "evidence", "sha256"), filepath.Dir(ref.Path)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o700 {
			t.Fatalf("directory %s mode %04o", path, mode)
		}
	}
	info, err := os.Stat(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("receipt mode %04o", mode)
	}
}

func TestVerifyRejectsReferenceAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	store := evidence.NewStore(root, clock.Fixed{Time: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)})
	ref, err := store.Add(fixtureReceipt())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(evidence.Reference{Hash: ref.Hash, Path: filepath.Join(root, "elsewhere")}); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("path mismatch: got %v", err)
	}
	if err := os.Chmod(ref.Path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(ref); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("mode tamper: got %v", err)
	}
}

func TestVerifyRejectsSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	store := evidence.NewStore(root, clock.Fixed{Time: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)})
	ref, err := store.Add(fixtureReceipt())
	if err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Dir(ref.Path)
	outside := t.TempDir()
	if err := os.Rename(prefix, filepath.Join(root, "prefix-backup")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, prefix); err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(ref); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("symlinked parent: got %v", err)
	}
}

func TestLoadAndVerifyRejectRootModeDrift(t *testing.T) {
	root := t.TempDir()
	store := evidence.NewStore(root, clock.Fixed{Time: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)})
	ref, err := store.Add(fixtureReceipt())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ref); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("Load root mode drift: got %v", err)
	}
	if err := store.Verify(ref); failure.ExitCodeFor(err) != 60 {
		t.Fatalf("Verify root mode drift: got %v", err)
	}
}

func TestLoadRejectsOversizedStoredReceiptBeforeDecode(t *testing.T) {
	store := evidence.NewStore(t.TempDir(), clock.Fixed{Time: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)})
	ref, err := store.Add(fixtureReceipt())
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(ref.Path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(make([]byte, 1<<20)); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	err = nil
	if _, err = store.Load(ref); failure.ExitCodeFor(err) != 60 || !strings.Contains(err.Error(), "size") {
		t.Fatalf("oversized receipt: got %v", err)
	}
}

func TestLoadAndVerifyDerivePathFromHashOnlyReference(t *testing.T) {
	store := evidence.NewStore(t.TempDir(), clock.Fixed{Time: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)})
	ref, err := store.Add(fixtureReceipt())
	if err != nil {
		t.Fatal(err)
	}
	hashOnly := evidence.Reference{Hash: ref.Hash}
	if _, err := store.Load(hashOnly); err != nil {
		t.Fatalf("hash-only Load: %v", err)
	}
	if err := store.Verify(hashOnly); err != nil {
		t.Fatalf("hash-only Verify: %v", err)
	}
}

func canonicalBytes(data []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func fixtureReceipt() evidence.Receipt {
	return evidence.Receipt{
		SchemaVersion: 1,
		MethodID:      "fixture-check",
		Probe:         "fixture-v1",
		CandidateHash: strings.Repeat("a", 64),
		ManifestHash:  strings.Repeat("b", 64),
		ResultClass:   evidence.ResultSuccess,
		ExitCode:      0,
	}
}
