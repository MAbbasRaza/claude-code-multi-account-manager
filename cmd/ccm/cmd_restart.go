package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/MAbbasRaza/claude-code-multi-account-manager/internal/locate"
	"github.com/MAbbasRaza/claude-code-multi-account-manager/internal/proc"
)

// shutdownGrace is how long a program is given to exit on its own.
//
// Generous on purpose. Nothing here ever escalates to a forced kill, so the
// only cost of waiting is a slower command, while the cost of giving up early
// is reporting a failure for something that was about to succeed.
const shutdownGrace = 10 * time.Second

// target is something ccm can restart.
type target struct {
	key string
	// program is the executable name, and for ccm's own apps the key into
	// internal/locate.
	program string
	label   string
	// relaunch is false for Claude Code. It is an interactive program tied to
	// the terminal or editor it was started from, and a copy started detached
	// by ccm would not be the session the user wants back.
	relaunch bool
}

var restartTargets = []target{
	{key: "claude", program: "claude", label: "Claude Code", relaunch: false},
	{key: "tray", program: locate.Tray, label: "the tray app", relaunch: true},
	{key: "gui", program: locate.GUI, label: "the desktop app", relaunch: true},
}

func findTarget(key string) (target, bool) {
	for _, t := range restartTargets {
		if t.key == key {
			return t, true
		}
	}
	return target{}, false
}

// running finds the processes for a target.
//
// Claude Code goes through proc.FindClaude rather than proc.Find so that the
// matcher a switch depends on for safety stays the single definition of what
// counts as Claude Code.
func (t target) running() ([]proc.Process, error) {
	if t.key == "claude" {
		return proc.FindClaude()
	}
	return proc.Find(t.program)
}

func cmdRestart(g globalOpts, args []string) error {
	if len(args) == 0 {
		return restartStatus(g)
	}

	key := strings.ToLower(strings.TrimSpace(args[0]))
	if key == "all" {
		// Deliberately ccm's own apps only. Sweeping Claude Code into an
		// "all" would close the user's session as a side effect of asking to
		// restart a tray icon.
		var failed []string
		for _, t := range restartTargets {
			if !t.relaunch {
				continue
			}
			if err := restartOne(g, t); err != nil {
				failed = append(failed, err.Error())
			}
		}
		if len(failed) > 0 {
			return errors.New(strings.Join(failed, "\n"))
		}
		return nil
	}

	t, ok := findTarget(key)
	if !ok {
		return fmt.Errorf("unknown restart target %q (valid: claude, tray, gui, all)", args[0])
	}
	return restartOne(g, t)
}

func restartStatus(g globalOpts) error {
	type row struct {
		Target    string `json:"target"`
		Label     string `json:"label"`
		Running   bool   `json:"running"`
		PIDs      []int  `json:"pids,omitempty"`
		Installed bool   `json:"installed"`
		Relaunch  bool   `json:"canRelaunch"`
		Err       string `json:"error,omitempty"`
	}

	var rows []row
	for _, t := range restartTargets {
		r := row{Target: t.key, Label: t.label, Relaunch: t.relaunch, Installed: true}
		if t.relaunch {
			r.Installed = locate.Executable(t.program) != ""
		}
		procs, err := t.running()
		if err != nil {
			// Reported rather than swallowed: "could not tell" is not the same
			// as "not running", and the difference decides whether a restart
			// would do anything.
			r.Err = err.Error()
		}
		for _, p := range procs {
			r.PIDs = append(r.PIDs, p.PID)
		}
		r.Running = len(procs) > 0
		rows = append(rows, r)
	}

	if g.jsonOut {
		return emitJSON(map[string]any{"targets": rows})
	}

	fmt.Println("What ccm can restart:")
	fmt.Println()
	for _, r := range rows {
		state := "not running"
		switch {
		case r.Err != "":
			state = "could not tell: " + firstLine(r.Err)
		case !r.Installed:
			state = "not installed"
		case r.Running:
			state = fmt.Sprintf("running (%s)", pidList(r.PIDs))
		}
		fmt.Printf("  ccm restart %-7s %-16s %s\n", r.Target, r.Label, state)
	}
	fmt.Println()
	fmt.Println("  ccm restart all     the tray and desktop apps, not Claude Code")
	fmt.Println()
	fmt.Println("Claude Code is asked to close but not reopened: it is tied to the terminal")
	fmt.Println("or editor you started it from, and a copy ccm started would not be that.")
	return nil
}

func restartOne(g globalOpts, t target) error {
	procs, err := t.running()
	if err != nil {
		return fmt.Errorf("could not determine whether %s is running: %w", t.label, err)
	}

	var exe string
	if t.relaunch {
		exe = locate.Executable(t.program)
		if exe == "" && len(procs) == 0 {
			return fmt.Errorf("%s is not installed and is not running", t.label)
		}
	}

	stopped := 0
	for _, p := range procs {
		if err := proc.Terminate(p); err != nil {
			if errors.Is(err, proc.ErrNeedsForce) {
				return fmt.Errorf("Windows will not close %s politely (pid %d).\n"+
					"It has no window to send a close request to, and the only alternative "+
					"the system offers would give it no chance to finish writing its files.\n"+
					"Quit it yourself instead: %s",
					t.label, p.PID, quitHint(t))
			}
			return err
		}
		stopped++
	}

	if left := proc.WaitGone(procs, shutdownGrace); len(left) > 0 {
		// Never escalated to a forced kill. Claude Code writes .claude.json as
		// it exits, and killing it mid-write is how that file ends up
		// corrupted, which is the failure this tool exists to prevent.
		return fmt.Errorf("%s did not close within %s (%s still running).\n"+
			"ccm will not force it: these programs write files as they exit, and killing "+
			"one mid-write is how those files get corrupted. Close it yourself and try again.",
			t.label, shutdownGrace, pidList(pids(left)))
	}

	relaunched := false
	if t.relaunch && exe != "" {
		if err := exec.Command(exe).Start(); err != nil {
			return fmt.Errorf("stopped %s but could not start it again: %w", t.label, err)
		}
		relaunched = true
	}

	if g.jsonOut {
		return emitJSON(map[string]any{
			"target":     t.key,
			"stopped":    stopped,
			"relaunched": relaunched,
		})
	}

	switch {
	case stopped == 0 && relaunched:
		fmt.Printf("%s was not running; started it.\n", capitalise(t.label))
	case stopped == 0:
		fmt.Printf("%s is not running.\n", capitalise(t.label))
	case relaunched:
		fmt.Printf("Restarted %s.\n", t.label)
	default:
		fmt.Printf("Closed %s (%d %s).\n", t.label, stopped, plural(stopped, "process", "processes"))
		fmt.Println("Start it again the way you normally do; ccm does not reopen it, because")
		fmt.Println("a copy started here would not be attached to your terminal or editor.")
	}
	return nil
}

// quitHint names the way a user actually closes each program, since ccm cannot.
func quitHint(t target) string {
	switch t.key {
	case "claude":
		return "type /exit in Claude Code, or press Ctrl+C in its terminal"
	case "tray":
		return "right click the tray icon and choose Quit"
	default:
		return "close its window"
	}
}

func pids(procs []proc.Process) []int {
	out := make([]int, 0, len(procs))
	for _, p := range procs {
		out = append(out, p.PID)
	}
	return out
}

func pidList(ids []int) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("pid %d", id))
	}
	return strings.Join(parts, ", ")
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
