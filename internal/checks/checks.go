// Package checks implements Surface 1: the in-repo file checks that run bare
// (`repo-standard`) inside a repository, every `make check`, hard-fail.
//
// Every finding names the file, the rule id, and a repair command. There is no
// soft-fail: a rule too noisy to hard-fail gets deleted, not warned.
//
// Rule ids are the shared vocabulary between this checker and the values
// file's exceptions (see internal/values): a repo excepts a rule by listing
// its id in repo-standard.json with a reason. Ids are kebab-case and stable.
package checks

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/dkoosis/repo-standard/internal/values"
)

// ValuesFile is the repo's repo-standard.json, under docs/ like every other
// repo-level declaration: the root is minimal (decision d9cd0e20868b). Only
// dotfiles a host tool reads at the root by name stay there; repo-standard reads its
// own file wherever it says, so it says docs/.
const ValuesFile = "docs/repo-standard.json"

// LegacyValuesFiles are where the file sat under the tool's old name,
// conform-to-sdlc, best first: docs/ and then the root, where five of seven
// fleet repos declared it. repo-standard still reads them when ValuesFile is
// absent, so a repo keeps its profile and exceptions until its own pin bump
// moves the file; checkRetiredFiles reports the rename as a finding. Reading
// is not blessing: read here, flagged there. Delete this list and its
// fallbacks once no fleet repo holds an old-name file.
var LegacyValuesFiles = []string{"docs/conform.json", "conform.json"}

// Surface-1 rule ids. Other surfaces reserve their own ids in the same
// vocabulary (e.g. `no-git-ops`, honored by --local for repos like loto that
// declare git operations out of scope).
const (
	RuleValuesFile      = "values-file"          // docs/repo-standard.json present and valid
	RuleMakefileVerb    = "makefile-verbs"       // four-verb contract + prereq composition
	RuleMakefileDocs    = "makefile-docs"        // every target carries a ## doc comment
	RuleLintFloor       = "lint-floor"           // .golangci.yml carries the 6-linter baseline floor
	RuleLintContext     = "lint-contextcheck"    // contextcheck must stay demoted
	RuleLintSet         = values.DropRule        // .golangci.yml enables the canonical linter set at its strict settings
	RuleLintPack        = "lint-pack"            // .golangci-rules/ is byte-equal to the lintbrush pack repo-standard embeds
	RuleLintPin         = "lint-pin"             // exactly one golangci-lint pin location
	RuleCIGate          = "ci-gate"              // CI calls make check; no gate re-implemented in YAML
	RuleCodexShape      = "codex-workflow"       // codex-review fires on issue_comment, never pull_request
	RuleRetiredFiles    = "retired-files"        // files folded into repo-standard are gone
	RuleBDConfig        = "bd-config"            // bd config keys present
	RuleHooksShape      = "hooks-shape"          // tracked .githooks the global hooks forward to
	RuleReadme          = "readme"               // README.md present, non-empty, opening with a heading
	RuleRootMinimal     = "root-minimal"         // no ROADMAP.md / repo-standard.json / NORTH_STAR.md / kg at the root
	RuleSandboxLib      = "sandbox-lib"          // .sandbox/lib matches the canonical copy repo-standard ships
	RuleAgentsBlock     = "agents-managed-block" // root AGENTS.md carries no block a tool wrote
	RuleCIDocsSkip      = "ci-docs-skip"         // a detect job lets a docs-only PR skip make check, context kept
	RuleGateSkip        = "gate-skip"            // a check or audit step exits 0 when its tool is missing
	RuleToolPin         = "tool-pin"             // a gate tool runs on PATH, at @latest, or at an unset version
	RuleFuzzCoverage    = "fuzz-coverage"        // a Fuzz function the fuzz target does not run
	RuleCIAuditSchedule = "ci-audit-schedule"    // no workflow runs make audit on a schedule
	RuleHookExitDiscard = "hook-exit-discard"    // a hook swallows a tool's exit status with nothing that surfaces the failure
)

// Finding is one contract violation: which file, which rule, what to run.
type Finding struct {
	File   string // repo-relative path
	Rule   string // rule id (stable, kebab-case)
	Msg    string
	Repair string // the command or edit that fixes it
}

