// Package releasecheck validates release ZIP archives without extracting or
// executing archive contents.
package releasecheck

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	SchemaVersion           = 1
	CheckerVersion          = "1.0.0"
	RequiredPrefix          = "provehito/"
	maxFileSize      uint64 = 32 * 1024 * 1024
	maxTotalSize     uint64 = 32 * 1024 * 1024
	maxCompressRatio        = 100.0
)

const (
	StatusPass    = "PASS"
	StatusFail    = "FAIL"
	StatusSkipped = "SKIPPED"
)

// Options configures a release archive check.
type Options struct {
	ExpectedFiles       []string
	PrivateDenylistPath string
}

// Finding records one validation failure without leaking sensitive bytes.
type Finding struct {
	Rule string `json:"rule"`
	Path string `json:"path,omitempty"`
}

// Result is a deterministic structured receipt suitable for release certification.
type Result struct {
	SchemaVersion    int       `json:"schema_version"`
	CheckerVersion   string    `json:"checker_version"`
	StructuralStatus string    `json:"structural_status"`
	PrivateStatus    string    `json:"private_status"`
	Findings         []Finding `json:"findings,omitempty"`
	TrackedCount     int       `json:"tracked_count"`
	MemberCount      int       `json:"member_count"`
}

// Check validates a ZIP archive in memory.
func Check(data []byte, opts Options) (Result, error) {
	result := Result{
		SchemaVersion:    SchemaVersion,
		CheckerVersion:   CheckerVersion,
		StructuralStatus: StatusPass,
		PrivateStatus:    StatusSkipped,
	}
	if opts.PrivateDenylistPath != "" {
		result.PrivateStatus = StatusPass
	}

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return result, fmt.Errorf("zip open: %w", err)
	}

	var denylist []string
	if opts.PrivateDenylistPath != "" {
		needles, err := loadDenylist(opts.PrivateDenylistPath)
		if err != nil {
			return result, fmt.Errorf("denylist: %w", err)
		}
		denylist = needles
	}

	var findings []Finding
	var members []string
	var totalUncompressed uint64
	seen := make(map[string]string)
	foldSeen := make(map[string]string)

	for _, file := range reader.File {
		name := file.Name

		if strings.Contains(name, "\\") {
			findings = append(findings, Finding{Rule: "backslash-path", Path: sanitizePath(name)})
			continue
		}
		if strings.HasPrefix(name, "/") || path.IsAbs(name) {
			findings = append(findings, Finding{Rule: "absolute-path", Path: sanitizePath(name)})
			continue
		}
		if hasControlName(name) {
			findings = append(findings, Finding{Rule: "control-name", Path: sanitizePath(name)})
			continue
		}
		if containsTraversal(name) {
			findings = append(findings, Finding{Rule: "path-traversal", Path: sanitizePath(name)})
			continue
		}
		if file.Flags&0x1 != 0 {
			findings = append(findings, Finding{Rule: "encrypted-entry", Path: sanitizePath(name)})
			continue
		}
		mode := file.Mode()
		if mode&fs.ModeSymlink != 0 || (!mode.IsRegular() && !strings.HasSuffix(name, "/")) {
			findings = append(findings, Finding{Rule: "non-regular-entry", Path: sanitizePath(name)})
			continue
		}
		if prev, ok := seen[name]; ok {
			findings = append(findings, Finding{Rule: "duplicate-name", Path: sanitizePath(name)})
			_ = prev
			continue
		}
		seen[name] = name
		fold := strings.ToLower(name)
		if prev, ok := foldSeen[fold]; ok && prev != name {
			findings = append(findings, Finding{Rule: "case-fold-collision", Path: sanitizePath(name)})
			continue
		}
		foldSeen[fold] = name

		if rule := forbiddenRule(name); rule != "" {
			findings = append(findings, Finding{Rule: rule, Path: sanitizePath(name)})
			continue
		}

		uncompressed := file.UncompressedSize64
		if uncompressed > maxFileSize {
			findings = append(findings, Finding{Rule: "file-size-limit", Path: sanitizePath(name)})
			continue
		}
		if uncompressed > maxTotalSize-totalUncompressed {
			findings = append(findings, Finding{Rule: "total-size-limit", Path: sanitizePath(name)})
			continue
		}
		totalUncompressed += uncompressed

		if file.CompressedSize64 > 0 && float64(uncompressed)/float64(file.CompressedSize64) > maxCompressRatio {
			findings = append(findings, Finding{Rule: "compression-ratio", Path: sanitizePath(name)})
			continue
		}

		if !strings.HasPrefix(name, RequiredPrefix) {
			findings = append(findings, Finding{Rule: "missing-prefix", Path: sanitizePath(name)})
			continue
		}

		rel := strings.TrimPrefix(name, RequiredPrefix)
		if rel == "" || strings.HasSuffix(rel, "/") {
			continue
		}
		members = append(members, rel)

		content, err := readFileContent(file)
		if err != nil {
			findings = append(findings, Finding{Rule: "read-error", Path: sanitizePath(name)})
			continue
		}
		findings = append(findings, scanContent(rel, content)...)
		if len(denylist) > 0 {
			findings = append(findings, scanDenylist(rel, content, denylist)...)
		}
	}

	if len(opts.ExpectedFiles) > 0 {
		sort.Strings(members)
		expected := append([]string(nil), opts.ExpectedFiles...)
		sort.Strings(expected)
		if !equalStringSlices(members, expected) {
			findings = append(findings, Finding{Rule: "expected-list-mismatch"})
		}
	}

	result.MemberCount = len(members)
	result.TrackedCount = len(opts.ExpectedFiles)
	result.Findings = sortFindings(findings)

	hasStructural := false
	hasPrivate := false
	for _, f := range result.Findings {
		if f.Rule == "private-denylist" {
			hasPrivate = true
		} else {
			hasStructural = true
		}
	}
	if hasStructural {
		result.StructuralStatus = StatusFail
	}
	if hasPrivate {
		result.PrivateStatus = StatusFail
	}
	if opts.PrivateDenylistPath == "" {
		result.PrivateStatus = StatusSkipped
	}

	return result, nil
}

