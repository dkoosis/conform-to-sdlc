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
		run, ok := jscpdInvocation(expandMakeVars(cmd.text, vars))
		if !ok {
			continue
		}
		if msg := dupeThresholdProblem(dir, run, vars); msg != "" {
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

// jscpdSegEnd ends a shell command: a separator, a pipe or a command
// substitution's close.
var jscpdSegEnd = regexp.MustCompile(`[;&|)]|\n`)

// jscpdCommand finds jscpd run as a command: tool@version, or the word after
// npx and its flags, or at the start of a command. A path or an argument that
// merely says jscpd (mktemp -t jscpd) is not a run.
func jscpdCommand(masked string) []int {
	for _, loc := range jscpdRun.FindAllStringIndex(masked, -1) {
		if loc[1] < len(masked) && masked[loc[1]] == '@' {
			return loc
		}
		// The word holding jscpd: make's prefix chars and a path (./node_modules/.bin/)
		// may precede the name.
		wordStart := strings.LastIndexAny(masked[:loc[0]], " \t;&|(") + 1
		word := strings.TrimLeft(masked[wordStart:loc[0]], "@-+")
		if word != "" && !strings.HasSuffix(word, "/") {
			continue
		}
		before := strings.TrimRight(masked[:wordStart], " \t")
		prev := before
		if i := strings.LastIndexAny(before, " \t;&|("); i >= 0 {
			prev = before[i+1:]
		}
		switch strings.TrimLeft(prev, "@-+") {
		case "npx", "exec":
			return loc
		}
		switch {
		case before == "", strings.HasSuffix(before, ";"), strings.HasSuffix(before, "&"), strings.HasSuffix(before, "|"), strings.HasSuffix(before, "("),
			prev == "--yes", prev == "-y", prev == "--no-install":
			return loc
		}
	}
	return nil
}

// jscpdInvocation returns the text of the jscpd command in a recipe line,
// from the tool name to the end of that command, so a flag on another
// command in the line (mktemp -t) is not read as jscpd's. A backslash-newline
// continues the command.
func jscpdInvocation(text string) (string, bool) {
	masked := maskQuotes(strings.ReplaceAll(text, "\\\n", "  "))
	masked = makeVarRef.ReplaceAllStringFunc(masked, func(ref string) string { return strings.Repeat("_", len(ref)) })
	loc := jscpdCommand(masked)
	if loc == nil {
		return "", false
	}
	flat := strings.ReplaceAll(text, "\\\n", "  ")
	end := len(masked)
	if e := jscpdSegEnd.FindStringIndex(masked[loc[1]:]); e != nil {
		end = loc[1] + e[0]
	}
	return flat[loc[0]:end], true
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

// expandMakeVars resolves $(NAME) and ${NAME} until the text stops changing
// (a variable may hold another), so a jscpd run through a variable is seen.
func expandMakeVars(s string, vars map[string]makeVar) string {
	for range 5 {
		next := resolveMakeVars(s, vars)
		if next == s {
			break
		}
		s = next
	}
	return s
}

var (
	deadcodeRun    = regexp.MustCompile(`\bdeadcode\b`)
	deadcodeTest   = regexp.MustCompile(`\s--?test(?:=(?:true|1|t|T|TRUE|True))?(?:\s|$)`)
	swallowTail    = regexp.MustCompile(`^\)?\s*\|\|\s*(?:true\b|:(?:\s|;|$)|exit\s+0\b)`)
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
	// Each command is masked on its own, so a quote left open in one (an
	// apostrophe in a comment) cannot pair with a quote in the next.
	var sb, masked strings.Builder
	var swallowed []Finding
	for _, c := range cmds {
		m := maskQuotes(c.text)
		if deadcodeRun.MatchString(m) {
			swallowed = append(swallowed, deadcodeSwallowFindings(c.text, m)...)
			sb.WriteString(c.text + "\n")
			masked.WriteString(m + "\n")
		}
	}
	text, runs := sb.String(), masked.String()
	if text == "" {
		return nil // no deadcode step to read; makefile-verbs owns a missing target
	}
	findings := append(deadcodeRootFindings(runs, hasMainPackage(dir)), swallowed...)
	return append(findings, deadcodeFilterFindings(text, runs, vals)...)
}

var (
	deadcodeCapture = regexp.MustCompile("(\\w+)=(?:\\$\\$\\(|`)[^\n]*$")
	negatedPipe     = regexp.MustCompile(`(?:^|[\s;(@+])!\s*go\s+run\s+\S*$`)
	pipeToGrep      = regexp.MustCompile(`\|\s*grep\b`)
)

// deadcodeSwallowFindings says whether a deadcode recipe command can never
// fail. deadcode exits 0 when it finds dead code, so the gate is a test of its
// output: captured and tested non-empty (-n/-z), or piped to `! grep`. A
// failure swallowed by `|| true`, `|| :`, `|| exit 0` or make's - prefix, or an
// output nobody tests, passes at any dead code. text is the command, masked
// the same with its quoted strings blanked.
func deadcodeSwallowFindings(text, masked string) []Finding {
	trim := strings.TrimLeft(text, " \t")
	if strings.HasPrefix(strings.TrimLeft(trim, "@+-"), "#") {
		return nil // a comment that mentions deadcode
	}
	prefix := trim[:len(trim)-len(strings.TrimLeft(trim, "@+-"))]
	at := deadcodeRun.FindStringIndex(masked)[0]
	var why string
	switch {
	case strings.Contains(prefix, "-"):
		why = "a leading - ignores the command's exit status"
	case swallowTail.MatchString(masked[at+pipelineEnd(masked[at:]):]):
		why = "`|| true`, `|| :` or `|| exit 0` swallows its failure"
	case !deadcodeOutputTested(text, masked, at):
		why = "its output is neither captured and tested (-n or -z) nor a negated pipeline (`! deadcode ... | grep .`); deadcode exits 0 when it finds dead code"
	default:
		return nil
	}
	return []Finding{{
		File:   makefileFile,
		Rule:   RuleDeadcodeGate,
		Msg:    "the deadcode target can never fail: " + why,
		Repair: "capture and test the output: out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./...) || { echo \"deadcode: could not run\"; exit 1; }; if [ -n \"$$out\" ]; then echo \"$$out\"; exit 1; fi",
	}}
}

// deadcodeOutputTested says whether the command tests what deadcode prints.
func deadcodeOutputTested(text, masked string, at int) bool {
	if negatedPipe.MatchString(masked[:at]) && pipeToGrep.MatchString(masked[at:]) {
		return true
	}
	if m := deadcodeCapture.FindStringSubmatch(masked[:at]); m != nil {
		v := `"?\$\$\{?` + regexp.QuoteMeta(m[1]) + `\b\}?"?`
		return regexp.MustCompile(`-[nz]\s+` + v + `|` + v + `\s*\|\s*grep\s+-\w*q`).MatchString(text[at:])
	}
	// -z "$$(... deadcode ...)": the substitution is the tested word.
	return regexp.MustCompile(`-[nz]\s+"\$\$\([^"]*deadcode`).MatchString(text)
}

// deadcodeRootFindings reads runs, the recipe with its quoted strings masked.
func deadcodeRootFindings(runs string, hasMain bool) []Finding {
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

// allowList is the functions the repo's deadcode-filter exception allows.
func allowList(vals values.Values) map[string]string {
	for _, e := range vals.Exceptions {
		if e.Rule == values.FilterRule {
			return e.Allow
		}
	}
	return nil
}

func deadcodeFilterFindings(text, masked string, vals values.Values) []Finding {
	allowed := allowList(vals)
	patterns, other := deadcodeFilters(text, masked)
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
	add := `add {"rule": "deadcode-filter", "allow": {"<function>": "<reason>"}} to docs/repo-standard.json, one entry per function the filter hides, and write each as 'unreachable func: <name>$' in a grep -v pattern; or delete the filter and the dead function`
	var findings []Finding
	for _, tool := range other {
		findings = append(findings, finding(tool+" filters deadcode's output, and the checker can verify only `grep -v <function names>` against an allow list", "use `grep -v` with literal function names, "+add))
	}
	if len(patterns) > 0 && len(allowed) == 0 {
		findings = append(findings, finding("the deadcode target filters deadcode's output, which hides unreachable functions from the gate, and docs/repo-standard.json has no deadcode-filter exception for it", add))
	}
	for _, p := range patterns {
		if len(allowed) > 0 && !namesOnly(p, names) {
			findings = append(findings, finding(fmt.Sprintf("the deadcode filter pattern %q is not just names from the deadcode-filter allow list, each written as `unreachable func: <name>$`; a bare name or a bare -F string also hides every function that contains it", strings.TrimPrefix(p, fixedStringMark)), "write each alternative as `unreachable func: <name>$` with regex metacharacters escaped ('unreachable func: hookA$' or 'unreachable func: (hookA|T\\.Close)$'), and list each name in the exception's allow, with a reason"))
		}
	}
	for _, n := range names {
		if !slices.ContainsFunc(patterns, func(p string) bool { return strings.Contains(p, regexp.QuoteMeta(n)) }) {
			findings = append(findings, finding(fmt.Sprintf("deadcode-filter allows %q, but the deadcode target does not filter it out", n), "delete the stale entry from the exception's allow list"))
		}
	}
	return findings
}

// namesOnly says whether pattern p is made of allowed names and alternation
// punctuation alone: an unlisted name or a wildcard would hide more.
func namesOnly(p string, names []string) bool {
	// deadcode prints "file.go:1:6: unreachable func: <name>". Every
	// alternative of the pattern (| for grep -E, \| for a basic one) must be
	// exactly `unreachable func: <name>$` with <name> an allowed name, regex
	// metacharacters escaped; the shared-prefix form
	// `unreachable func: (A|B)$` is the same. A bare name matches any line
	// that contains it, and an empty alternative matches every line.
	if strings.HasPrefix(p, fixedStringMark) {
		return false
	}
	p = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(p, "$$", "$"), `\|`, "|")) // make's $$ is the shell's $
	quoted := make(map[string]bool, len(names))
	for _, n := range names {
		quoted[regexp.QuoteMeta(n)] = true
	}
	exact := true
	for alt := range strings.SplitSeq(p, "|") {
		name, ok := strings.CutPrefix(strings.TrimPrefix(strings.TrimSpace(alt), "^"), unreachablePrefix)
		if name, cut := strings.CutSuffix(name, "$"); !ok || !cut || !quoted[name] {
			exact = false
			break
		}
	}
	if exact {
		return true
	}
	group, ok := strings.CutPrefix(strings.TrimPrefix(p, "^"), unreachablePrefix)
	if !ok {
		return false
	}
	for _, pair := range [][2]string{{`\(`, `\)$`}, {"(", ")$"}} {
		if inner, ok := strings.CutPrefix(group, pair[0]); ok {
			if inner, ok = strings.CutSuffix(inner, pair[1]); ok {
				return allQuoted(inner, quoted)
			}
		}
	}
	return false
}

// allQuoted says whether every | separated name in alts is a key of quoted.
func allQuoted(alts string, quoted map[string]bool) bool {
	for n := range strings.SplitSeq(alts, "|") {
		if !quoted[n] {
			return false
		}
	}
	return true
}

const (
	unreachablePrefix = "unreachable func: "
	// fixedStringMark tags a pattern read from grep -F or fgrep, which cannot
	// anchor with $.
	fixedStringMark = "\x00fixed:"
)

var fixedGrepFlag = regexp.MustCompile(`(?:^|\s)(?:-[a-zA-Z]*F[a-zA-Z]*|--fixed-strings)(?:\s|$)`)

// isRedirectAmp says whether the & at blank[i] is part of 2>&1 or &>.
func isRedirectAmp(blank string, i int) bool {
	return (i > 0 && blank[i-1] == '>') || (i+1 < len(blank) && blank[i+1] == '>')
}

// pipelineEnd returns the offset in blank where the command that starts at
// its beginning stops feeding the pipeline: a ;, && , ||, & , a newline, or the
// ) that closes an enclosing command substitution.
func pipelineEnd(blank string) int {
	depth := 0
	for i := range len(blank) {
		switch blank[i] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return i
			}
			depth--
		case '&':
			// 2>&1 and &> redirect; only & and && end the pipeline.
			if !isRedirectAmp(blank, i) {
				return i
			}
		case ';', '\n':
			return i
		case '|':
			if i+1 < len(blank) && blank[i+1] == '|' {
				return i
			}
		}
	}
	return len(blank)
}