func (f Finding) String() string {
	return fmt.Sprintf("%s: %s: %s\n    fix: %s", f.File, f.Rule, f.Msg, f.Repair)
}

// Run executes all Surface-1 checks against the repo rooted at dir and
// returns the findings, with the repo's declared exceptions (repo-standard.json)
// filtered out. A missing or invalid values file is itself a finding.
func Run(dir string) []Finding {
	vals, findings := loadValues(dir)

	findings = append(findings, checkMakefile(dir, vals.Profile)...)
	findings = append(findings, checkGateSteps(dir)...)
	findings = append(findings, checkFuzzCoverage(dir)...)
	findings = append(findings, checkLintFloor(dir)...)
	findings = append(findings, checkLintSet(dir, vals)...)
	findings = append(findings, checkLintPack(dir)...)
	findings = append(findings, checkLintPin(dir)...)
	findings = append(findings, checkCIGate(dir)...)
	findings = append(findings, checkCIDocsSkip(dir)...)
	findings = append(findings, checkCIAuditSchedule(dir)...)
	findings = append(findings, checkCodexShape(dir)...)
	findings = append(findings, checkRetiredFiles(dir)...)
	findings = append(findings, checkBDConfig(dir)...)
	findings = append(findings, checkHooksShape(dir)...)
	findings = append(findings, checkHookExitDiscard(dir)...)
	findings = append(findings, checkReadme(dir)...)
	findings = append(findings, checkRootMinimal(dir)...)
	findings = append(findings, checkAgentsBlock(dir)...)
	findings = append(findings, checkSandboxLib(dir)...)

	findings = applyExceptions(findings, vals)
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Rule < findings[j].Rule
	})
	return findings
}

// loadValues reads docs/repo-standard.json (ValuesFile), falling back to the
// old-name files (LegacyValuesFiles) while the fleet migrates. Absent or invalid, the checks
// still run — under the default tool profile with no exceptions — and the
// values problem is reported as a finding of its own.
//
// Only a MISSING docs/ file falls back. An invalid one is reported as it
// stands: silently preferring the root copy because the canonical file has a
// typo would hide the typo behind stale values.
func loadValues(dir string) (values.Values, []Finding) {
	fallback := values.Values{Profile: values.ProfileTool}
	path := filepath.Join(dir, ValuesFile)
	v, err := values.Load(path)
	if errors.Is(err, fs.ErrNotExist) {
		for _, old := range LegacyValuesFiles {
			if legacy, lerr := values.Load(filepath.Join(dir, old)); lerr == nil {
				return *legacy, nil
			}
		}
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fallback, []Finding{{
				File:   ValuesFile,
				Rule:   RuleValuesFile,
				Msg:    "values file missing — repo-standard needs the repo's profile (tool|lib) and declared exceptions",
				Repair: `create ` + ValuesFile + `: {"profile": "tool", "exceptions": []}`,
			}}
		}
		return fallback, []Finding{{
			File:   ValuesFile,
			Rule:   RuleValuesFile,
			Msg:    err.Error(),
			Repair: "fix " + ValuesFile + ": profile tool|lib; every exception needs rule + reason",
		}}
	}
	return *v, nil
}

// applyExceptions drops findings whose rule id the repo has excepted with a
// reason. The exception mechanism is the only sanctioned suppression — there
// is no severity dial and no warn level.
func applyExceptions(findings []Finding, vals values.Values) []Finding {
	if len(vals.Exceptions) == 0 {
		return findings
	}
	excepted := make(map[string]bool, len(vals.Exceptions))
	for _, e := range vals.Exceptions {
		if len(e.Drop) > 0 {
			continue // excuses only the linters it names; lint-set reads it
		}
		excepted[e.Rule] = true
		if e.Rule == RuleNoGitOps {
			// One word excepts the whole git-hook family (loto: a repo that
			// performs no git operations by design has no hook chain to keep
			// alive).
			for _, r := range noGitOpsFamily {
				excepted[r] = true
			}
		}
	}
	kept := findings[:0]
	for _, f := range findings {
		if !excepted[f.Rule] {
			kept = append(kept, f)
		}
	}
	return kept
}
