package releasecheck_test

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/provehito-project/provehito/internal/releasecheck"
)

func TestCheckValidArchive(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{
		"provehito/README.md": "# ok\n",
		"provehito/go.mod":    "module example\n",
	})
	expected := []string{"README.md", "go.mod"}
	result, err := releasecheck.Check(z, releasecheck.Options{
		ExpectedFiles: expected,
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.StructuralStatus != releasecheck.StatusPass {
		t.Fatalf("structural status = %q, want PASS; findings=%v", result.StructuralStatus, result.Findings)
	}
	if result.PrivateStatus != releasecheck.StatusSkipped {
		t.Fatalf("private status = %q, want SKIPPED", result.PrivateStatus)
	}
	if result.MemberCount != 2 {
		t.Fatalf("member count = %d, want 2", result.MemberCount)
	}
	if result.TrackedCount != 2 {
		t.Fatalf("tracked count = %d, want 2", result.TrackedCount)
	}
}

func TestRejectAbsolutePath(t *testing.T) {
	t.Parallel()
	z := buildRawZip(t, []rawEntry{{name: "/etc/passwd", body: "x"}})
	result, err := releasecheck.Check(z, releasecheck.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	assertFinding(t, result, "absolute-path")
}

func TestRejectTraversal(t *testing.T) {
	t.Parallel()
	z := buildRawZip(t, []rawEntry{{name: "provehito/../secret", body: "x"}})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "path-traversal")
}

func TestRejectBackslash(t *testing.T) {
	t.Parallel()
	z := buildRawZip(t, []rawEntry{{name: "provehito\\file.txt", body: "x"}})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "backslash-path")
}

func TestRejectControlName(t *testing.T) {
	t.Parallel()
	z := buildRawZip(t, []rawEntry{{name: "provehito/bad\x00name", body: "x"}})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "control-name")
}

func TestRejectDuplicateNames(t *testing.T) {
	t.Parallel()
	z := buildRawZip(t, []rawEntry{
		{name: "provehito/dup.txt", body: "a"},
		{name: "provehito/dup.txt", body: "b"},
	})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "duplicate-name")
}

func TestRejectCaseFoldCollision(t *testing.T) {
	t.Parallel()
	z := buildRawZip(t, []rawEntry{
		{name: "provehito/File.txt", body: "a"},
		{name: "provehito/file.txt", body: "b"},
	})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "case-fold-collision")
}

func TestRejectEncryptedEntry(t *testing.T) {
	t.Parallel()
	z := buildEncryptedZip(t, "provehito/secret.txt", "data")
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "encrypted-entry")
}

func TestRejectSymlinkEntry(t *testing.T) {
	t.Parallel()
	z := buildSymlinkZip(t, "provehito/link", "target")
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "non-regular-entry")
}

func TestRejectOversizedFile(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 32*1024*1024+1)
	z := buildZip(t, map[string]string{"provehito/big.txt": body})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "file-size-limit")
}

func TestRejectOverflowingUncompressedSizeMetadata(t *testing.T) {
	t.Parallel()
	z := buildZipWithDeclaredUncompressedSize(t, "provehito/evil.txt", math.MaxUint64)
	result, err := releasecheck.Check(z, releasecheck.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	assertFinding(t, result, "file-size-limit")
}

func TestRejectOversizedTotal(t *testing.T) {
	t.Parallel()
	half := strings.Repeat("x", 16*1024*1024+1)
	z := buildZip(t, map[string]string{
		"provehito/a.txt": half,
		"provehito/b.txt": half,
	})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "total-size-limit")
}

func TestRejectCompressionRatio(t *testing.T) {
	t.Parallel()
	z := buildHighRatioZip(t, "provehito/compressed.txt")
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "compression-ratio")
}

func TestRejectNULContent(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/binary.bin": "hello\x00world"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "nul-content")
}

func TestRejectForbiddenGit(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/.git/config": "[core]\n"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "forbidden-segment")
}

func TestRejectForbiddenSuperpowers(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/.superpowers/state": "x"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "forbidden-segment")
}

func TestRejectForbiddenMACOSX(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/__MACOSX/._file": "x"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "forbidden-segment")
}

func TestRejectForbiddenDSStore(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/.DS_Store": "x"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "forbidden-name")
}

func TestRejectForbiddenBundle(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/app.bundle/Contents": "x"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "forbidden-name")
}

func TestRejectForbiddenEnv(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/.env": "SECRET=1\n"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "forbidden-name")
}

func TestRejectForbiddenBuildArtifact(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/dist/output.js": "x"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "forbidden-segment")
}

