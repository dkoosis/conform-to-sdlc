package checks_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dkoosis/repo-standard/internal/checks"
	"github.com/dkoosis/repo-standard/internal/lintpack"
	"github.com/dkoosis/repo-standard/internal/values"
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

// withExclusionRule returns the canonical config with rule (YAML, indented as
// an exclusions.rules item) added first in linters.exclusions.rules.
func withExclusionRule(t *testing.T, rule string) string {
	t.Helper()
	const anchor = "    rules:\n      # Tests"
	if strings.Count(checks.CanonicalGolangci, anchor) != 1 {
		t.Fatal("canonical config has no single exclusions.rules anchor")
	}
	return strings.Replace(checks.CanonicalGolangci, anchor, "    rules:\n"+rule+"      # Tests", 1)
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
			want:    []string{"drops depguard, which is neither a canonical linter"},
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
			name:    "errcheck leaves check-blank unset",
			golangc: strings.Replace(checks.CanonicalGolangci, "      check-blank: true\n", "", 1),
			want:    []string{"set linters.settings.errcheck.check-blank: true"},
		},
		{
			name:    "errcheck sets check-blank false",
			golangc: strings.Replace(checks.CanonicalGolangci, "check-blank: true", "check-blank: false", 1),
			want:    []string{"linters.settings.errcheck.check-blank"},
		},
		{
			name:    "check-blank unset but kept with its reason",
			golangc: strings.Replace(checks.CanonicalGolangci, "      check-blank: true\n", "", 1),
			values:  `{"profile": "tool", "exceptions": [{"rule": "lint-set", "drop": {"errcheck.check-blank": "a generated client discards by design"}}]}`,
		},
		{
			name:    "check-blank with the test-file exclusion from the lintbrush baseline passes",
			golangc: withExclusionRule(t, "      - path: _test\\.go$\n        linters:\n          - errcheck\n        text: Error return value\n        source: '(^|[^A-Za-z0-9_.])_\\s*(:=|=[^=])'\n"),
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
			name:    "linter excluded on every path",
			golangc: withExclusionRule(t, "      - linters:\n          - wrapcheck\n          - gosec\n        path: '.*'\n"),
			want:    []string{"excluded on every file by linters.exclusions: gosec, wrapcheck"},
		},
		{
			name:    "linter excluded with no path",
			golangc: withExclusionRule(t, "      - linters:\n          - dupl\n"),
			want:    []string{"excluded on every file by linters.exclusions: dupl"},
		},
		{
			name:    "every path excluded",
			golangc: strings.Replace(checks.CanonicalGolangci, "    generated: lax\n    presets:", "    generated: lax\n    paths:\n      - '.*'\n    presets:", 1),
			want:    []string{"excluded on every file by linters.exclusions: asciicheck"},
		},
		{
			name:    "linter excluded by a catch-all text",
			golangc: withExclusionRule(t, "      - linters:\n          - gosec\n        text: '.*'\n"),
			want:    []string{"excluded on every file by linters.exclusions: gosec"},
		},
		{
			name:    "linter excluded by a catch-all source",
			golangc: withExclusionRule(t, "      - linters:\n          - gosec\n        source: '^'\n"),
			want:    []string{"excluded on every file by linters.exclusions: gosec"},
		},
		{
			name:    "linter excluded except on a path no file has",
			golangc: withExclusionRule(t, "      - linters:\n          - gosec\n        path-except: '^nothing$'\n"),
			want:    []string{"excluded on every file by linters.exclusions: gosec"},
		},
		{
			name:    "paths-except that spares no file",
			golangc: strings.Replace(checks.CanonicalGolangci, "    generated: lax\n    presets:", "    generated: lax\n    paths-except:\n      - '^nothing$'\n    presets:", 1),
			want:    []string{"excluded on every file by linters.exclusions: asciicheck"},
		},
		{
			name:    "path-except that spares real files passes",
			golangc: withExclusionRule(t, "      - linters:\n          - gosec\n        path-except: '^internal/'\n"),
		},
		{
			name:    "linter excluded by a one-character text",
			golangc: withExclusionRule(t, "      - linters:\n          - gosec\n        text: '.'\n"),
			want:    []string{"excluded on every file by linters.exclusions: gosec"},
		},
		{
			name:    "catch-all rule with no linters list",
			golangc: withExclusionRule(t, "      - path: '.*'\n        text: '.+'\n"),
			want:    []string{"excluded on every file by linters.exclusions: asciicheck"},
		},
		{
			name:    "rule with no linters list scoped by text passes",
			golangc: withExclusionRule(t, "      - path: '.*'\n        text: 'Error return value of .os\\.Setenv. is not checked'\n"),
		},
		{
			name:    "exclusion scoped by text passes",
			golangc: withExclusionRule(t, "      - linters:\n          - wrapcheck\n        text: 'sig: func os\\.'\n"),
		},
		{
			name:    "allow-unused kept with its reason",
			golangc: strings.Replace(checks.CanonicalGolangci, "require-specific: true", "require-specific: true\n      allow-unused: true", 1),
			values:  `{"profile": "tool", "exceptions": [{"rule": "lint-set", "drop": {"nolintlint.allow-unused": "a newer linter build calls a still-needed directive dead"}}]}`,
		},
		{
			name:    "a setting drop excuses only that setting",
			golangc: strings.Replace(withoutLinter(t, "dupl"), "require-specific: true", "require-specific: true\n      allow-unused: true", 1),
			values:  `{"profile": "tool", "exceptions": [{"rule": "lint-set", "drop": {"nolintlint.allow-unused": "a newer linter build calls a still-needed directive dead"}}]}`,
			want:    []string{"canonical linters not enabled: dupl"},
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
				files["docs/repo-standard.json"] = tt.values + "\n"
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
			name: "present under another name",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, ".golangci.yaml"), []byte("version: \"2\"\nlinters:\n  default: none\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantFinding: true,
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
	// 50ms is a twentieth of the whole check's 1s budget, with room for the
	// race detector on a shared CI runner (about 11ms a run there).
	if per := time.Since(start) / 100; per > 50*time.Millisecond {
		t.Errorf("lint-set takes %v a run, want under 50ms", per)
	}
}

