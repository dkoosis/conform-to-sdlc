package checks

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/dkoosis/repo-standard/internal/values"
)

// The dupe and deadcode gates (dupe-gate, deadcode-gate, deadcode-filter).
// Each tool exits 0 in its default form however much it finds, so a green
// `make audit` proved nothing:
//
//	jscpd     exits 0 at any duplication unless given a threshold;
//	deadcode  prints unreachable functions and exits 0, and from a library's
//	          packages (no main) it has no root to trace from.
//
// These rules read the recipes of the `dupe` and `deadcode` targets.

// dupeMaxThreshold is the highest jscpd threshold (percent) a repo may run
// without an exception: the fleet's value.
const dupeMaxThreshold = 3.0

const jscpdConfigFile = ".jscpd.json"

var (
	jscpdThresholdFlag = regexp.MustCompile(`(?:^|\s)(?:--threshold|-t)(?:=|\s+)(\S+)`)
	jscpdConfigFlag    = regexp.MustCompile(`(?:^|\s)(?:--config|-c)(?:=|\s+)(\S+)`)
	jscpdRun           = regexp.MustCompile(`\bjscpd\b`)
)

// gateRecipes returns the recipe commands of the named Makefile target and
// the Makefile's variables.
func gateRecipes(dir, target string) ([]recipeCmd, map[string]makeVar) {
	data, err := os.ReadFile(filepath.Join(dir, makefileFile))
	if err != nil {
		return nil, nil
	}
	recipes, vars := parseRecipes(string(data))
	addIncludedVars(dir, string(data), vars)
	return recipes[target], vars
}

// checkDupeGate (dupe-gate) fails a repo whose dupe target runs jscpd with no
// threshold of 3% or less, from a flag on the command or from the .jscpd.json
// it picks up. Without one, jscpd exits 0 at any duplication.
func checkDupeGate(dir string) []Finding {
	cmds, vars := gateRecipes(dir, "dupe")
	for _, cmd := range cmds {
		if !jscpdRun.MatchString(blankQuotes(cmd.text)) {
			continue
		}
		if msg := dupeThresholdProblem(dir, cmd.text, vars); msg != "" {
			return []Finding{{
				File:   makefileFile,
				Rule:   RuleDupeGate,
				Msg:    fmt.Sprintf("target %q, line %d: %s; jscpd exits 0 at any duplication without a threshold, so the gate cannot fail", "dupe", cmd.line, msg),
				Repair: `add "--threshold 3" to the jscpd command, or set "threshold": 3 in a tracked ` + jscpdConfigFile + ` that the command picks up; a repo keeps a higher one only by an exception with id and reason: {"rule": "dupe-gate", "reason": "<why, and the number>"}`,
			}}
		}
	}
	return nil
}

