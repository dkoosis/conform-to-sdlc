package checks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/dkoosis/repo-standard/internal/values"
)

// The four-verb contract: check · audit · deploy · help, identical in every
// repo; internals are compositions. Only the top-level Makefile is parsed —
// targets provided by .sandbox includes (doctor, cross*) are theirs to
// document.
var (
	// checkFloor is what `check` must compose, at minimum. Extra prereqs are
	// per-repo freedom.
	checkFloor = []string{"vet", "lint", "test", "build"}

	// targetFloor is every target a Go repo carries, whatever `check`
	// composes — the Makefile floor ratified on sd-dtxs.1 (2026-09-07). The
	// contract verbs check · audit · help · deploy are enforced separately.
	targetFloor = []string{"build", "test", "lint", "vet", "vuln", "standard", "race", "clean"}

	// auditGates is what `audit` must reach, through its prerequisites at any
	// depth (cfm-tvk, nug 284ca53cd396): the most rigorous static analysis
	// available, in every Go repo. alt names a target that serves as well.
	// doc and recipe are what the scaffold emits for the gate.
	auditGates = []auditGate{
		{name: "race", alt: "test-race", doc: "Run tests with the race detector (fresh run)", recipe: "go test -race -count=1 -cover ./..."},
		{name: "fuzz", doc: "Fuzz every Fuzz test (FUZZTIME=10s each)", recipe: `for pkg in $$(go list ./...); do for f in $$(go test -list '^Fuzz' $$pkg | sed -n '/^Fuzz/p'); do go test -run '^$$' -fuzz "^$$f$$" -fuzztime=$(FUZZTIME) $$pkg || exit 1; done; done`},
		// jscpd exits 0 at any duplication unless given a threshold; 3% is the fleet's .jscpd.json value.
		{name: "dupe", alt: "dupl", doc: "Fail above 3% code duplication (jscpd)", recipe: `npx --yes jscpd@$(JSCPD_VER) --threshold 3 --ignore '**/.git/**,**/.beads/**,**/.sandbox/**' .`},
		// -include-pkgs keeps nilaway's reports to code the repo can fix, not its dependencies.
		{name: "nilcheck", alt: "nilaway", doc: "Run nilaway on this module's packages", recipe: "go run go.uber.org/nilaway/cmd/nilaway@$(NILAWAY_VER) -include-pkgs=$$(go list -m) ./..."},
		// A scaffold has no main package yet, so it traces from the tests (-test); deadcode-gate fails that form once a main exists.
		{name: "deadcode", doc: "Fail on any function no test or main reaches", recipe: `out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) -test ./...) || { echo "deadcode: could not run"; exit 1; }; if [ -n "$$out" ]; then echo "$$out"; exit 1; fi`},
		// omitemptyscalar exits 3 on a ,omitempty tag on a bool. It ships in this module, first at v0.4.0.
		{name: "omitempty", doc: "Fail on a bool field tagged omitempty (omitemptyscalar)", recipe: "go run github.com/dkoosis/repo-standard/cmd/omitemptyscalar@$(OMITEMPTYSCALAR_VER) ./..."},
		{name: "vuln", doc: "Scan for known vulnerabilities", recipe: "go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VER) ./..."},
	}

	// gateVersions pins each tool an audit gate runs (tool-pin): the scaffold
	// writes them into the Makefile, in this order. Values are sdlc's pins.
	gateVersions = [][2]string{
		{"NILAWAY_VER", "v0.0.0-20260918162853-acb8859b9031"},
		{"GOVULNCHECK_VER", "v1.9.0"},
		{"DEADCODE_VER", "v0.52.0"},
		{"JSCPD_VER", "5.1.2"},
		{"OMITEMPTYSCALAR_VER", "v0.4.0"},
		{"REPO_STANDARD_VER", "v0.4.0"},
	}

	// toolTargets extend the floor for the tool profile only: a lib has no
	// binary to install or cross-compile, the same reason it carries no deploy.
	toolTargets = []string{"install", "cross"}

	// crossInclude: `cross` comes from the shared .sandbox include in most of
	// the fleet, so including it satisfies the floor without a top-level
	// target.
	crossInclude = regexp.MustCompile(`(?m)^-?include\s+\S*\.sandbox/lib/Makefile\.cross\.mk\s*$`)

	// targetLine matches a rule head at column 0: name, colon, and NOT an
	// assignment (`X := y`) or a double-colon assignment. Dot-targets
	// (.PHONY, .DEFAULT_GOAL) and pattern rules (%) are not contract
	// targets.
	targetLine = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_.-]*)\s*:($|[^=].*)`)
)

// makefileFile is the top-level Makefile every check and repair in this file
// (and the pin and scaffold rules) names — one constant so a rename can't
// leave a stray literal behind.
const makefileFile = "Makefile"

type auditGate struct{ name, alt, doc, recipe string }

type mkTarget struct {
	name    string
	prereqs []string
	doc     bool // carries a "##" help comment
}

// parseMakefile extracts contract-relevant targets from Makefile text.
func parseMakefile(text string) []mkTarget {
	var targets []mkTarget
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "#") {
			continue // recipe or comment
		}
		m := targetLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rest := m[2]
		doc := strings.Contains(rest, "##")
		if i := strings.Index(rest, "#"); i >= 0 {
			rest = rest[:i]
		}
		targets = append(targets, mkTarget{
			name:    m[1],
			prereqs: strings.Fields(rest),
			doc:     doc,
		})
	}
	return targets
}

// contractVerbs is the human surface: the same words in every repo.
var contractVerbs = []string{"check", "audit", "help"}

// requiredVerbs is the verb list for a profile — `deploy` is
// profile-dependent (tools ship it, libs must not have it; no-deploy is what
// the lib profile means). verbFindings checks against this list and the
// scaffold renderer emits from it, so the two cannot disagree.
func requiredVerbs(profile values.Profile) []string {
	verbs := slices.Clone(contractVerbs)
	if profile == values.ProfileTool {
		verbs = append(verbs, "deploy")
	}
	return verbs
}

// checkMakefile enforces the verb contract (makefile-verbs) and ## doc
// coverage (makefile-docs) on the top-level Makefile.
func checkMakefile(dir string, profile values.Profile) []Finding {
	data, err := os.ReadFile(filepath.Join(dir, makefileFile))
	if err != nil {
		return []Finding{{
			File:   makefileFile,
			Rule:   RuleMakefileVerb,
			Msg:    "no top-level Makefile — the four-verb contract (check · audit · deploy · help) has no home",
			Repair: "copy sdlc's Makefile (the reference) and adapt targets; a new repo gets one from `repo-standard init`",
		}}
	}

	targets := parseMakefile(string(data))
	byName := make(map[string]mkTarget, len(targets))
	for _, t := range targets {
		if _, dup := byName[t.name]; !dup {
			byName[t.name] = t
		}
	}

	findings := verbFindings(byName, profile)
	findings = append(findings, floorFindings(byName, profile, crossInclude.Match(data))...)
	findings = append(findings, docFindings(targets)...)
	return findings
}

// floorTargets is the target floor for a profile.
func floorTargets(profile values.Profile) []string {
	floor := slices.Clone(targetFloor)
	if profile == values.ProfileTool {
		floor = append(floor, toolTargets...)
	}
	return floor
}

// floorFindings enforces the target floor (makefile-verbs): every floor
// target exists, and `check` runs standard last so repo-standard grades the tree
// the rest of the gate just passed.
func floorFindings(byName map[string]mkTarget, profile values.Profile, crossIncluded bool) []Finding {
	var missing []string
	for _, name := range floorTargets(profile) {
		if _, ok := byName[name]; ok || (name == "cross" && crossIncluded) {
			continue
		}
		missing = append(missing, name)
	}
	var findings []Finding
	if len(missing) > 0 {
		repair := "add a documented target for each (cross may come from `include .sandbox/lib/Makefile.cross.mk`)"
		if _, old := byName["selfcheck"]; old && slices.Contains(missing, "standard") {
			repair = "rename the selfcheck target to standard (the tool was renamed repo-standard), and its use in check; " + repair
		}
		findings = append(findings, Finding{
			File:   makefileFile,
			Rule:   RuleMakefileVerb,
			Msg:    "floor targets missing: " + strings.Join(missing, ", ") + " — every Go repo answers the same targets (sd-dtxs.1 floor)",
			Repair: repair,
		})
	}
	if check, ok := byName["check"]; ok {
		if n := len(check.prereqs); n == 0 || check.prereqs[n-1] != "standard" {
			findings = append(findings, Finding{
				File:   makefileFile,
				Rule:   RuleMakefileVerb,
				Msg:    "check does not run standard last — repo-standard must grade the tree the rest of the gate passed",
				Repair: "make standard the last prerequisite of check",
			})
		}
	}
	return findings
}

// verbFindings enforces verb presence and prereq composition
// (makefile-verbs). `deploy` is profile-dependent: tools ship it, libs must
// not have it (no-deploy is what the lib profile means).
func verbFindings(byName map[string]mkTarget, profile values.Profile) []Finding {
	var findings []Finding
	add := func(msg, repair string) {
		findings = append(findings, Finding{File: makefileFile, Rule: RuleMakefileVerb, Msg: msg, Repair: repair})
	}

	for _, verb := range requiredVerbs(profile) {
		if _, ok := byName[verb]; !ok {
			add(
				fmt.Sprintf("verb %q missing — the human surface is four verbs, identical in every repo", verb),
				fmt.Sprintf("add a %q target composing existing internal steps", verb),
			)
		}
	}
	if _, ok := byName["deploy"]; ok && profile == values.ProfileLib {
		add(
			"lib profile carries a deploy verb — libs are the contract minus deploy",
			"delete the deploy target (or set profile to tool in repo-standard.json)",
		)
	}

	// Prereq composition: check composes the gate; audit runs check first.
	if check, ok := byName["check"]; ok {
		if missing := missingFrom(check.prereqs, checkFloor); len(missing) > 0 {
			add(
				"check does not compose "+strings.Join(missing, ", ")+" — the fast gate must run vet + lint + test + build",
				"add "+strings.Join(missing, " ")+" to check's prerequisites",
			)
		}
	}
	if audit, ok := byName["audit"]; ok {
		if len(missingFrom(audit.prereqs, []string{"check"})) > 0 {
			add(
				"audit does not run check — an audit that skips the fast gate audits nothing",
				"make check audit's first prerequisite",
			)
		}
		if missing := unreachedGates(byName); len(missing) > 0 {
			add(
				"audit does not reach "+strings.Join(missing, ", ")+" — every Go repo's audit runs race, fuzz, dupe, nilcheck, deadcode, omitempty and vuln",
				"add a documented target for each and list it in audit's prerequisites (sdlc's Makefile is the reference)",
			)
		}
	}
	return findings
}

// unreachedGates returns the audit gates that no prerequisite chain from
// audit reaches, in auditGates order.
func unreachedGates(byName map[string]mkTarget) []string {
	reached := reachable(byName, "audit")
	var missing []string
	for _, g := range auditGates {
		if !reached[g.name] && !reached[g.alt] {
			missing = append(missing, g.name)
		}
	}
	return missing
}

// docFindings enforces ## coverage (makefile-docs): every top-level target
// self-documents; `make help`'s awk lister only shows documented targets.
func docFindings(targets []mkTarget) []Finding {
	var undocumented []string
	for _, t := range targets {
		if !t.doc {
			undocumented = append(undocumented, t.name)
		}
	}
	if len(undocumented) == 0 {
		return nil
	}
	sort.Strings(undocumented)
	return []Finding{{
		File:   makefileFile,
		Rule:   RuleMakefileDocs,
		Msg:    "targets without a ## doc comment: " + strings.Join(undocumented, ", ") + " — they vanish from make help",
		Repair: `append "## <one-line purpose>" to each target line`,
	}}
}

// missingFrom returns the members of want absent from have, in want order.
func missingFrom(have, want []string) []string {
	set := make(map[string]bool, len(have))
	for _, h := range have {
		set[h] = true
	}
	var missing []string
	for _, w := range want {
		if !set[w] {
			missing = append(missing, w)
		}
	}
	return missing
}
