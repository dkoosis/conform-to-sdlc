package checks

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Fuzz coverage (cfm-pxx.4, epic cfm-pxx R4): every Fuzz function a repo
// holds is one its fuzz target runs. `go test -fuzz` takes one target per
// package per run, so a recipe either names each function or loops over what
// `go test -list '^Fuzz'` prints. The rule parses the _test.go files and reads
// the recipe text; it starts no subprocess.

// fuzzFunc is one `func FuzzX(f *testing.F)` found in a _test.go file.
type fuzzFunc struct {
	name string
	file string // slash path from the repo root
	line int
}

var (
	// fuzzFlag captures the value of a -fuzz flag: -fuzz=^FuzzX$$, -fuzz '^FuzzX$$', -fuzz="^$$f$$".
	fuzzFlag = regexp.MustCompile(`-fuzz(?:=|\s+)(?:'([^']*)'|"([^"]*)"|(\S+))`)
)

// skipFuzzDir says whether the walk leaves a directory alone.
func skipFuzzDir(name string) bool {
	return name == "vendor" || name == "node_modules" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// findFuzzFuncs parses every _test.go file under dir for Fuzz functions.
func findFuzzFuncs(dir string) []fuzzFunc {
	var found []fuzzFunc
	fset := token.NewFileSet()
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error { //nolint:errcheck // the callback returns nil or SkipDir, so WalkDir has no error to report
		switch {
		case err != nil:
			return nil //nolint:nilerr // an unreadable entry is skipped, not a finding
		case d.IsDir() && path != dir && skipFuzzDir(d.Name()):
			return filepath.SkipDir
		case d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go"):
			return nil
		}
		rel, _ := filepath.Rel(dir, path) //nolint:errcheck // path came from walking dir, so it is under dir
		found = append(found, fuzzFuncsIn(fset, path, filepath.ToSlash(rel))...)
		return nil
	})
	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})
	return found
}

// fuzzFuncsIn lists the Fuzz functions of one file; a file that does not
// parse is the compiler's finding, not this rule's.
func fuzzFuncsIn(fset *token.FileSet, path, rel string) []fuzzFunc {
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var out []fuzzFunc
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && isFuzzFunc(fn) {
			out = append(out, fuzzFunc{name: fn.Name.Name, file: rel, line: fset.Position(fn.Pos()).Line})
		}
	}
	return out
}

// isFuzzFunc matches func FuzzXxx(f *testing.F) with no receiver.
func isFuzzFunc(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Fuzz") || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || fn.Type.Results != nil {
		return false
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "F"
}

// checkFuzzCoverage fails a repo with a Fuzz function its fuzz target does
// not run, a missing fuzz target included. A repo with none passes whatever
// its fuzz target does.
func checkFuzzCoverage(dir string) []Finding {
	funcs := findFuzzFuncs(dir)
	if len(funcs) == 0 {
		return nil
	}
	cmds, hasTarget, ok := fuzzRecipe(dir)
	if !ok {
		return nil
	}
	runs := fuzzRuns(cmds)
	runs.byDir = map[string][]string{}
	for _, f := range funcs {
		runs.byDir[path.Dir(f.file)] = append(runs.byDir[path.Dir(f.file)], f.name)
	}

	var why string
	switch {
	case !hasTarget:
		why = "the Makefile has no fuzz target"
	case len(runs.entries) == 0:
		why = "the fuzz target runs no `go test -fuzz`"
	}
	var findings []Finding
	for _, f := range funcs {
		if why == "" && runs.cover(f) {
			continue
		}
		reason := why
		if reason == "" {
			reason = "the fuzz target does not run it, by name or by listing its package"
		}
		findings = append(findings, Finding{
			File: makefileFile,
			Rule: RuleFuzzCoverage,
			Msg:  fmt.Sprintf("%s (%s:%d) is never run: %s — a fuzz test written must be a fuzz test run", f.name, f.file, f.line, reason),
			Repair: "loop over `go test -list '^Fuzz'` in the fuzz target (sdlc's Makefile is the reference), " +
				"or name it: go test -run '^$$' -fuzz '^" + f.name + "$$' -fuzztime=$(FUZZTIME) ./<pkg>",
		})
	}
	return findings
}

