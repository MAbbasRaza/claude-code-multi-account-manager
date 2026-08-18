// Command ccm switches between Claude Code accounts without re-authenticating.
//
// Claude Code keeps one account's auth state at a time, so signing into a
// second account destroys the first one's refresh token and returning to it
// requires a full browser authorization. ccm parks each account's state and
// swaps it back in on demand.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MAbbasRaza/claude-code-multi-account-manager/internal/manager"
	"github.com/MAbbasRaza/claude-code-multi-account-manager/internal/vault"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

type globalOpts struct {
	configDir string
	jsonOut   bool
	force     bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ccm: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	var g globalOpts
	var rest []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			g.jsonOut = true
		case a == "--force" || a == "-f":
			g.force = true
		case a == "--config-dir":
			if i+1 >= len(args) {
				return errors.New("--config-dir requires a path")
			}
			i++
			g.configDir = args[i]
		case strings.HasPrefix(a, "--config-dir="):
			g.configDir = strings.TrimPrefix(a, "--config-dir=")
		case a == "--version" || a == "-v":
			fmt.Println("ccm " + version)
			return nil
		case a == "--help" || a == "-h" || a == "help":
			usage()
			return nil
		default:
			rest = append(rest, a)
		}
	}

	if len(rest) == 0 {
		return cmdPicker(g)
	}

	switch rest[0] {
	case "init":
		return cmdInit(g)
	case "list", "ls":
		return cmdList(g)
	case "use", "switch":
		if len(rest) < 2 {
			return errors.New("usage: ccm use <profile>")
		}
		return cmdUse(g, rest[1])
	case "add":
		name := ""
		if len(rest) > 1 {
			name = rest[1]
		}
		return cmdAdd(g, name)
	case "rm", "remove", "delete":
		if len(rest) < 2 {
			return errors.New("usage: ccm rm <profile>")
		}
		return cmdRemove(g, rest[1])
	case "rename", "mv":
		if len(rest) < 3 {
			return errors.New("usage: ccm rename <old-name> <new-name>")
		}
		return cmdRename(g, rest[1], rest[2])
	case "status":
		return cmdStatus(g)
	case "config":
		return cmdConfig(g, rest[1:])
	case "autostart":
		return cmdAutostart(g, rest[1:])
	case "shortcut", "shortcuts":
		return cmdShortcut(g, rest[1:])
	case "token", "tokens":
		return cmdToken(g, rest[1:])
	case "refresh":
		// The phrasing people reach for first. `ccm refresh work` and
		// `ccm token refresh work` are the same thing.
		return cmdToken(g, append([]string{"refresh"}, rest[1:]...))
	case "restart":
		return cmdRestart(g, rest[1:])
	case "doctor":
		return cmdDoctor(g)
	default:
		return fmt.Errorf("unknown command %q (try: ccm --help)", rest[0])
	}
}

func usage() { fmt.Print(helpText) }

