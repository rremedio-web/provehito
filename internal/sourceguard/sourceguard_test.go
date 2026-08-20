package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckSourceRejectsOutwardActionsAndAllowsConfiguredLaunches(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "git network mutation",
			src:  `package fixture; import "os/exec"; func run() { exec.Command("git", "push") }`,
			want: "git network mutation",
		},
		{
			name: "git network mutation with context",
			src:  `package fixture; import ("context"; "os/exec"); func run() { exec.CommandContext(context.Background(), "git", "push") }`,
			want: "git network mutation",
		},
		{
			name: "socket listener",
			src:  `package fixture; import "net"; func run() { net.Listen("tcp", ":0") }`,
			want: "socket listener",
		},
		{
			name: "socket dialer",
			src:  `package fixture; import n "net"; func run() { n.Dial("tcp", "example.com:443") }`,
			want: "socket listener/dialer",
		},
		{
			name: "shell interpreter",
			src:  `package fixture; import x "os/exec"; func run() { args := []string{"-c", "echo unsafe"}; x.Command("sh", args...) }`,
			want: "shell interpreter",
		},
		{
			name: "credential api",
			src:  `package fixture; import "os/user"; func run() { user.Current() }`,
			want: "credential API",
		},
		{
			name: "dynamic exec",
			src:  `package fixture; import "os/exec"; func run(executable string, args []string) { exec.Command(executable, args...) }`,
			want: "dynamic exec",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(path, []byte(tc.src), 0600); err != nil {
				t.Fatal(err)
			}
			findings, err := checkFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(findings) != 0 {
					t.Fatalf("expected allow, got %#v", findings)
				}
				return
			}
			if len(findings) == 0 || !strings.Contains(findings[0].Rule, tc.want) {
				t.Fatalf("got findings %#v, want %q", findings, tc.want)
			}
		})
	}
}

func TestCheckSourceDetectsAliasedAndSplitGitMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.go")
	src := `package fixture
import x "os/exec"
func run() {
	command := "git"
	args := []string{"push"}
	x.Command(command, args...)
}`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	findings, err := checkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 || !strings.Contains(findings[0].Rule, "dynamic exec") {
		t.Fatalf("got findings %#v, want dynamic exec", findings)
	}
}

func TestCheckFileRejectsEvilcoreSupervisorLookalike(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evilcore", "process", "supervisor.go")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	src := `package process
import "os/exec"
func run(executable string, args []string) { exec.Command(executable, args...) }
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	findings, err := checkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 || !strings.Contains(findings[0].Rule, "dynamic exec") {
		t.Fatalf("evilcore supervisor lookalike should be rejected: %#v", findings)
	}
}

func TestCheckFileAllowsSupervisorDynamicExec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core", "process", "supervisor.go")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	src := `package process
import "os/exec"
func run(executable string, args []string) { exec.CommandContext(nil, executable, args...) }
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	findings, err := checkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("supervisor dynamic exec should be allowed: %#v", findings)
	}
}

func TestCheckFileAllowsHardenedGitExceptions(t *testing.T) {
	cases := []struct {
		name string
		path string
		src  string
	}{
		{
			name: "fingerprint runner",
			path: filepath.Join("core", "fingerprint", "runner.go"),
			src: `package fingerprint
import "os/exec"
func run(ctx context.Context, executable string, hardened []string) {
	exec.CommandContext(ctx, executable, hardened...)
}
`,
		},
		{
			name: "commands state git version",
			path: filepath.Join("cmd", "provehito", "commands_state.go"),
			src: `package main
import "os/exec"
func run(gitPath string) {
	exec.Command(gitPath, "--version")
}
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.path)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.src), 0600); err != nil {
				t.Fatal(err)
			}
			findings, err := checkFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 0 {
				t.Fatalf("hardened git exception should be allowed: %#v", findings)
			}
		})
	}
}

func TestCheckDirScansEngineSourcesAndSkipsTests(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "engine"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "engine", "safe.go"), []byte(`package engine
func run() {}
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "engine", "fixture_test.go"), []byte(`package engine
import "net"
func testOnly() { net.Listen("tcp", ":0") }
`), 0600); err != nil {
		t.Fatal(err)
	}
	findings, err := checkDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("test-only source should not be guarded: %#v", findings)
	}
}

