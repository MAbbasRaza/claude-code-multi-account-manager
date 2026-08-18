// Package proc detects running Claude Code processes.
//
// This is a safety check, not a nicety. Claude Code reads credentials at
// startup and rewrites .claude.json when it exits, so a switch performed
// underneath a live session is both invisible to that session and liable to be
// overwritten by it moments later. Refusing to switch while Claude Code is
// running is the difference between a tool that works and one that
// intermittently loses an account.
package proc

import (
	"errors"
	"strings"
	"time"
)

// Process is a running Claude Code process.
type Process struct {
	PID  int
	Name string
}

// names that indicate a running Claude Code CLI or its native build.
var claudeNames = []string{"claude", "claude.exe"}

func isClaude(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, c := range claudeNames {
		if n == c {
			return true
		}
	}
	return false
}

// FindClaude returns every running Claude Code process.
//
// Detection is best effort: a platform where enumeration fails returns the
// error so callers can tell "none running" apart from "could not tell", and
// refuse accordingly rather than assuming it is safe to proceed.
func FindClaude() ([]Process, error) {
	return findProcs(isClaude)
}

// Find returns every running process whose executable matches one of names,
// compared case-insensitively and with any .exe suffix ignored.
//
// Used to locate ccm's own resident programs. Kept separate from FindClaude so
// that the Claude Code matcher, which a switch depends on for safety, cannot be
// broadened by accident.
func Find(names ...string) ([]Process, error) {
	want := make(map[string]bool, len(names)*2)
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		want[n] = true
		want[n+".exe"] = true
	}
	return findProcs(func(name string) bool {
		return want[strings.ToLower(strings.TrimSpace(name))]
	})
}

// ErrNeedsForce reports that the platform will not close a process politely.
//
// Windows only, and common: taskkill without /F posts WM_CLOSE to a window, so
// a console program with no window cannot be asked to close at all. The only
// alternative the OS offers is TerminateProcess, which gives the program no
// chance to finish writing what it is writing. ccm surfaces this instead of
// escalating, and lets the user close the program themselves.
var ErrNeedsForce = errors.New("this platform will only close that process forcefully")

// Terminate asks a process to exit, and does not force it.
//
// The distinction matters here more than usual. Claude Code writes
// .claude.json as it shuts down, so killing it outright risks leaving that
// file half-written, which is the exact corruption this whole tool exists to
// avoid. A process that ignores the request stays running and the caller
// reports that, rather than escalating.
func Terminate(p Process) error { return terminate(p) }

// Alive reports whether a process id is still running.
func Alive(pid int) bool { return alive(pid) }

// WaitGone polls until every process has exited or the deadline passes,
// returning those still running.
func WaitGone(procs []Process, timeout time.Duration) []Process {
	deadline := time.Now().Add(timeout)
	for {
		var left []Process
		for _, p := range procs {
			if Alive(p.PID) {
				left = append(left, p)
			}
		}
		if len(left) == 0 || time.Now().After(deadline) {
			return left
		}
		time.Sleep(100 * time.Millisecond)
	}
}
