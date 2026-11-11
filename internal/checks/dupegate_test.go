package checks_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/dkoosis/repo-standard/internal/checks"
	"github.com/dkoosis/repo-standard/internal/values"
)

func dupeRepo(t *testing.T, recipe string, extra map[string]string) string {
	t.Helper()
	files := map[string]string{"Makefile": withRecipe("dupe", recipe)}
	maps.Copy(files, extra)
	return writeRepo(t, files)
}

func TestDupeGate(t *testing.T) {
	t.Parallel()
	const bare = "\tnpx --yes jscpd@5.1.2 --ignore '**/.git/**' .\n"
	tests := map[string]struct {
		recipe string
		files  map[string]string
		want   bool // a finding
		msg    string
	}{
		"no threshold, no config":                {recipe: bare, want: true, msg: "no --threshold"},
		"config without threshold":               {recipe: bare, files: map[string]string{".jscpd.json": `{"gitignore": true}`}, want: true, msg: `sets no "threshold"`},
		"at-sign prefix":                         {recipe: "\t@npx jscpd .\n", want: true, msg: "no --threshold"},
		"at-sign prefix, threshold 10":           {recipe: "\t@npx jscpd --threshold 10 .\n", want: true, msg: "above 3%"},
		"dash prefix":                            {recipe: "\t-npx jscpd .\n", want: true, msg: "no --threshold"},
		"mixed prefixes":                         {recipe: "\t@-+npx jscpd .\n", want: true, msg: "no --threshold"},
		"local bin path":                         {recipe: "\t./node_modules/.bin/jscpd .\n", want: true, msg: "no --threshold"},
		"flag 3":                                 {recipe: "\tnpx --yes jscpd@5.1.2 --threshold 3 .\n"},
		"flag 2.5 with equals":                   {recipe: "\tnpx --yes jscpd@5.1.2 --threshold=2.5 .\n"},
		"flag 10":                                {recipe: "\tnpx --yes jscpd@5.1.2 --threshold 10 .\n", want: true, msg: "above 3%"},
		"flag via make variable":                 {recipe: "\tnpx --yes jscpd@5.1.2 --threshold $(DUPE_MAX) .\n", files: map[string]string{"Makefile": withRecipe("dupe", "\tnpx --yes jscpd@5.1.2 --threshold $(DUPE_MAX) .\n") + "DUPE_MAX := 8\n"}, want: true, msg: "above 3%"},
		"config threshold 3":                     {recipe: bare, files: map[string]string{".jscpd.json": `{"threshold": 3}`}},
		"config threshold 10":                    {recipe: bare, files: map[string]string{".jscpd.json": `{"threshold": 10}`}, want: true, msg: `"threshold": 10`},
		"flag beats a loose config":              {recipe: "\tnpx --yes jscpd@5.1.2 --threshold 3 .\n", files: map[string]string{".jscpd.json": `{"threshold": 10}`}},
		"config flag names a file":               {recipe: "\tnpx --yes jscpd@5.1.2 --config ci/jscpd.json .\n", files: map[string]string{"ci/jscpd.json": `{"threshold": 3}`, ".jscpd.json": `{"threshold": 10}`}},
		"other command's -t is not jscpd's":      {recipe: "\tTMP=$$(mktemp -d -t jscpd); npx --yes jscpd@5.1.2 . --threshold 3 --output $$TMP\n"},
		"a -t on another command does not count": {recipe: "\tmktemp -d -t 2; npx --yes jscpd@5.1.2 .\n", want: true, msg: "no --threshold"},
		"continued command":                      {recipe: "\tnpx --yes jscpd@5.1.2 . \\\n\t\t--threshold 3\n"},
		"another tool, out of scope":             {recipe: "\tgo run github.com/mibk/dupl@v1.0.0 ./...\n"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := dupeRepo(t, tc.recipe, tc.files)
			got := checks.CheckDupeGate(dir)
			if !tc.want {
				if len(got) != 0 {
					t.Fatalf("want no finding, got %v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Rule != checks.RuleDupeGate || !strings.Contains(got[0].Msg, tc.msg) || !strings.Contains(got[0].Repair, "--threshold 3") {
				t.Fatalf("want one dupe-gate finding containing %q with the repair, got %v", tc.msg, got)
			}
		})
	}
}

func TestDupeGate_ExceptionExcuses(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	files["Makefile"] = withRecipe("dupe", "\tnpx --yes jscpd@5.1.2 .\n")
	files["docs/repo-standard.json"] = `{"profile": "tool", "exceptions": [{"rule": "dupe-gate", "reason": "8% today; ratchet down"}]}`
	if got := rulesOf(checks.Run(writeRepo(t, files)))[checks.RuleDupeGate]; got != 0 {
		t.Fatalf("exception did not excuse dupe-gate: %d findings", got)
	}
	files["docs/repo-standard.json"] = goodValues
	if got := rulesOf(checks.Run(writeRepo(t, files)))[checks.RuleDupeGate]; got != 1 {
		t.Fatalf("without the exception want 1 dupe-gate finding, got %d", got)
	}
}

const (
	strictDeadcode = "\t@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./cmd/x) || { echo could not run; exit 1; }; if [ -n \"$$out\" ]; then echo \"$$out\"; exit 1; fi\n"
	testDeadcode   = "\t@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) -test ./...) || { echo could not run; exit 1; }; if [ -n \"$$out\" ]; then echo \"$$out\"; exit 1; fi\n"
	skipDeadcode   = "\t@go list -f '{{.Name}}' ./... | grep -qx main || { echo no main; exit 0; }; out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./...) || exit 1; if [ -n \"$$out\" ]; then echo \"$$out\"; exit 1; fi\n"
	filtered       = "\t@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./cmd/x | grep -v -e 'hookA' -e 'hookB') || exit 1; if [ -n \"$$out\" ]; then echo \"$$out\"; exit 1; fi\n"
)

func deadcodeRepo(t *testing.T, recipe string, withMain bool) string {
	t.Helper()
	files := map[string]string{"Makefile": withRecipe("deadcode", recipe), "x.go": "package x\n"}
	if withMain {
		files["cmd/x/main.go"] = "package main\n\nfunc main() {}\n"
	}
	return writeRepo(t, files)
}

func TestDeadcodeGate_Roots(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		recipe  string
		hasMain bool
		want    string // substring of the finding, "" for none
	}{
		"library, skip form":              {skipDeadcode, false, "no main package"},
		"library, strict form":            {strictDeadcode, false, "no main package"},
		"library, -test form":             {testDeadcode, false, ""},
		"tool, strict form":               {strictDeadcode, true, ""},
		"tool, -test form":                {testDeadcode, true, "runs with -test"},
		"tool, skip form (strict enough)": {skipDeadcode, true, ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := checks.CheckDeadcodeGate(deadcodeRepo(t, tc.recipe, tc.hasMain), values.Values{})
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("want none, got %v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Rule != checks.RuleDeadcodeGate || !strings.Contains(got[0].Msg, tc.want) || got[0].Repair == "" {
				t.Fatalf("want one deadcode-gate finding containing %q, got %v", tc.want, got)
			}
		})
	}
}

