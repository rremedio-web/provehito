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
	OK         bool   `json:"ok"`
	Command    string `json:"command"`
	Class      string `json:"class"`
	Message    string `json:"message"`
	Correction string `json:"correction,omitempty"`
	Data       any    `json:"data"`
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
	r := result{OK: false, Command: command, Class: string(class), Message: message, Correction: correctionFor(err, class), Data: data}
	if jsonOutput {
		_ = json.NewEncoder(stdout).Encode(r)
	} else {
		fmt.Fprintf(stdout, "RESULT: ERROR %s [%s]\n", command, class)
		fmt.Fprintf(stdout, "Correction: %s\n", correctionFor(err, class))
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

// correctionFor keys the remedy on the failure's typed reason, so a renamed
// operation string can no longer silently degrade the correction.
func correctionFor(err error, class failure.Class) string {
	switch failure.ReasonFor(err) {
	case failure.ReasonReviewerFamily:
		return "set --family on review record to a value different from the dispatch family used by the writer"
	case failure.ReasonReviewerSeat:
		return "set --seat-id on review record to a seat different from the writer seat"
	case failure.ReasonWriterSeat:
		return "set --seat-id or PROVEHITO_SEAT_ID to the writer seat declared by lane open"
	}
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
