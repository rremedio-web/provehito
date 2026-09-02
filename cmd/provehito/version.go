package main

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

// version, commit, and date are overridden by -ldflags at release build time.
// Empty values are filled from Go build info so `go install` still reports
// module version and VCS metadata.
var (
	version = ""
	commit  = ""
	date    = ""
)

type versionInfo struct {
	Version string
	Commit  string
	Date    string
}

func resolveVersion() versionInfo {
	info := versionInfo{
		Version: firstNonEmpty(version, "dev"),
		Commit:  firstNonEmpty(commit, "unknown"),
		Date:    firstNonEmpty(date, "unknown"),
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if version == "" {
		mod := bi.Main.Version
		if mod != "" && mod != "(devel)" {
			info.Version = strings.TrimPrefix(mod, "v")
		}
	}
	if commit != "" && date != "" {
		return info
	}
	for _, setting := range bi.Settings {
		switch setting.Key {
		case "vcs.revision":
			if commit == "" && setting.Value != "" {
				info.Commit = setting.Value
			}
		case "vcs.time":
			if date == "" && setting.Value != "" {
				info.Date = setting.Value
			}
		}
	}
	return info
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func runVersion(jsonOutput bool, stdout, stderr io.Writer) int {
	info := resolveVersion()
	data := map[string]any{
		"version": info.Version,
		"commit":  info.Commit,
		"date":    info.Date,
	}
	if jsonOutput {
		return writeResult(stdout, stderr, "version", true, data, nil)
	}
	fmt.Fprintf(stdout, "RESULT: OK version\n")
	fmt.Fprintf(stdout, "version: %s\n", info.Version)
	fmt.Fprintf(stdout, "commit: %s\n", info.Commit)
	fmt.Fprintf(stdout, "date: %s\n", info.Date)
	return 0
}