// deadcodeFilters reads the pipeline stages that follow the deadcode run in
// the recipe text. A grep stage yields its pattern arguments; any other
// filtering tool is returned by name. A quiet grep (-q) only tests, and is
// not a filter.
//
// Only the stages the deadcode command feeds directly count: the scan stops at
// a ;, &&, ||, &, a newline, or the ) that closes the command substitution
// holding deadcode, so a later display pipe (echo "$out" | head) is not a
// filter.
func deadcodeFilters(text, blank string) (patterns, other []string) {
	covered := 0
	for _, run := range deadcodeRun.FindAllStringIndex(blank, -1) {
		if run[0] < covered {
			continue
		}
		covered = run[0] + pipelineEnd(blank[run[0]:])
		p, o := stageFilters(text, blank, run[0], covered)
		patterns = append(patterns, p...)
		other = append(other, o...)
	}
	return patterns, other
}

// stageFilters reads the filter stages in blank[start:stop].
func stageFilters(text, blank string, start, stop int) (patterns, other []string) {
	scope := blank[:stop]
	for _, m := range outputFilter.FindAllStringSubmatchIndex(scope[start:], -1) {
		tool := blank[start+m[2] : start+m[3]]
		from := start + m[3]
		end := len(blank)
		if loc := filterSegEnd.FindStringIndex(blank[from:]); loc != nil {
			end = from + loc[0]
		}
		switch tool {
		case "grep", "egrep", "fgrep":
			patterns = append(patterns, grepPatterns(tool, text[from:end], blank[from:end])...)
		default:
			other = append(other, tool)
		}
	}
	return patterns, other
}

