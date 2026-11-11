package checks

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	// fuzzList is the discovery form: go test -list '^Fuzz'.
	fuzzList = regexp.MustCompile(`go\s+test\b[^\n;|&]*\s-list\b[^\n;|&]*Fuzz`)
)

// skipFuzzDir says whether the walk leaves a directory alone.
func skipFuzzDir(name string) bool {
	return name == "vendor" || name == "node_modules" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// findFuzzFuncs parses every _test.go file under dir for Fuzz functions.
func findFuzzFuncs(dir string) []fuzzFunc {
	var found []fuzzFunc
	fset := token.NewFileSet()
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil //nolint:nilerr // an unreadable entry is skipped, not a finding
		case d.IsDir() && path != dir && skipFuzzDir(d.Name()):
			return filepath.SkipDir
		case d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go"):
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
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
	if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Fuzz") || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
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
	recipe, hasTarget, ok := fuzzRecipe(dir)
	if !ok {
		return nil
	}

	var why string
	switch {
	case !hasTarget:
		why = "the Makefile has no fuzz target"
	case !strings.Contains(recipe, "-fuzz"):
		why = "the fuzz target runs no `go test -fuzz`"
	case fuzzList.MatchString(recipe):
		return nil // discovers every Fuzz function by construction
	}
	var findings []Finding
	for _, f := range funcs {
		if why == "" && fuzzNamed(recipe, f.name) {
			continue
		}
		reason := why
		if reason == "" {
			reason = "the fuzz target never names it"
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

// fuzzRecipe returns the text of the fuzz target's recipes and of every
// target it lists as a prerequisite; ok is false when there is no Makefile.
func fuzzRecipe(dir string) (recipe string, hasTarget, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, makefileFile))
	if err != nil {
		return "", false, false
	}
	byName := map[string]mkTarget{}
	for _, t := range parseMakefile(string(data)) {
		if _, dup := byName[t.name]; !dup {
			byName[t.name] = t
		}
	}
	if _, has := byName["fuzz"]; !has {
		return "", false, true
	}
	recipes, _ := parseRecipes(string(data))
	var body strings.Builder
	for name := range reachable(byName, "fuzz") {
		for _, c := range recipes[name] {
			body.WriteString(blankComments(c.text))
			body.WriteByte('\n')
		}
	}
	return body.String(), true, true
}

// blankComments drops a shell comment tail from each line of a command.
func blankComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if head, _, found := strings.Cut(l, " #"); found {
			lines[i] = head
		}
	}
	return strings.Join(lines, "\n")
}

// fuzzNamed says whether some -fuzz value in the recipe selects the function:
// a plain name, or a regexp that matches it. A value that holds a variable
// (the loop form `"^$$f$$"`) selects nothing by itself.
func fuzzNamed(recipe, name string) bool {
	for _, m := range fuzzFlag.FindAllStringSubmatch(recipe, -1) {
		val := m[1] + m[2] + m[3]
		val = strings.ReplaceAll(val, "$$", "$")
		if strings.Contains(val, "$(") || strings.Contains(val, "${") || strings.Contains(strings.TrimSuffix(val, "$"), "$") {
			continue
		}
		re, err := regexp.Compile(val)
		if err == nil && re.MatchString(name) {
			return true
		}
	}
	return false
}
