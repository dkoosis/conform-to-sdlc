package checks_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
	"github.com/dkoosis/conform-to-sdlc/internal/values"
)

// lintSetFindings runs the whole check and keeps the lint-set findings, so a
// case proves the rule is registered and nothing else fires.
// A rule named in also may fire too: lint-floor guards some of the same
// settings on its own.
func lintSetFindings(t *testing.T, files map[string]string, also string) []checks.Finding {
	t.Helper()
	dir := writeRepo(t, files)
	var out []checks.Finding
	for _, f := range checks.Run(dir) {
		switch f.Rule {
		case checks.RuleLintSet:
			out = append(out, f)
		case also:
		default:
			t.Errorf("a finding outside lint-set fired: %+v", f)
		}
	}
	return out
}

// withoutLinter returns the canonical config with one linter's enable line
// removed.
func withoutLinter(t *testing.T, linter string) string {
	t.Helper()
	line := "\n    - " + linter + "\n"
	if strings.Count(checks.CanonicalGolangci, line) != 1 {
		t.Fatalf("canonical config does not list %s exactly once", linter)
	}
	return strings.Replace(checks.CanonicalGolangci, line, "\n", 1)
}

func TestLintSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		golangc string
		values  string
		also    string   // another rule that may fire on the same fixture
		want    []string // substrings, one finding each, any order; nil = the repo passes
	}{
		{
			name:    "canonical config",
			golangc: checks.CanonicalGolangci,
		},
		{
			name:    "one linter missing",
			golangc: withoutLinter(t, "dupl"),
			want:    []string{"canonical linters not enabled: dupl"},
		},
		{
			name:    "one linter dropped with its reason",
			golangc: withoutLinter(t, "dupl"),
			values:  `{"profile": "tool", "exceptions": [{"rule": "lint-set", "drop": {"dupl": "generated tables repeat by design"}}]}`,
		},
		{
			name:    "drop with an empty reason",
			golangc: withoutLinter(t, "dupl"),
			values:  `{"profile": "tool", "exceptions": [{"rule": "lint-set", "drop": {"dupl": " "}}]}`,
			want:    []string{"drops dupl with no reason", "canonical linters not enabled: dupl"},
		},
		{
			name:    "drop of a linter outside the set",
			golangc: checks.CanonicalGolangci,
			values:  `{"profile": "tool", "exceptions": [{"rule": "lint-set", "drop": {"depguard": "not in the set"}}]}`,
			want:    []string{"drops depguard, which is not in the canonical set"},
		},
		{
			name:    "nolintlint without require-explanation",
			golangc: strings.Replace(checks.CanonicalGolangci, "require-explanation: true", "require-explanation: false", 1),
			also:    checks.RuleLintFloor,
			want:    []string{"linters.settings.nolintlint.require-explanation"},
		},
		{
			name:    "nolintlint allows unused",
			golangc: strings.Replace(checks.CanonicalGolangci, "require-specific: true", "require-specific: true\n      allow-unused: true", 1),
			want:    []string{"linters.settings.nolintlint.allow-unused"},
		},
		{
			name:    "errcheck excludes a call beyond the canonical list",
			golangc: strings.Replace(checks.CanonicalGolangci, "        - fmt.Fprintln\n", "        - fmt.Fprintln\n        - (*database/sql.Rows).Close\n", 1),
			want:    []string{"(*database/sql.Rows).Close"},
		},
		{
			name:    "ruleguard rules lack modern.go",
			golangc: strings.Replace(checks.CanonicalGolangci, ",${base-path}/.golangci-rules/modern.go", "", 1),
			want:    []string{"does not name .golangci-rules/modern.go"},
		},
		{
			name:    "forbidigo matches by text",
			golangc: strings.Replace(checks.CanonicalGolangci, "analyze-types: true", "analyze-types: false", 1),
			want:    []string{"linters.settings.forbidigo.analyze-types"},
		},
		{
			name:    "linter switched off in disable",
			golangc: strings.Replace(checks.CanonicalGolangci, "    - godox\n", "    - godox\n    - wrapcheck\n", 1),
			want:    []string{"canonical linters not enabled: wrapcheck"},
		},
		{
			name:    "two linters beyond the set",
			golangc: strings.Replace(checks.CanonicalGolangci, "  enable:\n", "  enable:\n    - depguard\n    - gomoddirectives\n", 1),
		},
		{
			name:    "default all with nothing disabled",
			golangc: strings.Replace(checks.CanonicalGolangci, "default: standard", "default: all", 1),
			also:    checks.RuleLintFloor,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files := goodRepo()
			files[".golangci.yml"] = tt.golangc
			if tt.values != "" {
				files["docs/conform.json"] = tt.values + "\n"
			}
			got := lintSetFindings(t, files, tt.also)
			if len(got) != len(tt.want) {
				t.Fatalf("lint-set findings = %+v, want %d matching %q", got, len(tt.want), tt.want)
			}
			for _, w := range tt.want {
				if !slices.ContainsFunc(got, func(f checks.Finding) bool {
					return strings.Contains(f.Msg+" "+f.Repair, w) && f.Repair != ""
				}) {
					t.Errorf("no finding with a repair names %q in %+v", w, got)
				}
			}
		})
	}
}