// dupeThresholdProblem says why the jscpd command text sets no threshold at
// or below the limit, or "" when it does. A --threshold flag wins over the
// config file, as it does in jscpd.
func dupeThresholdProblem(dir, text string, vars map[string]makeVar) string {
	if m := jscpdThresholdFlag.FindStringSubmatch(text); m != nil {
		raw := strings.Trim(resolveMakeVars(m[1], vars), `"'`)
		n, err := strconv.ParseFloat(raw, 64)
		switch {
		case err != nil:
			return fmt.Sprintf("--threshold %q is not a number the checker can read", m[1])
		case n > dupeMaxThreshold:
			return fmt.Sprintf("--threshold %s is above %g%%", raw, dupeMaxThreshold)
		case n < 0:
			return fmt.Sprintf("--threshold %s is negative", raw)
		}
		return ""
	}
	file := jscpdConfigFile
	if m := jscpdConfigFlag.FindStringSubmatch(text); m != nil {
		file = strings.Trim(resolveMakeVars(m[1], vars), `"'`)
	}
	if !filepath.IsLocal(file) {
		return fmt.Sprintf("its config %q is outside the repo", file)
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
	if err != nil {
		return "the command has no --threshold and the repo has no " + file
	}
	var cfg struct {
		Threshold *float64 `json:"threshold"`
	}
	switch err := json.Unmarshal(data, &cfg); {
	case err != nil:
		return file + " is not valid JSON, and the command has no --threshold"
	case cfg.Threshold == nil:
		return `the command has no --threshold and ` + file + ` sets no "threshold"`
	case *cfg.Threshold > dupeMaxThreshold || *cfg.Threshold < 0:
		return fmt.Sprintf(`%s sets "threshold": %g, outside 0 to %g`, file, *cfg.Threshold, dupeMaxThreshold)
	}
	return ""
}

// resolveMakeVars replaces $(NAME) and ${NAME} with the Makefile's own value
// where it has one.
func resolveMakeVars(s string, vars map[string]makeVar) string {
	return makeVarRef.ReplaceAllStringFunc(s, func(ref string) string {
		if v, ok := vars[strings.Trim(ref, "$(){}")]; ok {
			return v.value
		}
		return ref
	})
}

var (
	deadcodeRun    = regexp.MustCompile(`\bdeadcode\b`)
	deadcodeTest   = regexp.MustCompile(`\s-test(?:\s|$)`)
	deadcodeSkip   = regexp.MustCompile(`\bexit\s+0\b`)
	outputFilter   = regexp.MustCompile("(?:^|[\\s|;&(`])(grep|egrep|fgrep|rg|sed|awk|cut|head|tail|tr|comm|uniq|sort|diff)(?:\\s|$)")
	quietGrep      = regexp.MustCompile(`^\s+-\w*q`)
	filterSegEnd   = regexp.MustCompile(`[|;&)]|\n`)
	sourceSkipDirs = []string{"vendor", "testdata", "node_modules"}
	buildIgnore    = regexp.MustCompile(`(?m)^//go:build\s+ignore\b`)
	packageMain    = regexp.MustCompile(`(?m)^package\s+main\b`)
)

// checkDeadcodeGate runs deadcode-gate and deadcode-filter over the deadcode
// target.
//
// deadcode-gate: a repo with a main traces from main (the strict form: no
// -test, so a function only a test calls is still dead). A repo with no main
// has no root, so deadcode with no -test lists nothing and the gate passes
// by skipping; there the tests are the roots (`deadcode -test ./...`).
//
// deadcode-filter: a target that filters deadcode's output hides dead code
// from the gate. It is allowed only for functions an exception names, with a
// reason each: {"rule": "deadcode-filter", "allow": {"<function>": "<why>"}}.
func checkDeadcodeGate(dir string, vals values.Values) []Finding {
	cmds, _ := gateRecipes(dir, "deadcode")
	var text string
	for _, c := range cmds {
		if deadcodeRun.MatchString(blankQuotes(c.text)) {
			text += c.text + "\n"
		}
	}
	if text == "" {
		return nil // no deadcode step to read; makefile-verbs owns a missing target
	}
	findings := deadcodeRootFindings(text, hasMainPackage(dir))
	return append(findings, deadcodeFilterFindings(text, vals)...)
}

func deadcodeRootFindings(text string, hasMain bool) []Finding {
	runs := blankQuotes(text)
	i := deadcodeRun.FindStringIndex(runs)
	hasTest := deadcodeTest.MatchString(runs[i[0]:])
	switch {
	case !hasMain && (!hasTest || deadcodeSkip.MatchString(runs)):
		return []Finding{{
			File:   makefileFile,
			Rule:   RuleDeadcodeGate,
			Msg:    "this repo has no main package, and the deadcode target traces from main or skips: with no root it reports nothing and exits 0, so dead code in a library never fails the gate",
			Repair: "run `deadcode -test ./...` (the tests are the roots) with no skip branch: out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) -test ./...) || { echo \"deadcode: could not run\"; exit 1; }; if [ -n \"$$out\" ]; then echo \"$$out\"; exit 1; fi",
		}}
	case hasMain && hasTest:
		return []Finding{{
			File:   makefileFile,
			Rule:   RuleDeadcodeGate,
			Msg:    "this repo has a main package, and the deadcode target runs with -test: tests count as roots, so a function only a test calls passes as live",
			Repair: "drop -test; with a main the gate is strict: deadcode ./... must print nothing",
		}}
	}
	return nil
}

func deadcodeFilterFindings(text string, vals values.Values) []Finding {
	allowed := map[string]string{}
	for _, e := range vals.Exceptions {
		if e.Rule == values.FilterRule {
			allowed = e.Allow
		}
	}
	patterns, other := deadcodeFilters(text)
	if len(patterns) == 0 && len(other) == 0 && len(allowed) == 0 {
		return nil
	}
	names := make([]string, 0, len(allowed))
	for n := range allowed {
		names = append(names, n)
	}
	sort.Strings(names)

	finding := func(msg, repair string) Finding {
		return Finding{File: makefileFile, Rule: RuleDeadcodeFilter, Msg: msg, Repair: repair}
	}
	add := `add {"rule": "deadcode-filter", "allow": {"<function>": "<reason>"}} to docs/repo-standard.json, one entry per function the filter hides, and write each name literally in a grep -v pattern; or delete the filter and the dead function`
	var findings []Finding
	for _, tool := range other {
		findings = append(findings, finding(tool+" filters deadcode's output, and the checker can verify only `grep -v <function names>` against an allow list", "use `grep -v` with literal function names, "+add))
	}
	if len(patterns) > 0 && len(allowed) == 0 {
		findings = append(findings, finding("the deadcode target filters deadcode's output, which hides unreachable functions from the gate, and docs/repo-standard.json has no deadcode-filter exception for it", add))
	}
	for _, p := range patterns {
		if len(allowed) > 0 && !slices.ContainsFunc(names, func(n string) bool { return strings.Contains(p, n) }) {
			findings = append(findings, finding(fmt.Sprintf("the deadcode filter pattern %q names no function in the deadcode-filter allow list", p), "write each hidden function's name literally in the pattern and list it in the exception's allow, with a reason"))
		}
	}
	for _, n := range names {
		if !slices.ContainsFunc(patterns, func(p string) bool { return strings.Contains(p, n) }) {
			findings = append(findings, finding(fmt.Sprintf("deadcode-filter allows %q, but the deadcode target does not filter it out", n), "delete the stale entry from the exception's allow list"))
		}
	}
	return findings
}

// deadcodeFilters reads the pipeline stages that follow the deadcode run in
// the recipe text. A grep stage yields its pattern arguments; any other
// filtering tool is returned by name. A quiet grep (-q) only tests, and is
// not a filter.
func deadcodeFilters(text string) (patterns, other []string) {
	blank := maskQuotes(text)
	start := deadcodeRun.FindStringIndex(blank)[0]
	for _, m := range outputFilter.FindAllStringSubmatchIndex(blank[start:], -1) {
		tool := blank[start+m[2] : start+m[3]]
		from := start + m[3]
		end := len(blank)
		if loc := filterSegEnd.FindStringIndex(blank[from:]); loc != nil {
			end = from + loc[0]
		}
		switch tool {
		case "grep", "egrep", "fgrep":
			if quietGrep.MatchString(blank[from:end]) {
				continue
			}
			patterns = append(patterns, filterArgs(text[from:end])...)
		default:
			other = append(other, tool)
		}
	}
	return patterns, other
}

// filterArgs returns the non-flag arguments of a grep stage: quoted strings
// and bare words.
func filterArgs(seg string) []string {
	var args []string
	rest := quoted.ReplaceAllStringFunc(seg, func(q string) string {
		args = append(args, q[1:len(q)-1])
		return " "
	})
	for _, f := range strings.Fields(rest) {
		if !strings.HasPrefix(f, "-") {
			args = append(args, f)
		}
	}
	if len(args) == 0 {
		args = []string{""}
	}
	return args
}

// hasMainPackage says whether any non-test Go file in dir is package main,
// skipping hidden directories, vendor, testdata and files the toolchain
// ignores.
func hasMainPackage(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error { //nolint:errcheck // the callback never errs
		if err != nil || found {
			return filepath.SkipDir
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(name, ".") || slices.Contains(sourceSkipDirs, name)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // G304: path comes from walking the repo
		if err == nil && packageMain.Match(src) && !buildIgnore.Match(src) {
			found = true
		}
		return nil
	})
	return found
}

// maskQuotes blanks each quoted string to spaces of the same length, so an
// offset into the result is an offset into s.
func maskQuotes(s string) string {
	return quoted.ReplaceAllStringFunc(s, func(q string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, q)
	})
}
