//go:build !darwin && !linux

package process

import (
	"errors"
	"os/exec"
)

func configureProcessGroup(command *exec.Cmd) {}

func terminateProcessGroup(pid int) error {
	return errors.New("process groups are unsupported on this platform")
}
