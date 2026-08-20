// Command releasereceipt writes a deterministic release receipt JSON file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
)

func main() {
	output := flag.String("output", "", "output receipt path")
	head := flag.String("head", "", "git HEAD SHA")
	tree := flag.String("tree", "", "git tree SHA")
	trackedCount := flag.Int("tracked-count", 0, "tracked file count")
	memberCount := flag.Int("member-count", 0, "archive member count")
	archiveHash := flag.String("archive-hash", "", "archive SHA-256")
	build1 := flag.String("archive-hash-build1", "", "first build SHA-256")
	build2 := flag.String("archive-hash-build2", "", "second build SHA-256")
	structural := flag.String("structural-status", "", "structural scan status")
	private := flag.String("private-status", "", "private scan status")
	gitVersion := flag.String("git-version", "", "git version string")
	goVersion := flag.String("go-version", "", "go version string")
	flag.Parse()

	if *output == "" {
		fmt.Fprintf(os.Stderr, "releasereceipt: --output is required\n")
		os.Exit(2)
	}

	receipt := map[string]any{
		"schema_version":      1,
		"checker_version":     "1.0.0",
		"head":                *head,
		"tree":                *tree,
		"tracked_count":       *trackedCount,
		"member_count":        *memberCount,
		"archive_hash":        *archiveHash,
		"archive_hash_build1": *build1,
		"archive_hash_build2": *build2,
		"structural_status":   *structural,
		"private_status":      *private,
		"git_version":         *gitVersion,
		"go_version":          *goVersion,
	}

	keys := make([]string, 0, len(receipt))
	for k := range receipt {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	ordered := make(map[string]any, len(receipt))
	for _, k := range keys {
		ordered[k] = receipt[k]
	}

	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "releasereceipt: %v\n", err)
		os.Exit(2)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "releasereceipt: %v\n", err)
		os.Exit(2)
	}
}
