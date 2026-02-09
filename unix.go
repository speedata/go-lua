//go:build !windows
// +build !windows

package lua

import (
	"os/exec"
	"syscall"
)

func clock(l *State) int {
	var rusage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &rusage) // ignore errors
	l.PushNumber(float64(rusage.Utime.Sec+rusage.Stime.Sec) + float64(rusage.Utime.Usec+rusage.Stime.Usec)/1000000.0)
	return 1
}

func exitReasonAndCode(exitErr *exec.ExitError) (string, int) {
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return "signal", int(status.Signal())
	}
	return "exit", exitErr.ExitCode()
}
