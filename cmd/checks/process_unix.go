//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Cancelling a check must also stop the test binary, API/worker subprocesses
// and browser runner, rather than leaving them behind after killing only go.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		if err != nil {
			return err
		}
		// The browser runner forwards TERM to its owned detached browser group.
		// Give it bounded time to do so, then also stop TERM-ignoring members of
		// this group; exec.Cmd.WaitDelay alone kills only the direct parent.
		time.Sleep(250 * time.Millisecond)
		err = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
}

// A test binary can exit itself (including its own timeout) before the command
// context is cancelled. Its still-running helpers remain in our isolated group.
func finishProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
