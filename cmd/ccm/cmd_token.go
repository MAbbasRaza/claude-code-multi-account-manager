package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/MAbbasRaza/claude-code-multi-account-manager/internal/manager"
)

// ErrNoSuchProfile reports a selector that matched nothing.
var ErrNoSuchProfile = errors.New("no such profile")

// resolveSelector turns a name or a 1-based number into a profile.
//
// The numbers are the ones the interactive picker and `ccm list` print, so
// what a user reads off the screen is what they can type.
//
// A name always beats a number. A profile can legitimately be called "2", and
// in that case the user typing "2" means the profile they named, not the
// second row. Silently preferring the row would act on the wrong account,
// which for this tool means switching or overwriting the wrong credentials.
func resolveSelector(entries []listEntry, selector string) (listEntry, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return listEntry{}, errors.New("no profile given")
	}

	for _, e := range entries {
		if e.Name == selector {
			return e, nil
		}
	}

	if n, err := strconv.Atoi(selector); err == nil {
		if n < 1 || n > len(entries) {
			return listEntry{}, fmt.Errorf("%w: %d is out of range, there %s %d",
				ErrNoSuchProfile, n, plural(len(entries), "is", "are"), len(entries))
		}
		return entries[n-1], nil
	}

	return listEntry{}, fmt.Errorf("%w: %q. Run `ccm list` to see the names and numbers",
		ErrNoSuchProfile, selector)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func cmdToken(g globalOpts, args []string) error {
	action := "status"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}

	selector := ""
	if len(args) > 0 {
		selector = args[0]
	}

	switch action {
	case "status":
		return tokenStatus(g, selector)
	case "refresh":
		return tokenRefresh(g, selector)
	default:
		// A bare `ccm token work` reads naturally as asking for that profile's
		// status, so it is treated that way rather than rejected.
		return tokenStatus(g, action)
	}
}

// tokenReport is one profile's token state.
type tokenReport struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	Email      string `json:"email,omitempty"`
	Active     bool   `json:"active"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
	Expired    bool   `json:"expired"`
	Live       bool   `json:"live"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	Summary    string `json:"summary"`
}

// describe explains what the stored expiry actually means for this profile.
//
// The distinction matters and is easy to get wrong: only the active profile's
// expiry describes anything current. For a parked profile it is a snapshot
// taken when the profile was last written, and an expired access token there
// is the normal resting state rather than a fault.
func describe(e listEntry) string {
	switch {
	case e.ExpiresAt == "":
		return "no expiry recorded"
	case e.ExpiryIsLive && e.Expired:
		return "access token lapsed; Claude Code will exchange the refresh token on its next request"
	case e.ExpiryIsLive:
		return "access token valid until " + e.ExpiresAt
	case e.Expired:
		return "snapshot taken " + e.ExpiresAt + "; expired, which is normal for a parked account"
	default:
		return "snapshot valid until " + e.ExpiresAt
	}
}

func tokenStatus(g globalOpts, selector string) error {
	m, err := manager.Open(g.configDir)
	if err != nil {
		return err
	}
	entries, st, err := buildList(m)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		if g.jsonOut {
			return emitJSON(map[string]any{"tokens": []tokenReport{}})
		}
		fmt.Println("No profiles yet. Run `ccm add <name>` to capture the account you are signed into.")
		return nil
	}

	if selector != "" {
		e, err := resolveSelector(entries, selector)
		if err != nil {
			return err
		}
		entries = []listEntry{e}
	}

	reports := make([]tokenReport, 0, len(entries))
	for _, e := range entries {
		reports = append(reports, tokenReport{
			Index:      e.Index,
			Name:       e.Name,
			Email:      e.Email,
			Active:     e.Active,
			ExpiresAt:  e.ExpiresAt,
			Expired:    e.Expired,
			Live:       e.ExpiryIsLive,
			LastUsedAt: e.LastUsedAt,
			Summary:    describe(e),
		})
	}

	if g.jsonOut {
		return emitJSON(map[string]any{"tokens": reports, "status": st})
	}

	for _, r := range reports {
		marker := " "
		if r.Active {
			marker = "*"
		}
		fmt.Printf("%s %d) %s\n", marker, r.Index, r.Name)
		if r.Email != "" {
			fmt.Printf("      account  %s\n", r.Email)
		}
		fmt.Printf("      token    %s\n", r.Summary)
		if r.LastUsedAt != "" {
			fmt.Printf("      captured %s\n", r.LastUsedAt)
		}
	}

	// Said once, at the end, rather than per profile. Nothing local can tell
	// whether a refresh token is still good; only using it can, and that is
	// what a switch does.
	fmt.Println()
	fmt.Println("* = currently active")
	fmt.Println("Whether a parked account's refresh token is still accepted cannot be known")
	fmt.Println("without using it. `ccm use <name>` is what finds out.")
	return nil
}

func tokenRefresh(g globalOpts, selector string) error {
	m, err := manager.Open(g.configDir)
	if err != nil {
		return err
	}
	entries, st, err := buildList(m)
	if err != nil {
		return err
	}
	if !st.LoggedIn {
		return errors.New("not signed in to Claude Code, so there is no live token to save.\n" +
			"Run /login in Claude Code first.")
	}

	// Default to the account currently signed in, which is the only one that
	// has a live token to read.
	target := st.ActiveProfile
	if selector != "" {
		e, err := resolveSelector(entries, selector)
		if err != nil {
			return err
		}
		target = e.Name
		if !e.Active {
			return fmt.Errorf("%q is not the account you are signed into, so there is no live "+
				"token to read for it.\n"+
				"Only the signed-in account (%s) has one. ccm cannot renew a parked account "+
				"without switching to it: run `ccm use %s` first.",
				e.Name, activeLabel(st), e.Name)
		}
	}
	if target == "" {
		return errors.New("the signed-in account is not saved yet.\n" +
			"Run `ccm add <name>` to capture it first.")
	}

	before := findEntry(entries, target)

	p, err := m.Capture(target)
	if err != nil {
		return err
	}

	after, _, err := buildList(m)
	if err != nil {
		return err
	}
	updated := findEntry(after, target)

	if g.jsonOut {
		return emitJSON(map[string]any{
			"refreshed": target,
			"email":     p.EmailAddress,
			"before":    before.ExpiresAt,
			"after":     updated.ExpiresAt,
			"changed":   before.ExpiresAt != updated.ExpiresAt,
		})
	}

	fmt.Printf("Re-saved %q from the live login (%s).\n", target, p.EmailAddress)
	switch {
	case before.ExpiresAt == updated.ExpiresAt && before.ExpiresAt != "":
		// Worth saying plainly. Claude Code refreshes on its own schedule, so
		// an unchanged expiry means the stored copy was already current, not
		// that anything failed.
		fmt.Printf("Token unchanged, expiry still %s. The saved copy was already current.\n",
			updated.ExpiresAt)
	case updated.ExpiresAt != "":
		fmt.Printf("Token now valid until %s.\n", updated.ExpiresAt)
	}
	return nil
}

func activeLabel(st manager.Status) string {
	if st.ActiveProfile != "" {
		return st.ActiveProfile
	}
	if st.EmailAddress != "" {
		return st.EmailAddress + ", not yet saved"
	}
	return "none"
}

func findEntry(entries []listEntry, name string) listEntry {
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	return listEntry{}
}