// TestLintSet_MissingNamesTheRepair: the finding for a missing linter tells
// the reader both ways out.
func TestLintSet_MissingNamesTheRepair(t *testing.T) {
	t.Parallel()
	dir := writeRepo(t, map[string]string{".golangci.yml": withoutLinter(t, "wrapcheck")})
	got := checks.CheckLintSet(dir, values.Values{})
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want 1", got)
	}
	f := got[0]
	for _, w := range []string{"add to linters.enable", "wrapcheck", `"rule": "lint-set", "drop"`} {
		if !strings.Contains(f.Repair, w) {
			t.Errorf("repair %q does not name %q", f.Repair, w)
		}
	}
	if f.File != ".golangci.yml" || f.Rule != "lint-set" {
		t.Errorf("finding = %+v, want .golangci.yml under lint-set", f)
	}
}

// TestCanonicalGolangciHoldsTheFloor: the canonical config passes lint-floor
// and enables every floor linter, so --fix never writes a config another rule
// fails.
func TestCanonicalGolangciHoldsTheFloor(t *testing.T) {
	t.Parallel()
	dir := writeRepo(t, map[string]string{".golangci.yml": checks.CanonicalGolangci})
	if got := checks.CheckLintFloor(dir); len(got) != 0 {
		t.Fatalf("canonical config fails lint-floor: %+v", got)
	}
	var cfg struct {
		Linters struct {
			Default string   `yaml:"default"`
			Enable  []string `yaml:"enable"`
		} `yaml:"linters"`
	}
	if err := yaml.Unmarshal([]byte(checks.CanonicalGolangci), &cfg); err != nil {
		t.Fatal(err)
	}
	on := map[string]bool{}
	for _, l := range cfg.Linters.Enable {
		if on[l] {
			t.Errorf("canonical config enables %s twice", l)
		}
		on[l] = true
	}
	for _, l := range checks.FloorLinters() {
		if !on[l] && (cfg.Linters.Default != "standard" || l != "errcheck") {
			t.Errorf("canonical config does not enable floor linter %s", l)
		}
	}
}

// TestFix_Golangci: --fix writes the canonical .golangci.yml only where none
// exists; a present one, or an unwritable directory, leaves the repo as it was.
func TestFix_Golangci(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		setup       func(t *testing.T, dir string)
		wantWrote   bool
		wantErr     bool
		wantFinding bool // lint-set finding after the fix
	}{
		{
			name:      "absent",
			setup:     func(*testing.T, string) {},
			wantWrote: true,
		},
		{
			name: "present",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, ".golangci.yml"), []byte("version: \"2\"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantFinding: true,
		},
		{
			name: "unwritable",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // restore so TempDir can clean up
				probe := filepath.Join(dir, "probe")
				if err := os.WriteFile(probe, nil, 0o600); err == nil {
					_ = os.Remove(probe) // probe only
					t.Skip("running as a user who can write a 0500 directory (root); unwritable case cannot be built")
				}
			},
			wantErr:     true,
			wantFinding: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			files := goodRepo()
			delete(files, ".golangci.yml")
			dir := writeRepo(t, files)
			tc.setup(t, dir)
			before := snapshot(t, dir)

			done, err := checks.Fix(dir)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Fix error = %v, want error %v", err, tc.wantErr)
			}
			if wrote := anyContains(done, ".golangci.yml"); wrote != tc.wantWrote {
				t.Errorf("Fix reported writing .golangci.yml = %v, want %v (%v)", wrote, tc.wantWrote, done)
			}
			if !tc.wantWrote {
				if after := snapshot(t, dir); after != before {
					t.Errorf("Fix changed the repo:\nbefore %s\nafter  %s", before, after)
				}
			} else {
				got, err := os.ReadFile(filepath.Join(dir, ".golangci.yml"))
				if err != nil || string(got) != checks.CanonicalGolangci {
					t.Errorf("Fix wrote %q (err %v), want the canonical config", got, err)
				}
			}
			if got := rulesOf(checks.Run(dir))[checks.RuleLintSet] > 0; got != tc.wantFinding {
				t.Errorf("lint-set finding after Fix = %v, want %v", got, tc.wantFinding)
			}
		})
	}
}

// TestLintSet_Fast: the rule runs well inside the check's one-second budget.
func TestLintSet_Fast(t *testing.T) {
	t.Parallel()
	dir := writeRepo(t, goodRepo())
	vals := values.Values{}
	start := time.Now()
	for range 100 {
		checks.CheckLintSet(dir, vals)
	}
	if per := time.Since(start) / 100; per > 10*time.Millisecond {
		t.Errorf("lint-set takes %v a run, want under 10ms", per)
	}
}
