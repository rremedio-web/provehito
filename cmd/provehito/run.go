package main

import (
	"fmt"
	"io"
)

const usageText = `usage: provehito [--json] <command>

  init                 create a private state root outside the workspace
  doctor               read-only checks for OS, Git, schema, and state
  lane open            record a complete dispatch and activate a lane
  lane list            read-only aggregate of current lane state
  lane validate|status read a manifest and its current hash
  lane block|resume|abandon|incident
  agent run            run one configured local process in the foreground
  freeze               bind a clean Git candidate to exact fingerprints
  evidence add|verify  add or verify content-addressed receipts
  review open|record   inspect the frozen candidate or record a verdict
  ready                report READY when review and evidence pass
  close                close a ready lane
  version              print version, commit, and build date
  help                 print this help

READY is a local workflow result, not authorization to merge or deploy.
`

func jsonRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
}

func globalCommand(args []string) string {
	for _, arg := range args {
		if arg == "--json" {
			continue
		}
		return arg
	}
	return ""
}

func runHelp(jsonOutput bool, stdout io.Writer) int {
	if jsonOutput {
		return writeResult(stdout, io.Discard, "help", true, map[string]any{"usage": usageText}, nil)
	}
	fmt.Fprint(stdout, usageText)
	return 0
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

	switch globalCommand(args) {
	case "":
		return writeResult(stdout, stderr, "", jsonRequested(args), nil, usageError("missing command"))
	case "help", "--help", "-h":
		return runHelp(jsonRequested(args), stdout)
	case "version", "--version":
		return runVersion(jsonRequested(args), stdout, stderr)
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
