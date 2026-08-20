package main

import (
	"fmt"
	"io"
)

func jsonRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
}

// Run executes one explicit CLI command and returns its stable process exit
// code. All state roots are supplied by the command being run.
func Run(args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) == 0 || args[0] == "help" {
		if len(args) == 1 && args[0] == "help" {
			fmt.Fprintln(stdout, "usage: provehito init|doctor|lane <open|validate|status|block|resume|abandon|incident>")
			return 0
		}
		return writeResult(stdout, stderr, "", jsonRequested(args), nil, usageError("missing command"))
	}

	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "lane":
		if len(args) < 2 {
			return writeResult(stdout, stderr, "lane", false, nil, usageError("lane operation required"))
		}
		return runLane(args[1], args[2:], stdout, stderr)
	case "agent":
		if len(args) < 2 || args[1] != "run" {
			return writeResult(stdout, stderr, "agent", jsonRequested(args[1:]), nil, usageError("agent run required"))
		}
		return runAgent(args[2:], stdout, stderr)
	case "freeze":
		return runFreeze(args[1:], stdout, stderr)
	case "evidence":
		if len(args) < 2 || (args[1] != "add" && args[1] != "verify") {
			return writeResult(stdout, stderr, "evidence", jsonRequested(args[1:]), nil, usageError("evidence add or verify required"))
		}
		return runEvidence(args[1], args[2:], stdout, stderr)
	case "review":
		if len(args) < 2 || (args[1] != "open" && args[1] != "record") {
			return writeResult(stdout, stderr, "review", jsonRequested(args[1:]), nil, usageError("review open or record required"))
		}
		return runReview(args[1], args[2:], stdout, stderr)
	case "ready":
		return runReady(args[1:], stdout, stderr)
	case "close":
		return runClose(args[1:], stdout, stderr)
	default:
		return writeResult(stdout, stderr, args[0], jsonRequested(args[1:]), nil, usageError("unknown command"))
	}
}
