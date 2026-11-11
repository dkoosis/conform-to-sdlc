package checks_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/dkoosis/repo-standard/internal/checks"
)

// withRecipe replaces the one-line "name: ## doc" stub in goodMakefile with a
// target carrying the given recipe.
func withRecipe(target, recipe string) string {
	stub := target + ": ##"
	i := strings.Index(goodMakefile, "\n"+stub)
	j := strings.Index(goodMakefile[i+1:], "\n") + i + 1
	return goodMakefile[:j+1] + recipe + goodMakefile[j+1:]
}

const projectConfPins = "NILAWAY_VER=v0.0.0-20260918162853-acb8859b9031\nJSCPD_VER=5.1.2\n"

func gateFindings(t *testing.T, mk, conf string) []checks.Finding {
	t.Helper()
	dir := writeRepo(t, map[string]string{"Makefile": mk, ".sandbox/project.conf": conf})
	return checks.CheckGateSteps(dir)
}

func TestGateSkip_Fails(t *testing.T) {
	t.Parallel()
	skips := map[string]string{
		"negated if, echo only":      "\t@if ! command -v nilaway >/dev/null 2>&1; then \\\n\t\techo \"nilaway not installed — skipped\"; \\\n\telse \\\n\t\tgo run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./...; \\\n\tfi\n",
		"negated if, exit 0":         "\t@if ! command -v nilaway >/dev/null 2>&1; then \\\n\t\techo skipped; exit 0; \\\n\tfi\n",
		"positive if, no else":       "\t@if command -v nilaway >/dev/null 2>&1; then \\\n\t\tgo run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./...; \\\n\tfi\n",
		"silent if, later loud else": "\t@if command -v nilaway >/dev/null 2>&1; then \\\n\t\tgo run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./...; \\\n\tfi; \\\n\tif [ -f go.sum ]; then :; else echo no go.sum >&2; exit 1; fi\n",
		"silent and-or, later exit":  "\t@command -v nilaway >/dev/null 2>&1 && go run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./... || echo skipped; \\\n\ttest -f go.sum || exit 1\n",
		"nested in another if":       "\t@if [ -f go.sum ]; then command -v nilaway >/dev/null || echo skipped; fi\n",
		"inner if after then":        "\t@if [ -f go.sum ]; then if ! command -v nilaway >/dev/null; then echo skipped; exit 0; fi; fi\n",
		"inner if after fi":          "\t@if [ -f a ]; then :; fi;if ! command -v nilaway >/dev/null; then echo skipped; exit 0; fi\n",
		"or-branch":                  "\t@command -v nilaway >/dev/null || echo skipped\n",
		"and-run":                    "\t@command -v nilaway >/dev/null 2>&1 && go run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./...; echo done\n",
	}
	for name, recipe := range skips {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := gateFindings(t, withRecipe("nilcheck", recipe), "")
			if len(got) != 1 || got[0].Rule != checks.RuleGateSkip || !strings.Contains(got[0].Msg, `"nilcheck"`) || !strings.Contains(got[0].Msg, "line ") {
				t.Fatalf("want one gate-skip naming the target and line, got %v", got)
			}
		})
	}
}