// grepPatterns returns the patterns of one grep stage (seg, with its masked
// twin): none for a quiet grep, which only tests; each tagged as fixed for
// grep -F and fgrep.
func grepPatterns(tool, seg, maskedSeg string) []string {
	if quietGrep.MatchString(maskedSeg) {
		return nil
	}
	args := filterArgs(seg)
	if tool == "fgrep" || fixedGrepFlag.MatchString(maskedSeg) {
		for i := range args {
			args[i] = fixedStringMark + args[i]
		}
	}
	return args
}

// filterArgs returns the non-flag arguments of a grep stage: quoted strings
// and bare words.
func filterArgs(seg string) []string {
	var args []string
	rest := quoted.ReplaceAllStringFunc(seg, func(q string) string {
		args = append(args, q[1:len(q)-1])
		return " "
	})
	for f := range strings.FieldsSeq(rest) {
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
			if path != dir && skipDSLDir(path, name) {
				return filepath.SkipDir
			}
			return nil
		}
		found = isMainFile(path, name)
		return nil
	})
	return found
}

// isMainFile says whether the non-test Go file at path is package main and
// built by default.
func isMainFile(path, name string) bool {
	if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return false
	}
	src, err := os.ReadFile(path)
	return err == nil && packageMain.Match(src) && !buildIgnore.Match(src)
}

// maskQuotes blanks each quoted string to spaces of the same byte length, so
// an offset into the result is an offset into s.
func maskQuotes(s string) string {
	return quoted.ReplaceAllStringFunc(s, func(q string) string {
		b := []byte(q)
		for i, c := range b {
			if c != '\n' {
				b[i] = ' '
			}
		}
		return string(b)
	})
}
