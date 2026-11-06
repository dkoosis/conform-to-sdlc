package checks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

// TestLintFloor_FleetFixtures validates the parser against the five shipped
// ccp-sbp.2 baseline-floor configs (loto#230 canapay#153 ferret#116 fo#294
// strand#107): every fleet
// .golangci.yml as merged must pass the floor with zero findings.
func TestLintFloor_FleetFixtures(t *testing.T) {
	t.Parallel()

	fixtures, err := filepath.Glob("testdata/fixtures/*.golangci.yml")
	if err != nil || len(fixtures) != 5 {
		t.Fatalf("want the 5 fleet fixtures, got %d (err %v)", len(fixtures), err)
	}

	for _, fixture := range fixtures {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			dir := writeRepo(t, map[string]string{".golangci.yml": string(data)})

			if findings := checks.CheckLintFloor(dir); len(findings) != 0 {
				t.Errorf("shipped fleet config tripped the floor: %v", findings)
			}
		})
	}
}

// TestLintFloor_Violations: each way off the floor is its own finding.
func TestLintFloor_Violations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		yml      string
		wantRule string
		wantMsg  string // substring the finding must carry
	}{
		{
			name:     "default not standard",
			yml:      strings.Replace(goodGolangci, "default: standard", "default: none", 1),
			wantRule: checks.RuleLintFloor,
			wantMsg:  `"none"`,
		},
		{
			name:     "floor linter missing from enable",
			yml:      strings.Replace(goodGolangci, "    - rowserrcheck\n", "", 1),
			wantRule: checks.RuleLintFloor,
			wantMsg:  "rowserrcheck",
		},
		{
			name:     "floor linter disabled",
			yml:      goodGolangci + "  disable:\n    - errcheck\n",
			wantRule: checks.RuleLintFloor,
			wantMsg:  "errcheck",
		},
		{
			name:     "nolintlint without explanation requirement",
			yml:      strings.Replace(goodGolangci, "require-explanation: true", "require-explanation: false", 1),
			wantRule: checks.RuleLintFloor,
			wantMsg:  "nolintlint",
		},
		{
			name:     "contextcheck reintroduced",
			yml:      strings.Replace(goodGolangci, "    - nilerr\n", "    - nilerr\n    - contextcheck\n", 1),
			wantRule: checks.RuleLintContext,
			wantMsg:  "scoped-away noise",
		},
		{
			name:     "unparseable yaml",
			yml:      "linters: [broken",
			wantRule: checks.RuleLintFloor,
			wantMsg:  "unparseable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeRepo(t, map[string]string{".golangci.yml": tt.yml})
			findings := checks.CheckLintFloor(dir)

			found := false
			for _, f := range findings {
				if f.Rule == tt.wantRule && strings.Contains(f.Msg, tt.wantMsg) {
					found = true
					if f.File != ".golangci.yml" {
						t.Errorf("finding names file %q, want .golangci.yml", f.File)
					}
					if f.Repair == "" {
						t.Error("finding carries no repair command")
					}
				}
			}
			if !found {
				t.Errorf("no %s finding containing %q in %v", tt.wantRule, tt.wantMsg, findings)
			}
		})
	}
}

// TestLintFloor_MissingConfig: a repo with no .golangci.yml at all.
func TestLintFloor_MissingConfig(t *testing.T) {
	t.Parallel()

	dir := writeRepo(t, map[string]string{"Makefile": goodMakefile})
	if n := rulesOf(checks.CheckLintFloor(dir))[checks.RuleLintFloor]; n == 0 {
		t.Error("missing .golangci.yml produced no lint-floor finding")
	}
}