// fuzzRecipe returns the commands of the fuzz target and of every target it
// lists as a prerequisite; ok is false when there is no Makefile.
func fuzzRecipe(dir string) (cmds []string, hasTarget, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, makefileFile))
	if err != nil {
		return nil, false, false
	}
	byName := map[string]mkTarget{}
	for _, t := range parseMakefile(string(data)) {
		if _, dup := byName[t.name]; !dup {
			byName[t.name] = t
		}
	}
	if _, has := byName["fuzz"]; !has {
		return nil, false, true
	}
	recipes, _ := parseRecipes(string(data))
	for name := range reachable(byName, "fuzz") {
		for _, c := range recipes[name] {
			cmds = append(cmds, blankComments(c.text))
		}
	}
	return cmds, true, true
}

// blankComments drops each shell comment from a command: a whole-line comment
// (with or without a make @ - + prefix) and the tail after " #".
func blankComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " \t@-+"), "#") {
			lines[i] = ""
			continue
		}
		if head, _, found := strings.Cut(l, " #"); found {
			lines[i] = head
		}
	}
	return strings.Join(lines, "\n")
}

// fuzzEntry is one `go test -fuzz` command: the function names its value
// selects (nil: any, as in the loop form's "^$$f$$") and the packages it runs.
type fuzzEntry struct {
	names *regexp.Regexp
	pkgs  []fuzzPkg
}

// fuzzPkg is a package argument resolved to a directory from the repo root;
// tree says it ended in /..., any says it was a variable (the loop's $$pkg).
type fuzzPkg struct {
	dir  string
	tree bool
	any  bool
}

type fuzzRunSet struct {
	entries []fuzzEntry
	scope   []fuzzPkg           // packages the -list and go list commands cover
	listed  bool                // a go test -list command discovers the names
	exclude []*regexp.Regexp    // grep -v patterns a go list is piped through
	byDir   map[string][]string // Fuzz function names per directory
}

var (
	goTestCmd = regexp.MustCompile(`(?:\bgo|\$\(GO\))\s+test\b`)
	goListCmd = regexp.MustCompile(`\bgo\s+list\b`)
	stmtSplit = regexp.MustCompile(`;|&&|\|\||\n`)
	// continuation joins a backslash-continued command into one line.
	continuation = regexp.MustCompile(`\\\n\s*`)
	// valuedFlags take their value as the next token when written `-flag value`.
	valuedFlags = []string{"-run", "-fuzz", "-fuzztime", "-fuzzminimizetime", "-list", "-timeout", "-count", "-parallel", "-cpu", "-tags", "-bench", "-benchtime", "-coverprofile", "-o"}
)

// fuzzRuns reads every go test -fuzz command, and the -list and go list
// commands that discover which packages a loop runs.
func fuzzRuns(cmds []string) fuzzRunSet {
	var set fuzzRunSet
	for _, c := range cmds {
		cwd := "."
		for _, piece := range stmtSplit.Split(continuation.ReplaceAllString(c, " "), -1) {
			cwd = set.add(piece, cwd)
		}
	}
	return set
}

// add reads one shell statement and returns the directory later statements
// of the same command run in (a cd changes it).
func (s *fuzzRunSet) add(piece, cwd string) string {
	fields := strings.Fields(piece)
	for len(fields) > 0 && slices.Contains([]string{"do", "then", "else"}, strings.TrimLeft(fields[0], "@-+(")) {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return cwd
	}
	switch strings.TrimLeft(fields[0], "@-+(") {
	case "echo", "printf":
		return cwd // text about fuzzing runs nothing
	case "cd":
		if len(fields) > 1 {
			return path.Join(cwd, strings.Trim(fields[1], `"'`))
		}
	}
	isTest := goTestCmd.MatchString(piece)
	if isTest && strings.Contains(piece, "-list") {
		s.listed = true
	}
	switch {
	case isTest && strings.Contains(piece, "-fuzz"):
		s.entries = append(s.entries, fuzzEntry{names: fuzzNames(piece), pkgs: pkgArgs(piece, cwd)})
	case isTest && strings.Contains(piece, "-list"):
		s.scope = append(s.scope, literalPkgs(pkgArgs(piece, cwd))...)
	case goListCmd.MatchString(piece):
		s.scope = append(s.scope, literalPkgs(pkgArgs(piece, cwd))...)
		s.exclude = append(s.exclude, grepExcludes(piece)...)
	}
	return cwd
}

var grepV = regexp.MustCompile(`\|\s*grep\s+(?:-\w*v\w*|-v)\s+(?:'([^']*)'|"([^"]*)"|([^\s)|]+))`)

