package proc

import (
	"errors"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// sleeper starts a throwaway child process that will outlive the test unless
// it is terminated, and returns it with its executable name.
//
// A real child rather than a fake: the whole point of these functions is what
// the operating system does, so a stub would test nothing. Never uses Find to
// choose what to kill, because the test binary is itself a process and a
// careless matcher would terminate the test run.
func sleeper(t *testing.T) (*exec.Cmd, string) {
	t.Helper()

	var cmd *exec.Cmd
	var name string
	switch runtime.GOOS {
	case "windows":
		// ping sleeps roughly one second per echo and needs no shell.
		cmd = exec.Command("ping", "-n", "120", "127.0.0.1")
		name = "ping"
	default:
		cmd = exec.Command("sleep", "120")
		name = "sleep"
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a test child process: %v", err)
	}

	// Reap it as soon as it dies. On unix a terminated child stays in the
	// process table as a zombie until its parent collects it, and signal 0
	// succeeds on a zombie, so without this Alive would report it running
	// forever and WaitGone would burn its whole timeout. That is an artefact
	// of the child belonging to this test: the processes ccm actually
	// terminates are reaped by their own parents.
	go func() { _, _ = cmd.Process.Wait() }()

	t.Cleanup(func() {
		// If a test failed to stop it, do not leave a stray process behind.
		_ = cmd.Process.Kill()
	})
	return cmd, name
}

func TestAliveTracksAProcess(t *testing.T) {
	cmd, _ := sleeper(t)
	pid := cmd.Process.Pid

	if !Alive(pid) {
		t.Fatalf("pid %d was just started but reads as not alive", pid)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}

	// A killed process can linger briefly before the OS reaps it.
	deadline := time.Now().Add(5 * time.Second)
	for Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if Alive(pid) {
		t.Errorf("pid %d still reads as alive after being killed", pid)
	}
}

func TestAliveIsFalseForAnUnusedPID(t *testing.T) {
	// Chosen to be implausible rather than merely unused; the point is that a
	// nonexistent pid must not read as running, or a restart would report
	// something is up when nothing is.
	if Alive(0x7FFFFFF0) {
		t.Error("an implausible pid reads as alive")
	}
}

// The behaviour the restart command depends on: ask a process to close, and
// have it actually be gone afterwards.
//
// Windows is different, and deliberately so. taskkill without /F posts
// WM_CLOSE to a window, so a console program with no window cannot be asked to
// close politely at all. That is what the test child is, so the correct result
// there is ErrNeedsForce rather than a stopped process. Escalating to /F would
// give the program no chance to finish writing, which is the whole thing this
// avoids.
func TestTerminateStopsAProcess(t *testing.T) {
	cmd, name := sleeper(t)
	p := Process{PID: cmd.Process.Pid, Name: name}

	err := Terminate(p)

	if runtime.GOOS == "windows" {
		if !errors.Is(err, ErrNeedsForce) {
			t.Fatalf("expected ErrNeedsForce for a windowless console process, got %v", err)
		}
		// And it must still be running: nothing was forced.
		if !Alive(p.PID) {
			t.Error("the process was killed despite the platform refusing a polite close")
		}
		return
	}

	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if left := WaitGone([]Process{p}, 10*time.Second); len(left) > 0 {
		t.Fatalf("pid %d was still running 10s after being asked to close", p.PID)
	}
	if Alive(p.PID) {
		t.Errorf("pid %d still alive after WaitGone reported it gone", p.PID)
	}
}

// WaitGone must return promptly when nothing is running, rather than always
// burning the full timeout.
func TestWaitGoneReturnsImmediatelyWhenNothingIsRunning(t *testing.T) {
	start := time.Now()
	left := WaitGone([]Process{{PID: 0x7FFFFFF0}}, 5*time.Second)
	if len(left) != 0 {
		t.Errorf("expected nothing left, got %v", left)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %s for a process that was never running", elapsed)
	}
}

// WaitGone must report what is still running rather than claiming success, or
// the restart command would relaunch on top of a process that never closed.
func TestWaitGoneReportsSurvivors(t *testing.T) {
	cmd, name := sleeper(t)
	p := Process{PID: cmd.Process.Pid, Name: name}

	left := WaitGone([]Process{p}, 300*time.Millisecond)
	if len(left) != 1 || left[0].PID != p.PID {
		t.Errorf("expected the running process to be reported as a survivor, got %v", left)
	}
}

func TestFindMatchesByNameIgnoringCaseAndExtension(t *testing.T) {
	cmd, name := sleeper(t)
	defer func() { _ = cmd.Process.Kill() }()

	found, err := Find(name)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !containsPID(found, cmd.Process.Pid) {
		t.Fatalf("Find(%q) did not return the child just started (pid %d)", name, cmd.Process.Pid)
	}

	// The same lookup spelled differently has to find the same process, since
	// callers pass a bare program name and Windows reports one with .exe.
	upper, err := Find(name + ".EXE")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && !containsPID(upper, cmd.Process.Pid) {
		t.Error("matching should ignore case and the .exe suffix")
	}
}

func TestFindReturnsNothingForAnUnknownName(t *testing.T) {
	found, err := Find("ccm-definitely-not-a-real-program")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("expected no matches, got %v", found)
	}
}

// FindClaude must keep its own matcher. Broadening it would weaken the check a
// switch relies on to avoid writing underneath a live session.
func TestFindClaudeDoesNotMatchArbitraryNames(t *testing.T) {
	cmd, name := sleeper(t)
	defer func() { _ = cmd.Process.Kill() }()

	found, err := FindClaude()
	if err != nil {
		t.Skipf("process enumeration unavailable: %v", err)
	}
	if containsPID(found, cmd.Process.Pid) {
		t.Errorf("FindClaude matched a %s process; the Claude Code matcher has been broadened", name)
	}
}

func containsPID(procs []Process, pid int) bool {
	for _, p := range procs {
		if p.PID == pid {
			return true
		}
	}
	return false
}