func TestCheckDirSkipsTestdata(t *testing.T) {
	root := t.TempDir()
	testdataDir := filepath.Join(root, "testdata", "fake-agent")
	if err := os.MkdirAll(testdataDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testdataDir, "main.go"), []byte(`package main
import "net"
func main() { net.Listen("tcp", ":0") }
`), 0600); err != nil {
		t.Fatal(err)
	}
	findings, err := checkDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("testdata sources should be skipped: %#v", findings)
	}
}

func TestCheckSourceRejectsProcessSpawningAPIs(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "os start process",
			src:  `package fixture; import "os"; func run() { os.StartProcess("sh", []string{"sh"}, nil) }`,
			want: "process spawn",
		},
		{
			name: "syscall exec",
			src:  `package fixture; import "syscall"; func run() { syscall.Exec("/bin/sh", []string{"/bin/sh"}, nil) }`,
			want: "process spawn",
		},
		{
			name: "syscall fork exec",
			src:  `package fixture; import "syscall"; func run() { syscall.ForkExec("/bin/sh", []string{"/bin/sh"}, nil) }`,
			want: "process spawn",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(path, []byte(tc.src), 0600); err != nil {
				t.Fatal(err)
			}
			findings, err := checkFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) == 0 || !strings.Contains(findings[0].Rule, tc.want) {
				t.Fatalf("got findings %#v, want %q", findings, tc.want)
			}
		})
	}
}

func TestCheckSourceRejectsAliasedHTTPImportOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.go")
	src := `package fixture
import h "net/http"
func run() {}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	findings, err := checkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].Rule, "http client") {
		t.Fatalf("got findings %#v, want single http client", findings)
	}
}

func TestCheckSourceRejectsHTTPClientPatterns(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "net http import",
			src:  `package fixture; import "net/http"; func run() {}`,
		},
		{
			name: "http get",
			src:  `package fixture; import "net/http"; func run() { http.Get("https://example.com") }`,
		},
		{
			name: "default client",
			src:  `package fixture; import "net/http"; func run() { http.DefaultClient.Get("https://example.com") }`,
		},
		{
			name: "client transport",
			src:  `package fixture; import "net/http"; func run() { _ = &http.Client{Transport: &http.Transport{}} }`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(path, []byte(tc.src), 0600); err != nil {
				t.Fatal(err)
			}
			findings, err := checkFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) == 0 || !strings.Contains(findings[0].Rule, "http client") {
				t.Fatalf("got findings %#v, want http client", findings)
			}
			if len(findings) > 1 {
				t.Fatalf("got %d findings, want single http client finding: %#v", len(findings), findings)
			}
		})
	}
}

func TestSecurityWorkflowPinsAndPermissions(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	required := []string{
		"permissions:",
		"contents: read",
		`GITLEAKS_ENABLE_COMMENTS: "false"`,
		"gitleaks/gitleaks-action@e0c47f4f8be36e29cdc102c57e68cb5cbf0e8d1e",
		"github/codeql-action/init@ff2f1c621b7f889edc0d3c761ac2e6a3f8cdb0dd",
		"github/codeql-action/autobuild@ff2f1c621b7f889edc0d3c761ac2e6a3f8cdb0dd",
		"github/codeql-action/analyze@ff2f1c621b7f889edc0d3c761ac2e6a3f8cdb0dd",
		"golang.org/x/vuln/cmd/govulncheck@v1.7.0",
		"github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.11.0",
	}
	for _, needle := range required {
		if !strings.Contains(content, needle) {
			t.Fatalf("security workflow missing %q", needle)
		}
	}
	if strings.Contains(content, "security-events: write") && !strings.Contains(content, "codeql:") {
		t.Fatal("security-events write should be scoped to codeql job")
	}
	codeqlSection := content[strings.Index(content, "codeql:"):]
	if !strings.Contains(codeqlSection, "security-events: write") {
		t.Fatal("codeql job must grant security-events: write")
	}
	if strings.Count(content, "security-events: write") != 1 {
		t.Fatalf("security-events: write should appear once, got %d", strings.Count(content, "security-events: write"))
	}
	if strings.Contains(content, "comment: false") {
		t.Fatal("gitleaks comment input is unsupported; use GITLEAKS_ENABLE_COMMENTS")
	}
}
