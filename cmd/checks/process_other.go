//go:build !unix

package main

import "os/exec"

// The process-backed browser acceptance already requires POSIX. Ordinary
// formatting, compilation and unit checks retain CommandContext cancellation.
func configureProcess(_ *exec.Cmd) {}

func finishProcess(_ *exec.Cmd) error { return nil }