func TestRejectForbiddenTestSuffix(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/fixture.test": "x"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "forbidden-segment")
}

func TestRejectForbiddenEditorBackup(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/config.bak": "x"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "forbidden-segment")
}

func TestRejectMissingPrefix(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"README.md": "# no prefix\n"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "missing-prefix")
}

func TestRejectAbsolutePathInContent(t *testing.T) {
	t.Parallel()
	content := "path=" + assembleHostilePath("Users", "alice", "secret") + "\n"
	z := buildZip(t, map[string]string{"provehito/config.txt": content})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "absolute-path-content")
}

func TestRejectHomePathInContent(t *testing.T) {
	t.Parallel()
	content := "path=" + assembleHostilePath("home", "bob", "secret") + "\n"
	z := buildZip(t, map[string]string{"provehito/config.txt": content})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "absolute-path-content")
}

func TestRejectNonExampleEmail(t *testing.T) {
	t.Parallel()
	email := assembleFragments("alice", "@", "company", ".", "com")
	z := buildZip(t, map[string]string{"provehito/contact.txt": "reach me at " + email + "\n"})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "non-example-email")
}

func TestAllowExampleEmail(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/contact.txt": "reach me at alice@example.com\n"})
	result, err := releasecheck.Check(z, releasecheck.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.StructuralStatus != releasecheck.StatusPass {
		t.Fatalf("structural status = %q, want PASS; findings=%v", result.StructuralStatus, result.Findings)
	}
}

func TestAllowExampleInvalidEmail(t *testing.T) {
	t.Parallel()
	email := assembleFragments("test", "@", "example", ".", "invalid")
	z := buildZip(t, map[string]string{"provehito/contact.txt": "reach me at " + email + "\n"})
	result, err := releasecheck.Check(z, releasecheck.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.StructuralStatus != releasecheck.StatusPass {
		t.Fatalf("structural status = %q, want PASS; findings=%v", result.StructuralStatus, result.Findings)
	}
}

func TestRejectCredentialPattern(t *testing.T) {
	t.Parallel()
	key := assembleFragments("-----BEGIN", " RSA PRIVATE KEY-----\n", "MIIB\n")
	z := buildZip(t, map[string]string{"provehito/key.pem": key})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "credential-pattern")
}

func TestRejectURLCredential(t *testing.T) {
	t.Parallel()
	url := assembleFragments("https://", "user", ":", "secret", "@", "example.com/path\n")
	z := buildZip(t, map[string]string{"provehito/url.txt": url})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "url-credential")
}

func TestRejectNonAllowlistedHost(t *testing.T) {
	t.Parallel()
	link := assembleFragments("see ", "https://", "github.com/org/repo\n")
	z := buildZip(t, map[string]string{"provehito/link.txt": link})
	result, _ := releasecheck.Check(z, releasecheck.Options{})
	assertFinding(t, result, "non-allowlisted-host")
}