// TestLintSet_EverySettingCanBeKept: each setting lint-set reports has a key a
// drop may name, so keeping one never takes excepting the whole rule.
func TestLintSet_EverySettingCanBeKept(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("lintset.go")
	if err != nil {
		t.Fatal(err)
	}
	ids := checks.SettingIDs()
	re := regexp.MustCompile(`weak\("linters\.settings\.([^"]+)"`)
	found := re.FindAllStringSubmatch(string(src), -1)
	if len(found) < len(ids) {
		t.Fatalf("found %d weak settings in lintset.go, want at least %d", len(found), len(ids))
	}
	for _, m := range found {
		if !ids[m[1]] {
			t.Errorf("setting %s is reported but a drop cannot name it", m[1])
		}
	}
}

// TestLintSet_ConfigUnderAnotherName: a repo whose golangci-lint config is
// .golangci.yaml fails lint-set and names that file, even beside a canonical
// .golangci.yml golangci-lint does not load.
func TestLintSet_ConfigUnderAnotherName(t *testing.T) {
	t.Parallel()
	for _, other := range []string{".golangci.yaml", ".golangci.toml", ".golangci.json"} {
		dir := writeRepo(t, map[string]string{".golangci.yml": checks.CanonicalGolangci, other: "version: \"2\"\n"})
		got := checks.CheckLintSet(dir, values.Values{})
		if len(got) != 1 || got[0].File != other || !strings.Contains(got[0].Repair, "rename "+other) {
			t.Errorf("%s: findings = %+v, want one naming %s and its rename", other, got, other)
		}
	}
}

// TestCanonicalGolangciExemptsBlankDiscardInTests: the canonical config sets
// check-blank and carries the _test.go exclusion lintbrush's baseline ships,
// so --fix writes a config whose tests are exempt and lint-set accepts it.
func TestCanonicalGolangciExemptsBlankDiscardInTests(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"check-blank: true", `path: _test\.go$`, "text: Error return value"} {
		if !strings.Contains(checks.CanonicalGolangci, want) {
			t.Errorf("canonical config lacks %q", want)
		}
	}
	base, err := lintpack.Bytes("golangci.baseline.yml")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^ +source: .*$`)
	if got, want := re.FindString(checks.CanonicalGolangci), re.FindString(string(base)); got == "" || got != want {
		t.Errorf("canonical source filter %q differs from the embedded baseline's %q", got, want)
	}
}
