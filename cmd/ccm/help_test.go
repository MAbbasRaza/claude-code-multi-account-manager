package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// dispatchCommands reads the command names out of the switch in run(), rather
// than repeating them here. A hand-kept list in a test drifts exactly the way
// the help text it is checking does, so it would pass while being wrong.
func dispatchCommands(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		// The dispatcher is the one switching on rest[0].
		idx, ok := sw.Tag.(*ast.IndexExpr)
		if !ok {
			return true
		}
		if id, ok := idx.X.(*ast.Ident); !ok || id.Name != "rest" {
			return true
		}
		for _, stmt := range sw.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if s, err := strconv.Unquote(lit.Value); err == nil {
					names = append(names, s)
				}
			}
		}
		return false
	})

	if len(names) == 0 {
		t.Fatal("found no commands in the dispatch switch; the parser or run() changed")
	}
	return names
}

// Every command the CLI accepts has to appear in the help. An undocumented
// command is invisible: there is no other place a user would find it.
func TestHelpDocumentsEveryCommand(t *testing.T) {
	for _, name := range dispatchCommands(t) {
		if !strings.Contains(helpText, name) {
			t.Errorf("command %q is accepted by run() but never mentioned in the help", name)
		}
	}
}

// The flags are parsed in a different loop from the commands, so they need
// their own check.
func TestHelpDocumentsEveryFlag(t *testing.T) {
	for _, flag := range []string{
		"--config-dir", "--json", "--force", "-f",
		"--version", "-v", "--help", "-h",
	} {
		if !strings.Contains(helpText, flag) {
			t.Errorf("flag %q is accepted but not documented", flag)
		}
	}
}

// The settings keys "config set" accepts, which a user cannot guess.
func TestHelpDocumentsEverySettingsKey(t *testing.T) {
	for _, key := range []string{
		"claudeConfigDir", "vaultPath", "requireClosedSessions", "credentialsBackend",
	} {
		if !strings.Contains(helpText, key) {
			t.Errorf("settings key %q is settable but not documented", key)
		}
	}
}

// The environment variables, likewise. CCM_VAULT_BACKEND in particular is the
// only way to run ccm on macOS over SSH, and it is not discoverable anywhere
// else from the terminal.
func TestHelpDocumentsEveryEnvironmentVariable(t *testing.T) {
	for _, env := range []string{
		"CLAUDE_CONFIG_DIR", "CCM_CLAUDE_CONFIG_DIR", "CCM_HOME",
		"CCM_CREDENTIALS_BACKEND", "CCM_VAULT_BACKEND",
	} {
		if !strings.Contains(helpText, env) {
			t.Errorf("environment variable %q is read but not documented", env)
		}
	}
}

// The one thing in the help that is not reference material: capturing an
// account after logging out of it is impossible to recover from, so the
// warning has to survive future edits.
func TestHelpWarnsAboutCapturingBeforeLogout(t *testing.T) {
	if !strings.Contains(helpText, "BEFORE you log out") {
		t.Error("the help no longer warns that an account must be captured before logout, " +
			"which is the one mistake in this tool that cannot be undone")
	}
}

// A user running --help is usually stuck, not browsing. The workflow has to be
// near the top rather than below the reference sections.
func TestQuickStartComesBeforeTheCommandReference(t *testing.T) {
	quick := strings.Index(helpText, "QUICK START")
	cmds := strings.Index(helpText, "COMMANDS")
	if quick < 0 || cmds < 0 {
		t.Fatal("the help lost its QUICK START or COMMANDS section")
	}
	if quick > cmds {
		t.Error("QUICK START should come before COMMANDS")
	}
}