func TestAllowAllowlistedHost(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/link.txt": "see https://json-schema.org/draft/2020-12/schema\n"})
	result, err := releasecheck.Check(z, releasecheck.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.StructuralStatus != releasecheck.StatusPass {
		t.Fatalf("structural status = %q, want PASS", result.StructuralStatus)
	}
}

func TestExpectedListMismatch(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/a.txt": "a"})
	result, _ := releasecheck.Check(z, releasecheck.Options{
		ExpectedFiles: []string{"a.txt", "missing.txt"},
	})
	assertFinding(t, result, "expected-list-mismatch")
}

func TestPrivateDenylistMatch(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/secret.txt": "contains PRIVATE_NEEDLE_VALUE\n"})
	denylist := writeTempFile(t, "PRIVATE_NEEDLE_VALUE\n# comment\n\n")
	result, _ := releasecheck.Check(z, releasecheck.Options{
		PrivateDenylistPath: denylist,
	})
	if result.PrivateStatus != releasecheck.StatusFail {
		t.Fatalf("private status = %q, want FAIL", result.PrivateStatus)
	}
	assertFinding(t, result, "private-denylist")
}

func TestPrivateDenylistMissingIsToolingIncomplete(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/ok.txt": "ok\n"})
	_, err := releasecheck.Check(z, releasecheck.Options{
		PrivateDenylistPath: "/nonexistent/denylist.txt",
	})
	if err == nil {
		t.Fatal("expected error for missing denylist")
	}
	if !strings.Contains(err.Error(), "denylist") {
		t.Fatalf("error = %q, want denylist mention", err)
	}
}

func TestPrivateDenylistIgnoresCommentsAndBlank(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{"provehito/ok.txt": "clean content\n"})
	denylist := writeTempFile(t, "# comment\n\n  \n# another\n")
	result, err := releasecheck.Check(z, releasecheck.Options{
		PrivateDenylistPath: denylist,
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.PrivateStatus != releasecheck.StatusPass {
		t.Fatalf("private status = %q, want PASS", result.PrivateStatus)
	}
}

func TestLeakSafeErrors(t *testing.T) {
	t.Parallel()
	needle := "SUPER_SECRET_NEEDLE_XYZ"
	z := buildZip(t, map[string]string{"provehito/leak.txt": "prefix " + needle + " suffix\n"})
	denylist := writeTempFile(t, needle+"\n")
	result, _ := releasecheck.Check(z, releasecheck.Options{
		PrivateDenylistPath: denylist,
	})
	for _, f := range result.Findings {
		if strings.Contains(f.Rule, needle) || strings.Contains(f.Path, needle) {
			t.Fatalf("finding leaks needle: %+v", f)
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), needle) {
		t.Fatal("JSON receipt leaks needle bytes")
	}
}

func TestDecodedPatternsRejectRuntimeHostileValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rule    string
		content string
	}{
		{
			rule:    "absolute-path-content",
			content: "path=" + assembleHostilePath("Users", "alice", "secret") + "\n",
		},
		{
			rule:    "absolute-path-content",
			content: "path=" + assembleHostilePath("home", "bob", "secret") + "\n",
		},
		{
			rule:    "non-example-email",
			content: "reach me at " + assembleFragments("alice", "@", "company", ".", "com") + "\n",
		},
		{
			rule:    "credential-pattern",
			content: assembleFragments("-----BEGIN", " RSA PRIVATE KEY-----\n", "MIIB\n"),
		},
		{
			rule:    "url-credential",
			content: assembleFragments("https://", "user", ":", "secret", "@", "example.com/path\n"),
		},
		{
			rule:    "non-allowlisted-host",
			content: assembleFragments("see ", "https://", "github.com/org/repo\n"),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.rule, func(t *testing.T) {
			t.Parallel()
			z := buildZip(t, map[string]string{"provehito/runtime.txt": tc.content})
			result, err := releasecheck.Check(z, releasecheck.Options{})
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			assertFinding(t, result, tc.rule)
		})
	}
}

func TestDeterministicReceipt(t *testing.T) {
	t.Parallel()
	z := buildZip(t, map[string]string{
		"provehito/b.txt": "b\n",
		"provehito/a.txt": "a\n",
	})
	opts := releasecheck.Options{ExpectedFiles: []string{"a.txt", "b.txt"}}
	r1, err := releasecheck.Check(z, opts)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	r2, err := releasecheck.Check(z, opts)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	j1, _ := json.Marshal(r1)
	j2, _ := json.Marshal(r2)
	if string(j1) != string(j2) {
		t.Fatalf("non-deterministic receipt:\n%s\n!=\n%s", j1, j2)
	}
	if r1.SchemaVersion != releasecheck.SchemaVersion {
		t.Fatalf("schema version = %d, want %d", r1.SchemaVersion, releasecheck.SchemaVersion)
	}
	if r1.CheckerVersion != releasecheck.CheckerVersion {
		t.Fatalf("checker version = %q, want %q", r1.CheckerVersion, releasecheck.CheckerVersion)
	}
}

// --- test helpers ---

func assembleHostilePath(segments ...string) string {
	var b strings.Builder
	b.WriteByte('/')
	for i, seg := range segments {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(seg)
	}
	return b.String()
}

func assembleFragments(parts ...string) string {
	return strings.Join(parts, "")
}

func assertFinding(t *testing.T, result releasecheck.Result, rule string) {
	t.Helper()
	if result.StructuralStatus != releasecheck.StatusFail && result.PrivateStatus != releasecheck.StatusFail {
		t.Fatalf("expected FAIL finding for rule %q; structural=%q private=%q findings=%v",
			rule, result.StructuralStatus, result.PrivateStatus, result.Findings)
	}
	for _, f := range result.Findings {
		if f.Rule == rule {
			return
		}
	}
	t.Fatalf("no finding with rule %q; findings=%v", rule, result.Findings)
}

type rawEntry struct {
	name string
	body string
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var entries []rawEntry
	for name, body := range files {
		entries = append(entries, rawEntry{name: name, body: body})
	}
	return buildRawZip(t, entries)
}

