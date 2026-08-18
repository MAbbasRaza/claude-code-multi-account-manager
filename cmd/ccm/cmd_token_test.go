package main

import (
	"errors"
	"strings"
	"testing"
)

func entries(names ...string) []listEntry {
	out := make([]listEntry, 0, len(names))
	for i, n := range names {
		out = append(out, listEntry{Index: i + 1, Name: n})
	}
	return out
}

func TestResolveSelectorByName(t *testing.T) {
	e, err := resolveSelector(entries("work", "personal"), "personal")
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "personal" {
		t.Errorf("got %q, want personal", e.Name)
	}
}

func TestResolveSelectorByNumber(t *testing.T) {
	list := entries("work", "personal", "client")
	for n, want := range map[string]string{"1": "work", "2": "personal", "3": "client"} {
		e, err := resolveSelector(list, n)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if e.Name != want {
			t.Errorf("selector %s = %q, want %q", n, e.Name, want)
		}
	}
}

// The case that decides whether this command can act on the wrong account.
// A profile may legitimately be called "2"; typing 2 must mean that profile,
// not the second row. Getting this backwards would overwrite or switch
// credentials the user never named.
func TestResolveSelectorPrefersANameOverAPosition(t *testing.T) {
	// "2" is the name of the third entry, so a positional reading would
	// return "personal" instead.
	list := entries("work", "personal", "2")

	e, err := resolveSelector(list, "2")
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "2" {
		t.Errorf("selector %q resolved to %q; a name must beat a position, or this "+
			"command acts on an account the user did not ask for", "2", e.Name)
	}
	if e.Index != 3 {
		t.Errorf("index = %d, want 3", e.Index)
	}
}

func TestResolveSelectorRejectsOutOfRange(t *testing.T) {
	list := entries("work", "personal")
	for _, sel := range []string{"0", "3", "-1", "99"} {
		_, err := resolveSelector(list, sel)
		if err == nil {
			t.Errorf("selector %q should not resolve", sel)
			continue
		}
		if !errors.Is(err, ErrNoSuchProfile) {
			t.Errorf("selector %q: got %v, want ErrNoSuchProfile", sel, err)
		}
	}
}

func TestResolveSelectorRejectsUnknownName(t *testing.T) {
	_, err := resolveSelector(entries("work"), "nope")
	if !errors.Is(err, ErrNoSuchProfile) {
		t.Errorf("got %v, want ErrNoSuchProfile", err)
	}
	// The user has to be told how to find the right value.
	if !strings.Contains(err.Error(), "ccm list") {
		t.Errorf("the error should point at `ccm list`, got: %v", err)
	}
}

func TestResolveSelectorRejectsEmpty(t *testing.T) {
	if _, err := resolveSelector(entries("work"), "   "); err == nil {
		t.Error("an empty selector should not resolve")
	}
}

// The summary line is the whole point of `ccm token status`: it has to
// distinguish a live expiry from a stored snapshot. Reporting a parked
// profile's snapshot as though it were current would tell the user their
// account had expired when nothing is wrong.
func TestDescribeDistinguishesLiveFromSnapshot(t *testing.T) {
	tests := []struct {
		name  string
		entry listEntry
		want  string
	}{
		{
			name:  "live and valid",
			entry: listEntry{ExpiresAt: "2099-01-01T00:00:00Z", ExpiryIsLive: true},
			want:  "valid until",
		},
		{
			name:  "live and lapsed",
			entry: listEntry{ExpiresAt: "2000-01-01T00:00:00Z", ExpiryIsLive: true, Expired: true},
			want:  "Claude Code will exchange the refresh token",
		},
		{
			name:  "parked and lapsed is normal",
			entry: listEntry{ExpiresAt: "2000-01-01T00:00:00Z", Expired: true},
			want:  "normal for a parked account",
		},
		{
			name:  "nothing recorded",
			entry: listEntry{},
			want:  "no expiry recorded",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := describe(tc.entry)
			if !strings.Contains(got, tc.want) {
				t.Errorf("describe() = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// A live expiry and a snapshot must never render identically, or the
// distinction the function exists to make is invisible.
func TestDescribeNeverConfusesLiveWithSnapshot(t *testing.T) {
	at := "2030-05-05T00:00:00Z"
	live := describe(listEntry{ExpiresAt: at, ExpiryIsLive: true})
	parked := describe(listEntry{ExpiresAt: at})
	if live == parked {
		t.Errorf("a live token and a parked snapshot both render as %q", live)
	}
}

func TestPlural(t *testing.T) {
	if plural(1, "is", "are") != "is" {
		t.Error("1 should be singular")
	}
	for _, n := range []int{0, 2, 7} {
		if plural(n, "is", "are") != "are" {
			t.Errorf("%d should be plural", n)
		}
	}
}