func TestDeadcodeGate_MainInIgnoredFilesDoesNotCount(t *testing.T) {
	t.Parallel()
	dir := writeRepo(t, map[string]string{
		"Makefile":                 withRecipe("deadcode", testDeadcode),
		"x.go":                     "package x\n",
		"scripts/gen.go":           "//go:build ignore\n\npackage main\n\nfunc main() {}\n",
		"internal/testdata/m/m.go": "package main\n",
		"main_test.go":             "package main\n",
		".claude/worktrees/w/m.go": "package main\n",
	})
	if got := checks.CheckDeadcodeGate(dir, values.Values{}); len(got) != 0 {
		t.Fatalf("library with only ignored mains must take the -test form: %v", got)
	}
}

func allow(names ...string) values.Values {
	m := map[string]string{}
	for _, n := range names {
		m[n] = "cross-package test hook"
	}
	return values.Values{Exceptions: []values.Exception{{Rule: values.FilterRule, Allow: m}}}
}

func TestDeadcodeFilter(t *testing.T) {
	t.Parallel()
	dir := deadcodeRepo(t, filtered, true)
	tests := map[string]struct {
		vals values.Values
		want []string // substrings, one finding each; none = pass
	}{
		"filter, no exception":     {values.Values{}, []string{"has no deadcode-filter exception"}},
		"filter, exact allow list": {allow("hookA", "hookB"), nil},
		"allow misses a pattern":   {allow("hookA"), []string{`"hookB" is not just names`}},
		"allow lists a stale name": {allow("hookA", "hookB", "hookC"), []string{`allows "hookC"`}},
		"filter with other tool":   {values.Values{}, nil},
	}
	delete(tests, "filter with other tool")
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := checks.CheckDeadcodeGate(dir, tc.vals)
			var gate int
			for _, f := range got {
				if f.Rule == checks.RuleDeadcodeFilter {
					gate++
				}
			}
			if gate != len(tc.want) {
				t.Fatalf("want %d deadcode-filter findings, got %v", len(tc.want), got)
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i].Msg, strings.Trim(w, `"`)) && !strings.Contains(got[i].Msg, w) {
					t.Errorf("finding %d = %q, want it to contain %q", i, got[i].Msg, w)
				}
			}
		})
	}
}