func buildRawZip(t *testing.T, entries []rawEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, err := w.Create(e.name)
		if err != nil {
			t.Fatalf("create %q: %v", e.name, err)
		}
		if _, err := f.Write([]byte(e.body)); err != nil {
			t.Fatalf("write %q: %v", e.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return buf.Bytes()
}

func buildEncryptedZip(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.CreateHeader(&zip.FileHeader{
		Name:   name,
		Method: zip.Store,
		Flags:  0x1, // encrypted flag
	})
	if err != nil {
		t.Fatalf("create header: %v", err)
	}
	if _, err := f.Write([]byte(body)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return buf.Bytes()
}

func buildSymlinkZip(t *testing.T, name, target string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	h := &zip.FileHeader{
		Name:           name,
		Method:         zip.Store,
		CreatorVersion: 3<<8 | 20,
	}
	h.SetMode(fs.ModeSymlink | 0o777)
	f, err := w.CreateHeader(h)
	if err != nil {
		t.Fatalf("create header: %v", err)
	}
	if _, err := f.Write([]byte(target)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return buf.Bytes()
}

func buildZipWithDeclaredUncompressedSize(t *testing.T, name string, uncompressedSize uint64) []byte {
	t.Helper()
	nameBytes := []byte(name)
	body := []byte("x")
	compressedSize := uint64(len(body))
	crc := crc32.ChecksumIEEE(body)

	zip64Extra := make([]byte, 20)
	binary.LittleEndian.PutUint16(zip64Extra[0:2], 0x0001)
	binary.LittleEndian.PutUint16(zip64Extra[2:4], 16)
	binary.LittleEndian.PutUint64(zip64Extra[4:12], uncompressedSize)
	binary.LittleEndian.PutUint64(zip64Extra[12:20], compressedSize)

	var local bytes.Buffer
	writeU32(&local, 0x04034b50)
	writeU16(&local, 45)
	writeU16(&local, 0)
	writeU16(&local, 0)
	writeU16(&local, 0)
	writeU16(&local, 0)
	writeU32(&local, crc)
	writeU32(&local, 0xffffffff)
	writeU32(&local, 0xffffffff)
	writeU16(&local, uint16(len(nameBytes)))
	writeU16(&local, uint16(len(zip64Extra)))
	local.Write(nameBytes)
	local.Write(zip64Extra)
	local.Write(body)

	var cd bytes.Buffer
	writeU32(&cd, 0x02014b50)
	writeU16(&cd, 45)
	writeU16(&cd, 45)
	writeU16(&cd, 0)
	writeU16(&cd, 0)
	writeU16(&cd, 0)
	writeU16(&cd, 0)
	writeU32(&cd, crc)
	writeU32(&cd, 0xffffffff)
	writeU32(&cd, 0xffffffff)
	writeU16(&cd, uint16(len(nameBytes)))
	writeU16(&cd, uint16(len(zip64Extra)))
	writeU16(&cd, 0)
	writeU16(&cd, 0)
	writeU16(&cd, 0)
	writeU32(&cd, 0)
	writeU32(&cd, 0)
	cd.Write(nameBytes)
	cd.Write(zip64Extra)

	out := append(local.Bytes(), cd.Bytes()...)
	eocd := make([]byte, 22)
	binary.LittleEndian.PutUint32(eocd[0:4], 0x06054b50)
	binary.LittleEndian.PutUint16(eocd[8:10], 1)
	binary.LittleEndian.PutUint16(eocd[10:12], 1)
	binary.LittleEndian.PutUint32(eocd[12:16], uint32(cd.Len()))
	binary.LittleEndian.PutUint32(eocd[16:20], uint32(local.Len()))
	out = append(out, eocd...)

	reader, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("validate crafted zip: %v", err)
	}
	if len(reader.File) != 1 {
		t.Fatalf("crafted zip entries = %d, want 1", len(reader.File))
	}
	if reader.File[0].UncompressedSize64 != uncompressedSize {
		t.Fatalf("crafted UncompressedSize64 = %d, want %d", reader.File[0].UncompressedSize64, uncompressedSize)
	}
	return out
}

func writeU16(w *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	w.Write(b[:])
}

func writeU32(w *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.Write(b[:])
}

func buildHighRatioZip(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// Highly compressible content that exceeds 100:1 ratio when compressed.
	body := bytes.Repeat([]byte{0}, 200*1024)
	f, err := w.CreateHeader(&zip.FileHeader{
		Name:   name,
		Method: zip.Deflate,
	})
	if err != nil {
		t.Fatalf("create header: %v", err)
	}
	if _, err := f.Write(body); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return buf.Bytes()
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "denylist.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write denylist: %v", err)
	}
	return path
}