func TestGateSkip_Passes(t *testing.T) {
	t.Parallel()
	loud := "\t@if ! command -v nilaway >/dev/null 2>&1; then \\\n\t\techo \"nilaway not installed\" >&2; exit 1; \\\n\tfi; go run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./...\n"
	loudOr := "\t@command -v nilaway >/dev/null || { echo missing >&2; exit 1; }; go run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./...\n"
	loudElse := "\t@if command -v nilaway >/dev/null 2>&1; then go run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./...; else echo \"nilaway not installed\" >&2; exit 1; fi\n"
	andOr := "\t@command -v nilaway >/dev/null 2>&1 && go run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./... || { echo missing >&2; exit 1; }\n"
	contLoud := "\t@command -v nilaway >/dev/null 2>&1 || \\\n\t\t{ echo missing >&2; exit 1; }; go run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./...\n"
	for name, recipe := range map[string]string{"continued loud or": contLoud, "if": loud, "or": loudOr, "positive if, loud else": loudElse, "and-run, loud or": andOr} {
		if got := gateFindings(t, withRecipe("nilcheck", recipe), ""); len(got) != 0 {
			t.Errorf("%s: loud missing branch produced %v", name, got)
		}
	}
	// `T && run` as the last statement fails when T is missing: not a skip.
	if got := gateFindings(t, withRecipe("nilcheck", "\t@command -v nilaway >/dev/null 2>&1 && go run go.uber.org/nilaway/cmd/nilaway@v1.0.0 ./...\n"), ""); len(got) != 0 {
		t.Errorf("trailing and-run produced %v", got)
	}
	// A skip in a target audit does not reach is a deploy step, not a gate.
	mk := goodMakefile + "deploy-sign: ## sign\n\t@command -v nilaway >/dev/null || echo skipped\n"
	if got := gateFindings(t, mk, ""); len(got) != 0 {
		t.Errorf("unreachable skip produced %v", got)
	}
	// A repo-specific tool is out of the rule.
	if got := gateFindings(t, withRecipe("nilcheck", "\t@command -v mytool >/dev/null || echo skipped\n"), ""); len(got) != 0 {
		t.Errorf("unknown tool produced %v", got)
	}
}

// The omitempty gate holds the PR 60 rules: no skip when the tool is absent,
// no unpinned run.
func TestOmitemptyGate_Rules(t *testing.T) {
	t.Parallel()
	const pinned = "\tGOPRIVATE='github.com/dkoosis/*' go run github.com/dkoosis/lintbrush/gorules/omitemptyscalar/cmd/omitemptyscalar@$(OMITEMPTYSCALAR_VER) ./...\n"
	if got := gateFindings(t, "OMITEMPTYSCALAR_VER := v0.1.0\n"+withRecipe("omitempty", pinned), ""); len(got) != 0 {
		t.Errorf("pinned omitempty gate produced %v", got)
	}
	skip := "\t@command -v omitemptyscalar >/dev/null || echo skipped\n"
	got := gateFindings(t, withRecipe("omitempty", skip), "")
	if !slices.ContainsFunc(got, func(f checks.Finding) bool {
		return f.Rule == checks.RuleGateSkip && strings.Contains(f.Msg, "omitemptyscalar missing passes the step")
	}) {
		t.Errorf("skip branch not flagged: %v", got)
	}
}

func TestToolPin_Fails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, target, recipe, extra, conf, want string
	}{
		{"bare on PATH", "vuln", "\tgovulncheck ./...\n", "", "", "runs from PATH"},
		{"go run @latest", "vuln", "\tgo run golang.org/x/vuln/cmd/govulncheck@latest ./...\n", "", "", "@latest"},
		{"npx no version", "dupe", "\tnpx --yes jscpd --threshold 3 .\n", "", "", "npx with no version"},
		{"version var latest", "vuln", "\tgo run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VER) ./...\n", "GOVULNCHECK_VER := latest\n", "", `"latest" at Makefile line`},
		{"omitempty bare on PATH", "omitempty", "\tomitemptyscalar ./...\n", "", "", "runs from PATH"},
		{"omitempty @latest", "omitempty", "\tGOPRIVATE='github.com/dkoosis/*' go run github.com/dkoosis/lintbrush/gorules/omitemptyscalar/cmd/omitemptyscalar@latest ./...\n", "", "", "@latest"},
		{"omitempty version unset", "omitempty", "\tgo run github.com/dkoosis/lintbrush/gorules/omitemptyscalar/cmd/omitemptyscalar@$(OMITEMPTYSCALAR_VER) ./...\n", "", "", "add OMITEMPTYSCALAR_VER="},
		{"version missing from project.conf", "nilcheck", "\tgo run go.uber.org/nilaway/cmd/nilaway@$(NILAWAY_VER) ./...\n", "", "JSCPD_VER=5.1.2\n", "add NILAWAY_VER="},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := gateFindings(t, tc.extra+withRecipe(tc.target, tc.recipe), tc.conf)
			if len(got) != 1 || got[0].Rule != checks.RuleToolPin || !strings.Contains(got[0].Msg, tc.want) || got[0].Repair == "" {
				t.Fatalf("want one tool-pin containing %q, got %v", tc.want, got)
			}
		})
	}
}