// helpText is a constant rather than an inline literal so a test can assert
// every command the dispatcher accepts is actually documented here. This help
// had already fallen behind the code twice.
const helpText = `ccm - Claude Code multi-account manager

Switch between Claude Code accounts without signing in again.

Claude Code stores one account's credentials at a time. Signing into a second
account destroys the first one's refresh token, so going back means a full
browser sign-in. ccm keeps each account in a protected vault and swaps them
back on demand, which makes switching an offline, one-second operation.

QUICK START

  1. Point ccm at your Claude Code installation. Run this once:

       ccm init

  2. Save the account you are already signed into:

       ccm add work

  3. Switch to your other account inside Claude Code the normal way: run
     /logout, then /login. Come back and save that one too:

       ccm add personal

  4. From now on, switch offline:

       ccm use work

  Run ccm with no arguments for an interactive picker instead.

  Anywhere a profile is expected, "who" can be its name or the number shown by
  "ccm list". A name always wins, so a profile actually called 2 is still
  reachable by typing 2.

  IMPORTANT: capture an account with "ccm add" BEFORE you log out of it.
  Logging out destroys the refresh token, and nothing can recover it except
  signing in through the browser again.

COMMANDS

  ccm                       interactive account picker
  ccm init                  detect and pin the Claude Code config directory
  ccm status                show the active account, config dir and backend
  ccm list                  list saved profiles, marking the active one
                            (alias: ls)
  ccm add [name]            save the current login as a profile. With no name,
                            one is derived from the account's email address
  ccm use <profile>         switch to a profile (alias: switch)
  ccm rename <old> <new>    rename a profile, keeping its credentials
                            (alias: mv)
  ccm rm <profile>          forget a profile (aliases: remove, delete)

  ccm token status [who]    show each saved account's token: when it expires,
                            whether that is live or a stored snapshot, and when
                            it was last captured
  ccm token refresh [who]   re-read the live login and save it over that
                            profile, so the stored copy stops being stale.
                            Defaults to the account you are signed into
                            (aliases: ccm refresh, ccm tokens)
  ccm doctor                diagnose path resolution, permissions, running
                            sessions and vault health. Start here when
                            something is wrong

  ccm restart               show what ccm can restart and what is running
  ccm restart claude        ask Claude Code to close, so a switch takes effect.
                            It is not reopened: it belongs to the terminal or
                            editor you started it from
  ccm restart tray|gui|all  stop and start ccm's own apps. Mainly useful after
                            an upgrade, when the running copy is the old build.
                            "all" means the tray and desktop app, never Claude
                            Code

  ccm config path           print the location of ccm's settings file
  ccm config get [key]      read a setting, or all of them
  ccm config set <key> <value>
                            change a setting. Keys:
                              claudeConfigDir        which Claude Code install
                                                     to manage
                              vaultPath              where profiles are stored
                              requireClosedSessions  refuse to switch while
                                                     Claude Code is running
                              credentialsBackend     auto, file or keychain

  ccm autostart status|enable|disable
                            run the tray app when you log in. Uses the Run key
                            on Windows, a LaunchAgent on macOS and an XDG entry
                            on Linux, all per-user

  ccm shortcut status|add|remove [desktop|menu|all]
                            desktop and application menu shortcuts for the
                            desktop app. With no kind, acts on every kind this
                            platform has (aliases: shortcuts)

FLAGS

  --config-dir <path>   manage a different Claude Code install for one run
  --json                machine-readable output, for scripts and the extension
  --force, -f           switch even if Claude Code is running. See CAVEATS
  --version, -v         print the version
  --help, -h            this message

ENVIRONMENT

  CLAUDE_CONFIG_DIR         Claude Code's own setting, honoured when present
  CCM_CLAUDE_CONFIG_DIR     overrides it for ccm only
  CCM_HOME                  where ccm keeps its settings and vault
  CCM_CREDENTIALS_BACKEND   auto, file or keychain. Use file on macOS over SSH
                            or in tmux, where the login keychain is locked
  CCM_VAULT_BACKEND         auto or file. Same situation, for ccm's own vault,
                            which is encrypted under a key held in that same
                            keychain

  The config directory is resolved in this order, first match winning:
  --config-dir, CCM_CLAUDE_CONFIG_DIR, ccm's settings file, CLAUDE_CONFIG_DIR,
  then the platform default. "ccm doctor" prints every level and warns when
  they disagree.

WHAT A SWITCH ACTUALLY MOVES

  Only the account-scoped keys: claudeAiOauth in the credentials store, plus
  oauthAccount and userID in .claude.json. Your MCP server logins, project
  history, trust decisions and settings are left byte for byte as they were.
  Both files are backed up before every write.

CAVEATS

  Restart Claude Code after switching. Credentials are read at startup, so a
  running session keeps using the previous account.

  ccm refuses to switch while Claude Code is running, because a live session
  rewrites these files when it exits and would undo the switch. --force
  overrides that, and is usually the wrong answer.

  Parked accounts still expire. A sign-in has a finite lifetime of its own, so
  an account left unused long enough will need a real /login again. "ccm list"
  and "ccm doctor" show expiry per profile.

LEARN MORE

  https://github.com/MAbbasRaza/claude-code-multi-account-manager
`

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdStatus(g globalOpts) error {
	m, err := manager.Open(g.configDir)
	if err != nil {
		return err
	}
	st, err := m.Status()
	if err != nil {
		return err
	}
	if g.jsonOut {
		return emitJSON(st)
	}

	fmt.Printf("Config dir:  %s  (from %s)\n", st.ConfigDir, st.ConfigDirSource)
	fmt.Printf("Credentials: %s\n", m.Store.Describe())
	fmt.Printf("Account map: %s\n", st.ConfigJSONPath)
	if !st.LoggedIn {
		fmt.Println("\nNo active subscription login. Run /login in Claude Code.")
		return nil
	}
	fmt.Println()
	if st.EmailAddress != "" {
		fmt.Printf("Active:      %s\n", st.EmailAddress)
	}
	if st.Organization != "" {
		fmt.Printf("Org:         %s\n", st.Organization)
	}
	if st.Subscription != "" {
		fmt.Printf("Plan:        %s\n", st.Subscription)
	}
	if st.ExpiresAt != "" {
		fmt.Printf("Token expiry:%s %s\n", "", st.ExpiresAt)
	}
	if st.ActiveProfile != "" {
		fmt.Printf("Profile:     %s\n", st.ActiveProfile)
	} else {
		fmt.Println("Profile:     (not captured yet - run `ccm add <name>`)")
	}
	return nil
}

