package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/provehito-project/provehito/core/failure"
)

// result is deliberately a fixed-shape envelope. Field order is part of the
// CLI's deterministic JSON surface.
type result struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	Class   string `json:"class"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func writeResult(stdout, stderr io.Writer, command string, jsonOutput bool, data any, err error) int {
	if data == nil {
		data = map[string]any{}
	}
	if err == nil {
		r := result{OK: true, Command: command, Class: "OK", Message: "completed", Data: data}
		if jsonOutput {
			_ = json.NewEncoder(stdout).Encode(r)
		} else {
			fmt.Fprintf(stdout, "RESULT: OK %s\n", command)
			fmt.Fprintln(stdout, "Evidence: completed")
		}
		return 0
	}

	class, operation := errorDetails(err)
	message := operation
	if message == "" {
		message = "command failed"
	}
	r := result{OK: false, Command: command, Class: string(class), Message: message, Data: data}
	if jsonOutput {
		_ = json.NewEncoder(stdout).Encode(r)
	} else {
		fmt.Fprintf(stdout, "RESULT: ERROR %s [%s]\n", command, class)
		fmt.Fprintf(stdout, "Correction: %s\n", correctionFor(class))
	}
	_ = stderr
	return failure.ExitCodeFor(err)
}

func errorDetails(err error) (failure.Class, string) {
	var classified *failure.Error
	if errors.As(err, &classified) {
		return classified.Class, classified.Op
	}
	return failure.UsageOrSchema, "invalid command input"
}

func correctionFor(class failure.Class) string {
	switch class {
	case failure.Integrity:
		return "inspect the state root and correct the integrity failure"
	case failure.PolicyOrTransition:
		return "use a declared lifecycle transition"
	case failure.ToolingOrAdapter:
		return "install or repair the required local tooling"
	default:
		return "correct the command inputs and retry"
	}
}

func usageError(op string) error { return failure.New(failure.UsageOrSchema, op) }