// grepExcludes returns the `| grep -v <pattern>` filters on a go list.
func grepExcludes(piece string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, m := range grepV.FindAllStringSubmatch(piece, -1) {
		if re, err := regexp.Compile(m[1] + m[2] + m[3]); err == nil {
			out = append(out, re)
		}
	}
	return out
}

// fuzzNames returns the regexp a -fuzz value gives, or nil when the value
// holds a variable.
func fuzzNames(piece string) *regexp.Regexp {
	m := fuzzFlag.FindStringSubmatch(piece)
	if m == nil {
		return nil
	}
	val := strings.ReplaceAll(m[1]+m[2]+m[3], "$$", "$")
	if strings.Contains(strings.TrimSuffix(val, "$"), "$") || strings.Contains(val, "{}") {
		return nil
	}
	re, err := regexp.Compile(val)
	if err != nil {
		return nil
	}
	return re
}

// pkgArgs lists the package arguments after `go test` or `go list`: tokens
// that start with a dot or a variable. None means the current directory.
func pkgArgs(piece, cwd string) []fuzzPkg {
	loc := goTestCmd.FindStringIndex(piece)
	if loc == nil {
		loc = goListCmd.FindStringIndex(piece)
	}
	var pkgs []fuzzPkg
	skip := false
	for tok := range strings.FieldsSeq(piece[loc[1]:]) {
		tok = strings.Trim(tok, `);"'`)
		switch {
		case skip:
			skip = false
		case strings.HasPrefix(tok, "-"):
			skip = !strings.Contains(tok, "=") && slices.Contains(valuedFlags, tok)
		case strings.HasPrefix(tok, "$"):
			pkgs = append(pkgs, fuzzPkg{any: true})
		case strings.HasPrefix(tok, "."):
			pkgs = append(pkgs, fuzzPkg{dir: path.Join(cwd, strings.TrimSuffix(tok, "/...")), tree: strings.HasSuffix(tok, "/...")})
		}
	}
	if len(pkgs) == 0 {
		pkgs = []fuzzPkg{{dir: cwd}}
	}
	return pkgs
}

// literalPkgs drops the variable packages: a loop variable ranges over what
// the go list command prints, which is the scope.
func literalPkgs(pkgs []fuzzPkg) []fuzzPkg {
	var out []fuzzPkg
	for _, p := range pkgs {
		if !p.any {
			out = append(out, p)
		}
	}
	return out
}

// cover says whether some go test -fuzz command runs the function: its name
// matches and its packages (for a variable, the discovered ones) include the
// function's directory.
func (s *fuzzRunSet) cover(f fuzzFunc) bool {
	fdir := path.Dir(f.file)
	for _, e := range s.entries {
		if s.entryRunsFunc(e, f.name, fdir) && s.entryRunsDir(e, fdir) {
			return true
		}
	}
	return false
}

// entryRunsFunc says whether the command's -fuzz value runs the function.
// go test -fuzz runs one function per package: a regexp that matches two in
// the package fuzzes none ("will not fuzz, -fuzz matches more than one").
func (s *fuzzRunSet) entryRunsFunc(e fuzzEntry, name, dir string) bool {
	if e.names == nil {
		return s.listed
	}
	if !e.names.MatchString(name) {
		return false
	}
	matches := 0
	for _, other := range s.byDir[dir] {
		if e.names.MatchString(other) {
			matches++
		}
	}
	return matches == 1
}

// entryRunsDir says whether the command's packages include dir; a variable
// package ranges over the discovery commands' scope (less its grep -v
// filters), or everything with none.
func (s *fuzzRunSet) entryRunsDir(e fuzzEntry, dir string) bool {
	for _, p := range e.pkgs {
		pkgs := []fuzzPkg{p}
		if p.any && len(s.scope) > 0 {
			if s.excluded(dir) {
				continue
			}
			pkgs = s.scope
		}
		if pkgsInclude(pkgs, dir) {
			return true
		}
	}
	return false
}

// excluded says whether a go list filter drops the package: the patterns
// match go list's import paths, so test them against "mod/" + dir + "/".
func (s *fuzzRunSet) excluded(dir string) bool {
	for _, re := range s.exclude {
		if re.MatchString("mod/" + dir + "/") {
			return true
		}
	}
	return false
}

// pkgsInclude says whether any package covers directory dir.
func pkgsInclude(pkgs []fuzzPkg, dir string) bool {
	for _, p := range pkgs {
		switch {
		case p.any, p.dir == dir:
			return true
		case p.tree && (p.dir == "." || strings.HasPrefix(dir+"/", p.dir+"/")):
			return true
		}
	}
	return false
}
