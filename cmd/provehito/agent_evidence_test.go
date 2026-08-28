package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/provehito-project/provehito/core/evidence"
)

func syntheticTokenSecret() string {
	prefix := "Bearer" + " "
	token := "sk-" + "test-" + "token-" + "abcdef" + "1234567890"
	return prefix + token
}

func syntheticPEMSecret() string {
	begin := "-----" + "BEGIN " + "PRIVATE KEY" + "-----"
	body := " synthetic." + "example." + "com "
	end := "-----" + "END " + "PRIVATE KEY" + "-----"
	return begin + body + end
}

func syntheticURLSecret() string {
	scheme := "https" + "://"
	cred := "user" + ":" + "pass"
	host := "host." + "example." + "com"
	return scheme + cred + "@" + host + "/path"
}

func syntheticPlainSecret() string {
	return "secret" + "=" + "plain-" + "secret-" + "value"
}

func TestAgentRunDoesNotPersistSubprocessPlaintext(t *testing.T) {
	secrets := []string{syntheticTokenSecret(), syntheticPEMSecret(), syntheticURLSecret(), syntheticPlainSecret()}
	repo := newCleanGitFixture(t)
	state := t.TempDir()
	mustCLI(t, state, "init", "--workspace", repo)
	mustCLI(t, state, "lane", "open", "--id", "demo", "--workspace", repo, "--writer", "writer-1", "--family", "family-a", "--seat-id", "writer-seat", "--source-control", "git", "--adapter", "local", "--cost-class", "economy", "--allowed-paths", "cmd", "--forbidden-paths", "none", "--non-goals", "deploy", "--required-checks", "fixture-check", "--review-policy", "independent", "--max-seconds", "5", "--max-output-bytes", "4096", "--max-memory-bytes", "0")
	profile := fakeProfile(t)
	args := []string{
		"agent", "run", "--lane", "demo", "--profile", profile,
		"--profile-id", "local", "--family", "family-a", "--seat-id", "writer-seat", "--cost-class", "economy",
		"--capability", "writer", "--timeout", "5s", "--output-bytes", "4096",
		"--arg", "--print-secrets", "--state", state, "--json",
	}
	var out bytes.Buffer
	if code := Run(args, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("agent run: code=%d stdout=%s", code, out.String())
	}
	cliText := out.String()
	for _, secret := range secrets {
		if strings.Contains(cliText, secret) {
			t.Fatalf("CLI output leaked subprocess secret %q", secret)
		}
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for key, value := range envelope.Data {
		if strings.Contains(key, "stdout") || strings.Contains(key, "stderr") {
			if text, ok := value.(string); ok && len(text) > 0 && !isLowerHexHash(text) {
				t.Fatalf("CLI data field %q contained raw subprocess bytes: %q", key, text)
			}
		}
	}
	receiptHash, _ := envelope.Data["receipt"].(string)
	if receiptHash == "" {
		t.Fatal("missing receipt hash")
	}
	receiptPath := filepath.Join(state, "evidence", "sha256", receiptHash[:2], receiptHash+".json")
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	receiptText := string(receiptBytes)
	for _, secret := range secrets {
		if strings.Contains(receiptText, secret) {
			t.Fatalf("receipt leaked subprocess secret %q", secret)
		}
	}
	loaded, err := evidence.NewStore(state).Load(evidence.Reference{Hash: receiptHash})
	if err != nil {
		t.Fatal(err)
	}
	tokenSecret, plainSecret := syntheticTokenSecret(), syntheticPlainSecret()
	if strings.Contains(loaded.Probe, tokenSecret) || strings.Contains(loaded.Probe, plainSecret) {
		t.Fatalf("probe contained subprocess plaintext: %q", loaded.Probe)
	}
	if loaded.SeatID != "writer-seat" {
		t.Fatalf("receipt seat_id: got %q", loaded.SeatID)
	}
	stdoutRef, stderrRef := artifactRef(loaded.Artifacts, "stdout"), artifactRef(loaded.Artifacts, "stderr")
	if stdoutRef == nil || stderrRef == nil {
		t.Fatalf("missing stdout/stderr artifact refs: %#v", loaded.Artifacts)
	}
	stdoutPayload := strings.Join([]string{syntheticTokenSecret() + "\n", syntheticPEMSecret() + "\n"}, "")
	stderrPayload := strings.Join([]string{syntheticURLSecret() + "\n", syntheticPlainSecret() + "\n"}, "")
	stdoutDigest := sha256.Sum256([]byte(stdoutPayload))
	stderrDigest := sha256.Sum256([]byte(stderrPayload))
	if stdoutRef.Hash != hex.EncodeToString(stdoutDigest[:]) || stderrRef.Hash != hex.EncodeToString(stderrDigest[:]) {
		t.Fatalf("artifact hashes: stdout=%s stderr=%s want stdout=%x stderr=%x", stdoutRef.Hash, stderrRef.Hash, stdoutDigest, stderrDigest)
	}
	if stdoutHash, ok := envelope.Data["stdout_hash"].(string); !ok || stdoutHash != stdoutRef.Hash {
		t.Fatalf("stdout_hash in CLI data: got %v want %s", envelope.Data["stdout_hash"], stdoutRef.Hash)
	}
	if stderrHash, ok := envelope.Data["stderr_hash"].(string); !ok || stderrHash != stderrRef.Hash {
		t.Fatalf("stderr_hash in CLI data: got %v want %s", envelope.Data["stderr_hash"], stderrRef.Hash)
	}
}

func TestAgentRunPrelaunchFailureUsesEmptyStreamHashes(t *testing.T) {
	repo := newCleanGitFixture(t)
	state := t.TempDir()
	mustCLI(t, state, "init", "--workspace", repo)
	mustCLI(t, state, "lane", "open", "--id", "demo", "--workspace", repo, "--writer", "writer-1", "--family", "family-a", "--seat-id", "writer-seat", "--source-control", "git", "--adapter", "local", "--cost-class", "economy", "--allowed-paths", "cmd", "--forbidden-paths", "none", "--non-goals", "deploy", "--required-checks", "fixture-check", "--review-policy", "independent", "--max-seconds", "5", "--max-output-bytes", "4096", "--max-memory-bytes", "0")
	missing := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(missing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"agent", "run", "--lane", "demo", "--profile", missing,
		"--profile-id", "local", "--family", "family-a", "--seat-id", "writer-seat", "--cost-class", "economy",
		"--capability", "writer", "--timeout", "1s", "--output-bytes", "32",
		"--state", state, "--json",
	}
	var out bytes.Buffer
	code := Run(args, &out, &bytes.Buffer{})
	if code != 40 {
		t.Fatalf("expected tooling failure, got %d stdout=%s", code, out.String())
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	emptyHash := hex.EncodeToString(sha256.New().Sum(nil))
	if stdoutHash, ok := envelope.Data["stdout_hash"].(string); !ok || stdoutHash != emptyHash {
		t.Fatalf("stdout_hash: got %v want %s", envelope.Data["stdout_hash"], emptyHash)
	}
	if stderrHash, ok := envelope.Data["stderr_hash"].(string); !ok || stderrHash != emptyHash {
		t.Fatalf("stderr_hash: got %v want %s", envelope.Data["stderr_hash"], emptyHash)
	}
	receiptHash, _ := envelope.Data["receipt"].(string)
	if receiptHash == "" {
		t.Fatal("missing receipt hash on prelaunch failure")
	}
	loaded, err := evidence.NewStore(state).Load(evidence.Reference{Hash: receiptHash})
	if err != nil {
		t.Fatal(err)
	}
	stdoutRef, stderrRef := artifactRef(loaded.Artifacts, "stdout"), artifactRef(loaded.Artifacts, "stderr")
	if stdoutRef == nil || stderrRef == nil || stdoutRef.Hash != emptyHash || stderrRef.Hash != emptyHash {
		t.Fatalf("artifact refs on prelaunch failure: stdout=%#v stderr=%#v want %s", stdoutRef, stderrRef, emptyHash)
	}
}

func artifactRef(refs []evidence.Reference, name string) *evidence.Reference {
	for i := range refs {
		if refs[i].Name == name {
			return &refs[i]
		}
	}
	return nil
}

func isLowerHexHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
