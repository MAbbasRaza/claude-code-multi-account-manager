//go:build !windows

package proc

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// findProcs shells out to ps, which is present on both macOS and Linux and
// avoids a /proc parser that would only work on one of them.
func findProcs(match func(string) bool) ([]Process, error) {
	cmd := exec.Command("ps", "-A", "-o", "pid=,comm=")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list processes: %w: %s", err, strings.TrimSpace(errb.String()))
	}

	var res []Process
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sp := strings.Fields(line)
		if len(sp) < 2 {
			continue
		}
		pid, err := strconv.Atoi(sp[0])
		if err != nil {
			continue
		}
		// comm can be a full path depending on platform and invocation.
		name := filepath.Base(strings.Join(sp[1:], " "))
		if match(name) {
			res = append(res, Process{PID: pid, Name: name})
		}
	}
	return res, nil
}

// terminate sends SIGTERM, never SIGKILL. The program has to be allowed to run
// its shutdown path, because Claude Code writes .claude.json on the way out.
func terminate(p Process) error {
	proc, err := os.FindProcess(p.PID)
	if err != nil {
		return fmt.Errorf("find pid %d: %w", p.PID, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("ask pid %d to close: %w", p.PID, err)
	}
	return nil
}

// alive reports whether a pid is still running. Signal 0 performs the
// permission and existence checks without delivering anything.
//
// A zombie reads as alive, because its entry stays in the process table until
// its parent collects it. That is correct for every caller here: ccm only ever
// asks about processes it did not start, and those are reaped by whoever did.
func alive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
