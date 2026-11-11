package checks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Gate steps (cfm-pxx.3, epic cfm-pxx R3): a green check or audit must mean
// each tool ran, at one version. Two rules read the recipes reachable from
// check and audit:
//
//	gate-skip — a step that exits 0 when its tool is missing.
//	tool-pin  — a tool that runs on PATH, at @latest, or at an unset version.
//
// golangci-lint is out of both (lint-pin owns it). A tool not named in
// gateTools is repo-specific and out of the rules.

// gateTools are the tools the rules know by name.
var gateTools = []string{"govulncheck", "nilaway", "jscpd", "deadcode", "go-arch-lint", "staticcheck"}

var (
	toolAlt = strings.Join(gateTools, "|")

	// versioned matches tool@version where the tool ends a path (go run
	// golang.org/x/vuln/cmd/govulncheck@v1) or stands alone (npx jscpd@5).
	versioned = regexp.MustCompile(`(?:^|[\s/])(` + toolAlt + `)@(\$\(\w+\)|\$\{\w+\}|[^\s)"';|&\\]+)`)

	quoted      = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'[^']*'`)
	makeVarRef  = regexp.MustCompile(`\$[({]\w+[)}]`)
	shellSep    = strings.NewReplacer(";", " ", "|", " ", "&", " ", "(", " ", ")", " ", "{", " ", "}", " ", "!", " ", "<", " ", ">", " ", "\\", " ", "\n", " ", "\t", " ")
	makeAssign  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(?:::=|:=|\?=|=)\s*(.*)$`)
	pinnedVer   = regexp.MustCompile(`^(?:v?\d|[0-9a-f]{7,40}$)`)
	confLine    = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	missingExit = regexp.MustCompile(`\bexit\s+(?:[1-9]|\$)|\bfalse\b`)

	// lookup is a shell test for a tool's presence.
	lookup = `(?:command\s+-v|which|type|hash)\s+`
)

// recipeCmd is one logical recipe command: backslash continuations joined,
// newlines kept so a match maps back to a physical line.
type recipeCmd struct {
	line int // 1-based line of the command's first physical line
	text string
}

// lineAt returns the physical line of byte offset i in the command.
func (c recipeCmd) lineAt(i int) int {
	return c.line + strings.Count(c.text[:i], "\n")
}

// parseRecipes maps each top-level target to its recipe commands. It also
// returns the Makefile's variable assignments (name → value, line).
func parseRecipes(text string) (map[string][]recipeCmd, map[string]makeVar) {
	recipes := map[string][]recipeCmd{}
	vars := map[string]makeVar{}
	cur := ""
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "\t"):
			var cmd recipeCmd
			cmd, i = joinContinuations(lines, i)
			if cur != "" {
				recipes[cur] = append(recipes[cur], cmd)
			}
		case strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#"):
		case makeAssign.MatchString(l):
			recordVar(vars, l, i+1)
			cur = ""
		default:
			cur = ""
			if m := targetLine.FindStringSubmatch(l); m != nil {
				cur = m[1]
			}
		}
	}
	return recipes, vars
}

// joinContinuations reads the recipe command that starts at lines[i] and
// returns it with the index of its last physical line.
func joinContinuations(lines []string, i int) (recipeCmd, int) {
	cmd := recipeCmd{line: i + 1, text: strings.TrimPrefix(lines[i], "\t")}
	for strings.HasSuffix(cmd.text, `\`) && i+1 < len(lines) {
		i++
		cmd.text += "\n" + strings.TrimLeft(lines[i], " \t")
	}
	return cmd, i
}

func recordVar(vars map[string]makeVar, l string, line int) {
	m := makeAssign.FindStringSubmatch(l)
	val := m[2]
	if j := strings.Index(val, " #"); j >= 0 {
		val = val[:j]
	}
	if _, dup := vars[m[1]]; !dup {
		vars[m[1]] = makeVar{value: strings.TrimSpace(val), line: line}
	}
}

type makeVar struct {
	value string
	line  int
}

// reachable returns every target a prerequisite chain from roots reaches.
func reachable(byName map[string]mkTarget, roots ...string) map[string]bool {
	reached := map[string]bool{}
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if reached[name] {
			continue
		}
		reached[name] = true
		queue = append(queue, byName[name].prereqs...)
	}
	return reached
}

var includeLine = regexp.MustCompile(`(?m)^-?include\s+(\S+)\s*$`)

// addIncludedVars adds the variables of each literal-path include (the shared
// .sandbox/lib files set tool versions) that the Makefile itself leaves unset.
func addIncludedVars(dir, text string, vars map[string]makeVar) {
	for _, m := range includeLine.FindAllStringSubmatch(text, -1) {
		if !filepath.IsLocal(m[1]) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(m[1]))) //nolint:gosec // G703: IsLocal above keeps it under the repo
		if err != nil {
			continue
		}
		_, inc := parseRecipes(string(data))
		for k, v := range inc {
			if _, ok := vars[k]; !ok {
				vars[k] = v
			}
		}
	}
}

// projectConf reads KEY=VALUE pairs from .sandbox/project.conf.
func projectConf(dir string) map[string]string {
	conf := map[string]string{}
	data, err := os.ReadFile(filepath.Join(dir, pinFile))
	if err != nil {
		return conf
	}
	for l := range strings.SplitSeq(string(data), "\n") {
		if m := confLine.FindStringSubmatch(l); m != nil {
			conf[m[1]] = strings.Trim(strings.TrimSpace(m[2]), `"'`)
		}
	}
	return conf
}