type listEntry struct {
	// Index is the 1-based position shown by the picker, `ccm list` and
	// `ccm token`, so a number a user reads off any of them selects the same
	// profile everywhere.
	Index        int    `json:"index"`
	Name         string `json:"name"`
	Email        string `json:"email,omitempty"`
	Organization string `json:"organization,omitempty"`
	Subscription string `json:"subscription,omitempty"`
	Active       bool   `json:"active"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	Expired      bool   `json:"expired"`
	ExpiryIsLive bool   `json:"expiryIsLive"`
	LastUsedAt   string `json:"lastUsedAt,omitempty"`
}

func buildList(m *manager.Manager) ([]listEntry, manager.Status, error) {
	views, st, err := m.Profiles()
	if err != nil {
		return nil, st, err
	}
	var out []listEntry
	for i, v := range views {
		e := listEntry{
			Index:        i + 1,
			Name:         v.Name,
			Email:        v.EmailAddress,
			Organization: v.OrganizationName,
			Subscription: v.SubscriptionType(),
			Active:       v.Active,
			ExpiryIsLive: v.ExpiryIsLive,
		}
		if !v.ExpiresAt.IsZero() {
			e.ExpiresAt = v.ExpiresAt.UTC().Format(time.RFC3339)
			e.Expired = v.Expired()
		}
		if !v.LastUsedAt.IsZero() {
			e.LastUsedAt = v.LastUsedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, e)
	}
	return out, st, nil
}

func cmdList(g globalOpts) error {
	m, err := manager.Open(g.configDir)
	if err != nil {
		return err
	}
	entries, st, err := buildList(m)
	if err != nil {
		return err
	}
	if g.jsonOut {
		return emitJSON(map[string]any{"profiles": entries, "status": st})
	}
	if len(entries) == 0 {
		fmt.Println("No profiles yet.")
		fmt.Println("Sign in to Claude Code with /login, then run `ccm add <name>` to capture it.")
		return nil
	}
	for _, e := range entries {
		marker := " "
		if e.Active {
			marker = "*"
		}
		// An expired access token on a parked profile is the normal resting
		// state, not a problem: Claude Code exchanges the refresh token for a
		// new one on its next request. Only the active profile's expiry
		// describes something live, so only that one is worth reporting.
		note := ""
		switch {
		case e.ExpiryIsLive && e.Expired:
			note = "  access token lapsed, Claude Code will refresh it"
		case e.ExpiryIsLive:
			note = "  token valid until " + e.ExpiresAt
		case e.LastUsedAt != "":
			note = "  last used " + e.LastUsedAt
		}
		// Numbered to match the interactive picker, so a number read here can
		// be passed to the commands that accept one.
		fmt.Printf("%s %d) %-16s %-32s %-8s%s\n", marker, e.Index, e.Name, e.Email, e.Subscription, note)
	}
	fmt.Println("\n* = currently active")
	return nil
}

func cmdAdd(g globalOpts, name string) error {
	m, err := manager.Open(g.configDir)
	if err != nil {
		return err
	}
	p, err := m.Capture(name)
	if err != nil {
		return err
	}
	if g.jsonOut {
		return emitJSON(map[string]any{"captured": p.Name, "email": p.EmailAddress})
	}
	fmt.Printf("Captured %s as profile %q.\n", p.EmailAddress, p.Name)
	fmt.Println("To add another account, run /logout then /login in Claude Code, and run `ccm add <name>` again.")
	return nil
}

func cmdRemove(g globalOpts, name string) error {
	m, err := manager.Open(g.configDir)
	if err != nil {
		return err
	}
	if err := m.Remove(name); err != nil {
		return err
	}
	if g.jsonOut {
		return emitJSON(map[string]any{"removed": name})
	}
	fmt.Printf("Removed profile %q.\n", name)
	return nil
}

func cmdRename(g globalOpts, oldName, newName string) error {
	m, err := manager.Open(g.configDir)
	if err != nil {
		return err
	}
	if err := m.Rename(oldName, newName); err != nil {
		if errors.Is(err, vault.ErrNotFound) {
			return fmt.Errorf("%w\nRun `ccm list` to see available profiles", err)
		}
		return err
	}
	if g.jsonOut {
		return emitJSON(map[string]any{"renamed": oldName, "to": newName})
	}
	fmt.Printf("Renamed %q to %q. Its stored credentials are unchanged.\n", oldName, newName)
	return nil
}

func cmdUse(g globalOpts, target string) error {
	m, err := manager.Open(g.configDir)
	if err != nil {
		return err
	}
	res, err := m.Switch(target, g.force)
	if err != nil {
		if errors.Is(err, vault.ErrNotFound) {
			return fmt.Errorf("%w\nRun `ccm list` to see available profiles", err)
		}
		return err
	}
	if g.jsonOut {
		return emitJSON(res)
	}
	if res.CapturedAs != "" {
		verb := "updated"
		if res.CapturedNew {
			verb = "saved as new profile"
		}
		fmt.Printf("Captured outgoing account %s (%s %q).\n", res.FromEmail, verb, res.CapturedAs)
	}
	fmt.Printf("Switched to %q", res.To)
	if res.ToEmail != "" {
		fmt.Printf(" (%s)", res.ToEmail)
	}
	fmt.Println(".")
	fmt.Println(res.RestartWarning)
	return nil
}

func cmdPicker(g globalOpts) error {
	m, err := manager.Open(g.configDir)
	if err != nil {
		return err
	}
	entries, _, err := buildList(m)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("No profiles yet.")
		fmt.Println("Sign in to Claude Code with /login, then run `ccm add <name>` to capture it.")
		return nil
	}

	fmt.Println("Select an account:")
	for i, e := range entries {
		marker := " "
		if e.Active {
			marker = "*"
		}
		note := ""
		if e.Expired {
			note = "  (token expired)"
		}
		fmt.Printf("  %s %d) %-16s %s%s\n", marker, i+1, e.Name, e.Email, note)
	}
	fmt.Print("\nNumber (or blank to cancel): ")

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return nil
	}
	// Windows shells readily prepend a UTF-8 BOM when input is piped rather
	// than typed, which would otherwise be read as a bogus selection.
	line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
	if line == "" {
		fmt.Println("Cancelled.")
		return nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(entries) {
		return fmt.Errorf("invalid selection %q", line)
	}
	return cmdUse(g, entries[n-1].Name)
}
