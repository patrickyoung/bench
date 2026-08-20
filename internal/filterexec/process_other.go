//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package filterexec

import (
	"os"
	"os/exec"
)

func configureProcess(*exec.Cmd) {}

func interruptProcess(cmd *exec.Cmd) error {
	return cmd.Process.Signal(os.Interrupt)
}