func TestDeadcodeFilter_OtherToolsAreNotVerifiable(t *testing.T) {
	t.Parallel()
	recipe := "\t@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./cmd/x | sed '/hookA/d'); if [ -n \"$$out\" ]; then exit 1; fi\n"
	got := checks.CheckDeadcodeGate(deadcodeRepo(t, recipe, true), allow("hookA"))
	var hit bool
	for _, f := range got {
		hit = hit || (f.Rule == checks.RuleDeadcodeFilter && strings.Contains(f.Msg, "sed filters"))
	}
	if !hit {
		t.Fatalf("a sed filter must fail deadcode-filter, got %v", got)
	}
}

func TestDeadcodeFilter_QuietGrepAndStrictFormAreNotFilters(t *testing.T) {
	t.Parallel()
	recipe := "\t@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./cmd/x); if echo \"$$out\" | grep -q .; then echo \"$$out\"; exit 1; fi\n"
	if got := checks.CheckDeadcodeGate(deadcodeRepo(t, recipe, true), values.Values{}); len(got) != 0 {
		t.Fatalf("want none, got %v", got)
	}
}

func TestValues_AllowOnlyOnFilterRule(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"allow on another rule":      `{"profile":"tool","exceptions":[{"rule":"dupe-gate","reason":"r","allow":{"f":"why"}}]}`,
		"filter rule with no allow":  `{"profile":"tool","exceptions":[{"rule":"deadcode-filter","reason":"r"}]}`,
		"allowed function no reason": `{"profile":"tool","exceptions":[{"rule":"deadcode-filter","allow":{"f":""}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := values.Parse(strings.NewReader(body)); err == nil {
				t.Fatal("want an error")
			}
		})
	}
	ok := `{"profile":"tool","exceptions":[{"rule":"deadcode-filter","allow":{"hook":"test hook in pkg b"}}]}`
	if _, err := values.Parse(strings.NewReader(ok)); err != nil {
		t.Fatalf("valid allow list rejected: %v", err)
	}
}

func TestDeadcodeFilter_AlternationCannotHideAnUnlistedFunction(t *testing.T) {
	t.Parallel()
	for _, pat := range []string{`'hookA\|realDead'`, `-E 'hookA|.*'`, `-E 'hookA.*'`,
		`-E 'hookA|^'`, `-E 'hookA|'`, `'hookA\|$'`, `-E '|hookA'`, `-E 'hookA||hookA'`} {
		recipe := "\t@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./cmd/x | grep -v " + pat + ") || exit 1; test -z \"$$out\"\n"
		got := checks.CheckDeadcodeGate(deadcodeRepo(t, recipe, true), allow("hookA"))
		if len(got) != 1 || got[0].Rule != checks.RuleDeadcodeFilter || !strings.Contains(got[0].Msg, "not just names") {
			t.Errorf("pattern %s: want one deadcode-filter finding, got %v", pat, got)
		}
	}
	sub := "\t@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./cmd/x | grep -v 'hookA') || exit 1; test -z \"$$out\"\n"
	if got := checks.CheckDeadcodeGate(deadcodeRepo(t, sub, true), allow("hook")); len(got) != 1 || !strings.Contains(got[0].Msg, "not just names") {
		t.Errorf("allowed hook must not accept pattern hookA, got %v", got)
	}
	ok := "\t@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./cmd/x | grep -v -E 'hookA|hookB') || exit 1; test -z \"$$out\"\n"
	if got := checks.CheckDeadcodeGate(deadcodeRepo(t, ok, true), allow("hookA", "hookB")); len(got) != 0 {
		t.Errorf("names joined by | must pass, got %v", got)
	}
}

func TestDeadcodeGate_ApostropheInAComment(t *testing.T) {
	t.Parallel()
	recipe := "\t# it's deadcode below\n\t@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) -test ./... | grep -v 'x') || exit 1\n"
	// An unpaired quote in one command must not pair with the next: no panic, and the filter is read.
	got := checks.CheckDeadcodeGate(deadcodeRepo(t, recipe, false), allow("x"))
	if len(got) != 0 {
		t.Fatalf("want none, got %v", got)
	}
}