// CanonicalJSON returns deterministic JSON bytes for a result.
func CanonicalJSON(r Result) ([]byte, error) {
	r.Findings = sortFindings(r.Findings)
	return json.Marshal(r)
}

func loadDenylist(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("denylist unreadable: %w", err)
	}
	var needles []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		needles = append(needles, line)
	}
	return needles, nil
}

func readFileContent(file *zip.File) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, int64(maxFileSize+1)))
}

func hasControlName(name string) bool {
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func containsTraversal(name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func sanitizePath(p string) string {
	// Strip control chars for safe output without leaking content.
	var b strings.Builder
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			b.WriteRune('?')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func forbiddenRule(name string) string {
	rel := strings.TrimPrefix(name, RequiredPrefix)
	base := path.Base(rel)
	segments := strings.Split(rel, "/")

	for _, seg := range segments {
		switch {
		case seg == ".git" || seg == ".superpowers" || seg == "__MACOSX":
			return "forbidden-segment"
		case seg == ".DS_Store":
			return "forbidden-name"
		case strings.HasPrefix(seg, ".env"):
			return "forbidden-name"
		case strings.HasSuffix(seg, ".bundle"):
			return "forbidden-name"
		case isForbiddenSegment(seg):
			return "forbidden-segment"
		}
	}
	if base == ".DS_Store" {
		return "forbidden-name"
	}
	if strings.HasSuffix(base, ".bundle") {
		return "forbidden-name"
	}
	if strings.HasPrefix(base, ".env") {
		return "forbidden-name"
	}
	if isGitObjectShape(segments) {
		return "forbidden-segment"
	}
	return ""
}

func isForbiddenSegment(seg string) bool {
	switch seg {
	case ".idea", ".vscode", ".cache", "__pycache__", "node_modules",
		"dist", "build", "bin", "target", "htmlcov", ".pytest_cache",
		"test-results", "coverage", "vendor":
		return true
	}
	lower := strings.ToLower(seg)
	if strings.HasSuffix(lower, ".swp") || strings.HasSuffix(lower, "~") {
		return true
	}
	if strings.HasPrefix(lower, ".#") {
		return true
	}
	ext := path.Ext(lower)
	switch ext {
	case ".coverprofile", ".prof", ".pprof", ".out", ".test", ".bak", ".orig":
		return true
	}
	if lower == "coverage.out" || lower == "profile.out" {
		return true
	}
	return false
}

func isGitObjectShape(segments []string) bool {
	for i, seg := range segments {
		if seg != "objects" && seg != "refs" && seg != "logs" {
			continue
		}
		if i > 0 && segments[i-1] == ".git" {
			return true
		}
	}
	return false
}

var (
	absPathContentRE = mustCompilePattern("KF58W15bOmFsbnVtOl1fXSkvKFVzZXJzfGhvbWUpL1teWzpzcGFjZTpdIjw+XSs=")
	emailRE          = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	credentialRE     = mustCompilePattern("LS0tLS1CRUdJTltbOnNwYWNlOl1dK1tBLVowLTkgXSpQUklWQVRFIEtFWS0tLS0tfFxiKEFLSUF8QVNJQSlbQS1aMC05XXsxNn1cYnxcYmdoW3BvdXNyXV9bQS1aYS16MC05X117MjAsfVxifFxiZ2l0aHViX3BhdF9bQS1aYS16MC05X117MjAsfVxifFxieG94W2JhcHJzXS1bQS1aYS16MC05LV17MTYsfVxifFxiQmVhcmVyW1s6c3BhY2U6XV0rW0EtWmEtejAtOS5ffisvPS1dezIwLH1cYg==")
	urlCredentialRE  = mustCompilePattern("Oi8vW146L1s6c3BhY2U6XV0rOlteQFs6c3BhY2U6XV0rQA==")
	urlHostRE        = regexp.MustCompile(`https?://[^[:space:]"<>]+`)
)

func mustCompilePattern(encoded string) *regexp.Regexp {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		panic(fmt.Sprintf("releasecheck pattern: %v", err))
	}
	return regexp.MustCompile(string(data))
}

func binaryAssetExt(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".gif", ".png", ".jpg", ".jpeg", ".webp":
		return true
	default:
		return false
	}
}

func scanContent(rel string, content []byte) []Finding {
	if binaryAssetExt(rel) {
		return nil
	}
	var findings []Finding
	if bytes.IndexByte(content, 0) >= 0 {
		findings = append(findings, Finding{Rule: "nul-content", Path: rel})
	}
	text := string(content)
	if absPathContentRE.MatchString(text) {
		findings = append(findings, Finding{Rule: "absolute-path-content", Path: rel})
	}
	for _, match := range emailRE.FindAllString(text, -1) {
		at := strings.LastIndex(match, "@")
		if at < 0 {
			continue
		}
		domain := strings.ToLower(match[at+1:])
		if domain == "example.com" || domain == "example.org" || domain == "example.net" || domain == "example.invalid" {
			continue
		}
		findings = append(findings, Finding{Rule: "non-example-email", Path: rel})
		break
	}
	if credentialRE.MatchString(text) {
		findings = append(findings, Finding{Rule: "credential-pattern", Path: rel})
	}
	if urlCredentialRE.MatchString(text) {
		findings = append(findings, Finding{Rule: "url-credential", Path: rel})
	}
	for _, match := range urlHostRE.FindAllString(text, -1) {
		host := extractHost(match)
		if host != "" && !isAllowlistedHost(host) {
			findings = append(findings, Finding{Rule: "non-allowlisted-host", Path: rel})
			break
		}
	}
	return findings
}

func scanDenylist(rel string, content []byte, needles []string) []Finding {
	text := string(content)
	for _, needle := range needles {
		if needle == "" {
			continue
		}
		if strings.Contains(text, needle) {
			return []Finding{{Rule: "private-denylist", Path: rel}}
		}
	}
	return nil
}

func extractHost(rawURL string) string {
	rest := strings.TrimPrefix(rawURL, "https://")
	rest = strings.TrimPrefix(rest, "http://")
	host := rest
	if idx := strings.Index(host, "/"); idx >= 0 {
		host = host[:idx]
	}
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	host = strings.Trim(host, `"'`)
	return strings.ToLower(host)
}

func isAllowlistedHost(host string) bool {
	host = strings.ToLower(host)
	allowed := []string{
		"example.com", "example.org", "example.net",
		"localhost", "127.0.0.1",
		"json-schema.org", "apache.org",
	}
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortFindings(findings []Finding) []Finding {
	if len(findings) == 0 {
		return nil
	}
	sorted := append([]Finding(nil), findings...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Rule != sorted[j].Rule {
			return sorted[i].Rule < sorted[j].Rule
		}
		return sorted[i].Path < sorted[j].Path
	})
	return sorted
}
