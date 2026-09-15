// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// A cancelled generator or shell must not leave children writing after unlock.
func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}

		return err
	}
}
