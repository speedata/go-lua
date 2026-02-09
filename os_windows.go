package lua

import "os/exec"

func clock(l *State) int {
	Errorf(l, "os.clock not yet supported on Windows")
	panic("unreachable")
}

func exitReasonAndCode(exitErr *exec.ExitError) (string, int) {
	return "exit", exitErr.ExitCode()
}