// checkGateSteps runs gate-skip and tool-pin over the recipes reachable from
// check and audit. No Makefile is makefile-verbs' finding, not this rule's.
func checkGateSteps(dir string) []Finding {
	data, err := os.ReadFile(filepath.Join(dir, makefileFile))
	if err != nil {
		return nil
	}
	text := string(data)
	byName := map[string]mkTarget{}
	for _, t := range parseMakefile(text) {
		if _, dup := byName[t.name]; !dup {
			byName[t.name] = t
		}
	}
	recipes, vars := parseRecipes(text)
	addIncludedVars(dir, text, vars)
	conf := projectConf(dir)
	reached := reachable(byName, "check", "audit")

	var names []string
	for name := range recipes {
		if reached[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var findings []Finding
	for _, name := range names {
		var perCmd []Finding
		for _, cmd := range recipes[name] {
			perCmd = append(perCmd, skipFindings(name, cmd)...)
			perCmd = append(perCmd, pinFindings(name, cmd, vars, conf)...)
		}
		findings = append(findings, dedupe(perCmd)...)
	}
	return findings
}

// dedupe drops repeats of one rule and message, keeping order.
func dedupe(in []Finding) []Finding {
	seen := map[string]bool{}
	var out []Finding
	for _, f := range in {
		if key := f.Rule + f.Msg; !seen[key] {
			seen[key] = true
			out = append(out, f)
		}
	}
	return out
}

// skipFindings flags a command whose missing-tool branch does not exit
// non-zero. It reads each `if … fi` block on its own (the then or else the
// presence test selects), then the statements outside any block.
func skipFindings(target string, cmd recipeCmd) []Finding {
	text := blankQuotes(cmd.text)
	blocks := ifBlocks(text)
	rest := blankBlocks(text, blocks)

	var findings []Finding
	for _, tool := range gateTools {
		look := regexp.MustCompile(lookup + regexp.QuoteMeta(tool) + `\b`)
		for _, at := range append(blockSkips(text, blocks, look), statementSkips(rest, look)...) {
			findings = append(findings, Finding{
				File: makefileFile,
				Rule: RuleGateSkip,
				Msg: fmt.Sprintf("target %q, line %d: %s missing passes the step — a green gate must mean %s ran",
					target, cmd.lineAt(at), tool, tool),
				Repair: fmt.Sprintf("delete the presence test and run the pinned form (go run <pkg>@$(%s_VER) or npx), or make the missing branch print to stderr and `exit 1`", verKey(tool)),
			})
		}
	}
	return findings
}

// blankBlocks blanks each if block's keywords and condition, so the bodies
// read as plain statements: a presence test nested in another if still counts.
// blockSkips judges the conditions themselves.
func blankBlocks(text string, blocks []ifBlock) string {
	rest := []byte(text)
	blank := func(from, to int) {
		for i := from; i < to && i < len(rest); i++ {
			if rest[i] != '\n' {
				rest[i] = ' '
			}
		}
	}
	for _, b := range blocks {
		blank(b.start, b.thenAt+len("then"))
		if b.elseAt >= 0 {
			blank(b.elseAt, b.elseAt+len("else"))
			rest[b.elseAt] = ';'
		}
		blank(b.end-len("fi"), b.end)
		rest[b.end-len("fi")] = ';'
	}
	return string(rest)
}

// blockSkips returns the offset of each if block whose presence test for the
// tool selects a branch that does not exit non-zero.
func blockSkips(text string, blocks []ifBlock, look *regexp.Regexp) []int {
	var at []int
	for _, b := range blocks {
		cond := text[b.start:b.thenAt]
		loc := look.FindStringIndex(cond)
		if loc == nil {
			continue
		}
		missing := b.elseBody(text)
		if negatedTest.MatchString(cond[:loc[0]]) {
			missing = b.thenBody(text)
		}
		if !missingExit.MatchString(missing) {
			at = append(at, b.start)
		}
	}
	return at
}

// statementSkips returns the offset of each statement outside an if block
// whose presence test lets a missing tool pass: `T || <not exit>`, or `T &&
// run` with no `||` after it.
func statementSkips(rest string, look *regexp.Regexp) []int {
	var at []int
	sts := statements(rest)
	for n, st := range sts {
		loc := look.FindStringIndex(st.text)
		if loc == nil {
			continue
		}
		after := st.text[loc[1]:]
		i := strings.Index(after, "||")
		// `T && run` as a recipe line's last statement fails when T is missing
		// (the list's status is 1); it passes only when another statement follows.
		silent := i < 0 && strings.Contains(after, "&&") && laterStatement(sts[n+1:])
		if i >= 0 {
			silent = !missingExit.MatchString(after[i:])
		}
		if silent {
			at = append(at, st.at+loc[0])
		}
	}
	return at
}

var (
	negatedTest = regexp.MustCompile(`!\s*$`)
	ifKeyword   = regexp.MustCompile(`\b(if|then|else|elif|fi)\b`)
)

// blankQuotes replaces each quoted string with blanks, keeping newlines so
// line numbers survive.
func blankQuotes(s string) string {
	return quoted.ReplaceAllStringFunc(s, func(q string) string {
		return " " + strings.Repeat("\n", strings.Count(q, "\n")) + " "
	})
}

// ifBlock is one `if … fi`: byte offsets of the if, its then and else
// keywords (elseAt is -1 with no else), and the end of its fi.
type ifBlock struct{ start, thenAt, elseAt, end int }

func (b ifBlock) thenBody(text string) string {
	if b.elseAt >= 0 {
		return text[b.thenAt:b.elseAt]
	}
	return text[b.thenAt : b.end-2]
}

func (b ifBlock) elseBody(text string) string {
	if b.elseAt < 0 {
		return ""
	}
	return text[b.elseAt : b.end-2]
}

// ifBlocks finds every if … fi block, nested ones included.
func ifBlocks(text string) []ifBlock {
	var stack, out []ifBlock
	for _, m := range ifKeyword.FindAllStringSubmatchIndex(text, -1) {
		kw, at := text[m[2]:m[3]], m[2]
		if !keywordAt(text, m[2], m[3]) {
			continue
		}
		if kw == "if" {
			stack = append(stack, ifBlock{start: at, thenAt: -1, elseAt: -1})
			continue
		}
		n := len(stack)
		if n == 0 {
			continue
		}
		top := &stack[n-1]
		switch {
		case kw == "then" && top.thenAt < 0:
			top.thenAt = at
		case (kw == "else" || kw == "elif") && top.elseAt < 0:
			top.elseAt = at
		case kw == "fi":
			top.end = at + 2
			if top.thenAt >= 0 {
				out = append(out, *top)
			}
			stack = stack[:n-1]
		}
	}
	return out
}

// keywordAt says whether text[from:to] stands as a shell word: not inside a
// name, path or flag (go-if, a/fi, --then) and followed by a break.
func keywordAt(text string, from, to int) bool {
	if from > 0 && strings.ContainsRune("-./$_=", rune(text[from-1])) {
		return false
	}
	return to == len(text) || strings.ContainsRune(" \t\n;)}\\", rune(text[to]))
}

// laterStatement says whether any of sts holds a command.
func laterStatement(sts []statement) bool {
	for _, st := range sts {
		if strings.TrimSpace(strings.Trim(st.text, "\\")) != "" {
			return true
		}
	}
	return false
}

type statement struct {
	at   int
	text string
}

// statements splits shell text on ; and newlines outside braces.
func statements(text string) []statement {
	var out []statement
	depth, from := 0, 0
	for i, r := range text {
		switch {
		case r == '{':
			depth++
		case r == '}':
			depth = max(depth-1, 0)
		case (r == ';' || (r == '\n' && !strings.HasSuffix(text[:i], "\\"))) && depth == 0:
			out = append(out, statement{from, text[from:i]})
			from = i + 1
		}
	}
	return append(out, statement{from, text[from:]})
}

// pinFindings flags a known tool that runs at no fixed version.
func pinFindings(target string, cmd recipeCmd, vars map[string]makeVar, conf map[string]string) []Finding {
	var findings []Finding
	add := func(line int, tool, why string) {
		findings = append(findings, Finding{
			File: makefileFile,
			Rule: RuleToolPin,
			Msg:  fmt.Sprintf("target %q, line %d: %s %s", target, line, tool, why),
			Repair: fmt.Sprintf("run %s at a fixed version: go run <pkg>@$(%s_VER) or npx %s@$(%s_VER), with %s_VER set in the Makefile or .sandbox/project.conf",
				tool, verKey(tool), tool, verKey(tool), verKey(tool)),
		})
	}

	text := blankQuotes(cmd.text)
	for _, m := range versioned.FindAllStringSubmatchIndex(text, -1) {
		tool, ver := text[m[2]:m[3]], text[m[4]:m[5]]
		if why := unpinned(ver, vars, conf); why != "" {
			add(cmd.lineAt(m[0]), tool, why)
		}
	}

	// Bare names and npx without a version, after the versioned forms and
	// every quoted string are blanked out.
	rest := versioned.ReplaceAllString(text, " ")
	rest = strings.ReplaceAll(rest, "$$(", "( ")
	rest = makeVarRef.ReplaceAllString(rest, "_")
	toks := strings.Fields(shellSep.Replace(rest))
	for i, tok := range toks {
		tok = strings.TrimLeft(tok, "@-+")
		if !isGateTool(tok) {
			continue
		}
		prev := ""
		if i > 0 {
			prev = toks[i-1]
		}
		if strings.HasPrefix(prev, "-") && !slices.Contains(npxFlags, prev) {
			continue // a tool named as a flag's value, such as --enable staticcheck
		}
		switch prev {
		case "-v", "which", "type", "hash", "wrap", "install", "echo", "printf":
			continue
		case "npx", "--yes", "-y", "--no-install":
			add(cmd.line, tok, "runs through npx with no version")
		default:
			add(cmd.line, tok, "runs from PATH at whatever version is installed")
		}
	}
	return findings
}

var npxFlags = []string{"--yes", "-y", "--no-install", "--quiet", "-q"}

func isGateTool(s string) bool { return slices.Contains(gateTools, s) }

// verKey is the conventional version variable for a tool: NILAWAY_VER.
func verKey(tool string) string {
	return strings.ToUpper(strings.ReplaceAll(tool, "-", ""))
}

// unpinned says why a version reference is not a fixed version, or "".
func unpinned(ver string, vars map[string]makeVar, conf map[string]string) string {
	if !strings.HasPrefix(ver, "$") {
		if pinnedVer.MatchString(ver) {
			return ""
		}
		return fmt.Sprintf("runs at @%s, not a fixed version", ver)
	}
	key := strings.Trim(ver, "$(){}")
	if v, ok := vars[key]; ok {
		if v.value == "" || (!pinnedVer.MatchString(v.value) && !strings.Contains(v.value, "$")) {
			return fmt.Sprintf("runs at %s, which is %q at Makefile line %d, not a fixed version", key, v.value, v.line)
		}
		return ""
	}
	if v, ok := conf[key]; ok {
		if !pinnedVer.MatchString(v) {
			return fmt.Sprintf("runs at %s, which is %q in %s, not a fixed version", key, v, pinFile)
		}
		return ""
	}
	return fmt.Sprintf("runs at %s, which neither the Makefile nor %s sets; add %s=<version> to %s", key, pinFile, key, pinFile)
}