func TestToolPin_Passes(t *testing.T) {
	t.Parallel()
	mk := withRecipe("nilcheck", "\tgo run go.uber.org/nilaway/cmd/nilaway@$(NILAWAY_VER) -include-pkgs=x ./...\n")
	mk = strings.Replace(mk, "\ndupe: ## Duplication\n", "\ndupe: ## Duplication\n\tnpx --yes jscpd@$(JSCPD_VER) .\n", 1)
	if got := gateFindings(t, "-include .sandbox/project.conf\n"+mk, projectConfPins); len(got) != 0 {
		t.Errorf("pinned forms produced %v", got)
	}
	// Sourcing project.conf inside the recipe also makes the key visible.
	src := withRecipe("dupe", "\t. ./.sandbox/project.conf && npx --yes jscpd@$$JSCPD_VER .\n")
	if got := gateFindings(t, src, projectConfPins); len(got) != 0 {
		t.Errorf("recipe-sourced version produced %v", got)
	}
	// A version set in the Makefile itself, and a literal version, pass.
	mk = "GOVULNCHECK_VER := v1.9.0\n" + withRecipe("vuln", "\tgo run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VER) ./...\n\tgo run golang.org/x/tools/cmd/deadcode@v0.52.0 ./...\n")
	if got := gateFindings(t, mk, ""); len(got) != 0 {
		t.Errorf("Makefile-pinned forms produced %v", got)
	}
	// Tool names in an echo or a presence test are not runs.
	mk = withRecipe("vuln", "\t@echo \"install govulncheck: go install golang.org/x/vuln/cmd/govulncheck@latest\"\n")
	if got := gateFindings(t, mk, ""); len(got) != 0 {
		t.Errorf("echoed tool names produced %v", got)
	}
	// Outside check and audit, anything goes.
	mk = goodMakefile + "tools: ## install\n\tgo install golang.org/x/vuln/cmd/govulncheck@latest\n"
	if got := gateFindings(t, mk, ""); len(got) != 0 {
		t.Errorf("unreachable target produced %v", got)
	}
}

// The shared sandbox lib sets tool versions; an include that sets one counts.
func TestToolPin_VersionFromInclude(t *testing.T) {
	t.Parallel()
	mk := "include .sandbox/lib/Makefile.cross.mk\n" + withRecipe("vuln", "\tgo run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VER) ./...\n")
	dir := writeRepo(t, map[string]string{"Makefile": mk, ".sandbox/lib/Makefile.cross.mk": "GOVULNCHECK_VER ?= v1.1.4\n"})
	if got := checks.CheckGateSteps(dir); len(got) != 0 {
		t.Errorf("version set by an include produced %v", got)
	}
}

func TestToolPin_FlagValueAndHash(t *testing.T) {
	t.Parallel()
	mk := withRecipe("vuln", "\tgolangci-lint run --enable staticcheck ./...\n\tgo run go.uber.org/nilaway/cmd/nilaway@acb8859b9031 ./...\n")
	if got := gateFindings(t, mk, ""); len(got) != 0 {
		t.Errorf("flag value and commit-hash pin produced %v", got)
	}
}

// A version only project.conf sets, with a Makefile that never reads it, is
// empty at make time: npx jscpd@ then runs at whatever version is current.
func TestToolPin_VersionMakeCannotSee(t *testing.T) {
	t.Parallel()
	mk := withRecipe("dupe", "\tnpx --yes jscpd@$(JSCPD_VER) .\n")
	got := gateFindings(t, mk, projectConfPins)
	if len(got) != 1 || got[0].Rule != checks.RuleToolPin || !strings.Contains(got[0].Msg, "make sees it empty") {
		t.Fatalf("want one tool-pin about the empty version, got %v", got)
	}
}
