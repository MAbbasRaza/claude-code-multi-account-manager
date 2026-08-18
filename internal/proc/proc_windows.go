//go:build windows

package proc

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// findProcs walks the process list with the Toolhelp32 snapshot API. This
// avoids shelling out to tasklist, which is slow and locale-dependent.
func findProcs(match func(string) bool) ([]Process, error) {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("process snapshot: %w", err)
	}
	defer syscall.CloseHandle(snap)

	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe_Sizeof_ProcessEntry32())

	if err := syscall.Process32First(snap, &entry); err != nil {
		return nil, fmt.Errorf("first process: %w", err)
	}

	var out []Process
	for {
		name := syscall.UTF16ToString(entry.ExeFile[:])
		if match(name) {
			out = append(out, Process{PID: int(entry.ProcessID), Name: name})
		}
		if err := syscall.Process32Next(snap, &entry); err != nil {
			if err == syscall.ERROR_NO_MORE_FILES {
				break
			}
			return nil, fmt.Errorf("next process: %w", err)
		}
	}
	return out, nil
}

// terminate asks a process to close without forcing it.
//
// Windows has no SIGTERM. taskkill without /F posts WM_CLOSE to a window and
// a console control event to a console application, both of which let the
// program run its shutdown path. /F would map to TerminateProcess, which does
// not, and would risk a half-written .claude.json.
func terminate(p Process) error {
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(p.PID))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		// taskkill reports this on stdout, not stderr, and only as prose.
		// There is no distinct exit code for it, so the text is all there is
		// to match on.
		combined := out.String() + errb.String()
		if strings.Contains(combined, "can only be terminated forcefully") {
			return fmt.Errorf("%w (pid %d, %s)", ErrNeedsForce, p.PID, p.Name)
		}
		return fmt.Errorf("ask pid %d to close: %w: %s", p.PID, err,
			strings.TrimSpace(combined))
	}
	return nil
}

// alive reports whether a pid is still running.
//
// OpenProcess succeeds on a process that has exited but still has open
// handles, so the exit code is checked too: STILL_ACTIVE distinguishes a live
// process from a zombie one.
func alive(pid int) bool {
	const stillActive = 259
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)

	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
