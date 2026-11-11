package main

import (
	"reflect"
	"testing"
)

// TestSplitPositional: the repo name may sit before or after the flags, and a
// flag's space-separated value is never mistaken for it.
func TestSplitPositional(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantRepo string
		wantRest []string
	}{
		{
			name:     "name first",
			args:     []string{"widget", "-dry-run"},
			wantRepo: "widget",
			wantRest: []string{"-dry-run"},
		},
		{
			name:     "name last",
			args:     []string{"-dry-run", "widget"},
			wantRepo: "widget",
			wantRest: []string{"-dry-run"},
		},
		{
			name:     "flag value is not the name",
			args:     []string{"-prefix", "wg", "widget"},
			wantRepo: "widget",
			wantRest: []string{"-prefix", "wg"},
		},
		{
			name:     "equals form",
			args:     []string{"-prefix=wg", "widget"},
			wantRepo: "widget",
			wantRest: []string{"-prefix=wg"},
		},
		{
			name:     "bool flag does not swallow the name",
			args:     []string{"-with-remote", "widget"},
			wantRepo: "widget",
			wantRest: []string{"-with-remote"},
		},
		{
			name:     "no name",
			args:     []string{"-dry-run"},
			wantRepo: "",
			wantRest: []string{"-dry-run"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo, rest := splitPositional(tc.args)
			if repo != tc.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tc.wantRepo)
			}
			if !reflect.DeepEqual(rest, tc.wantRest) {
				t.Errorf("rest = %v, want %v", rest, tc.wantRest)
			}
		})
	}
}

// TestParseInitFlagsDefaultsTarget: with no -dir, the skeleton lands in a
// directory named after the repo, never in the working directory.
func TestParseInitFlagsDefaultsTarget(t *testing.T) {
	opts, err := parseInitFlags([]string{"widget"})
	if err != nil {
		t.Fatalf("parseInitFlags: %v", err)
	}
	if opts.target != "widget" {
		t.Errorf("target = %q, want widget", opts.target)
	}
	if opts.withRemote {
		t.Error("withRemote defaults on — GitHub state must be opt-in")
	}
	if opts.dryRun || opts.filesOnly {
		t.Error("dryRun/filesOnly default on")
	}
}

// TestParseInitFlagsRejectsTwoNames: two bare arguments is a typo, not a spec.
func TestParseInitFlagsRejectsTwoNames(t *testing.T) {
	if _, err := parseInitFlags([]string{"widget", "gadget"}); err == nil {
		t.Fatal("want error for two repo names, got nil")
	}
}

// TestParseInitFlagsRequiresAName.
func TestParseInitFlagsRequiresAName(t *testing.T) {
	if _, err := parseInitFlags([]string{"-dry-run"}); err == nil {
		t.Fatal("want error for a missing repo name, got nil")
	}
}

// TestBoolFlagsMatchTheFlagSet: splitPositional's bool list must stay in step
// with the flags actually declared, or a repo name gets eaten as a value.
func TestBoolFlagsMatchTheFlagSet(t *testing.T) {
	for name := range boolFlags {
		if name == "h" || name == "help" {
			continue // provided by the flag package itself
		}
		if _, err := parseInitFlags([]string{"widget", "-" + name}); err != nil {
			t.Errorf("-%s is listed as a bool flag but the flag set rejects it: %v", name, err)
		}
	}
}
