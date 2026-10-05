package checks_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

// TestReadme_ConformingRepoPasses: the reference repo carries a README.md
// opening with a heading, so the rule has nothing to say.
func TestReadme_ConformingRepoPasses(t *testing.T) {
	t.Parallel()
	dir := writeRepo(t, goodRepo())
	if got := checks.CheckReadme(dir); len(got) != 0 {
		t.Fatalf("conforming repo: want no findings, got %+v", got)
	}
}

// TestReadme_BrokenShapesAreFindings: each way a README fails the contract is
// its own finding, and each carries a repair.
func TestReadme_BrokenShapesAreFindings(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		body    string
		present bool
	}{
		"absent":           {present: false},
		"empty":            {body: "", present: true},
		"whitespace only":  {body: "\n\n   \n", present: true},
		"no heading":       {body: "conform-to-sdlc checks repos.\n", present: true},
		"heading too deep": {body: "## conform-to-sdlc\n\nprose.\n", present: true},
		"bare hash":        {body: "#conform-to-sdlc\n\nprose.\n", present: true},
		"empty heading":    {body: "# \n\nprose.\n", present: true},
		"heading not first": {
			body:    "a stray line.\n\n# conform-to-sdlc\n\nprose.\n",
			present: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			files := goodRepo()
			if tc.present {
				files[checks.ReadmeFile] = tc.body
			} else {
				delete(files, checks.ReadmeFile)
			}
			dir := writeRepo(t, files)
			got := checks.CheckReadme(dir)
			if len(got) != 1 || got[0].File != checks.ReadmeFile || got[0].Rule != checks.RuleReadme {
				t.Fatalf("%s: want one readme finding, got %+v", name, got)
			}
			if got[0].Repair == "" {
				t.Fatalf("%s: finding carries no repair", name)
			}
		})
	}
}

// TestReadme_LeadingBlankLinesStillPass: Markdown renders the same with or
// without them, so they must not be the difference between green and red.
func TestReadme_LeadingBlankLinesStillPass(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	files[checks.ReadmeFile] = "\n\n# conform-to-sdlc\n\nprose.\n"
	dir := writeRepo(t, files)
	if got := checks.CheckReadme(dir); len(got) != 0 {
		t.Fatalf("leading blank lines: want no findings, got %+v", got)
	}
}

// TestRun_MissingReadmeFailsTheGate: the rule is wired into Run, not just
// reachable from a unit test.
func TestRun_MissingReadmeFailsTheGate(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	delete(files, checks.ReadmeFile)
	dir := writeRepo(t, files)
	if rulesOf(checks.Run(dir))[checks.RuleReadme] != 1 {
		t.Fatalf("repo with no README.md: want one %s finding, got %+v", checks.RuleReadme, checks.Run(dir))
	}
}

// TestScaffold_EmitsAPassingReadme: `conform-to-sdlc init` promises a repo that passes
// unedited, so the README it writes must satisfy the rule it just added.
func TestScaffold_EmitsAPassingReadme(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := checks.Scaffold(dir, checks.ScaffoldSpec{Repo: "widget"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, checks.ReadmeFile))
	if err != nil {
		t.Fatalf("scaffold emitted no %s: %v", checks.ReadmeFile, err)
	}
	if !strings.HasPrefix(string(body), "# widget\n") {
		t.Fatalf("scaffolded README does not open naming the repo:\n%s", body)
	}
	if got := checks.CheckReadme(dir); len(got) != 0 {
		t.Fatalf("scaffolded README fails its own rule: %+v", got)
	}
	if !slices.Contains(checks.ScaffoldPaths(checks.ScaffoldSpec{Repo: "widget"}), checks.ReadmeFile) {
		t.Fatalf("%s missing from the list init prints", checks.ReadmeFile)
	}
}

// TestFix_WritesAReadmeThatStaysRed: --fix creates the absent file, and the
// skeleton it writes must NOT turn the gate green — a page nobody wrote is not
// an introduction.
func TestFix_WritesAReadmeThatStaysRed(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	delete(files, checks.ReadmeFile)
	dir := writeRepo(t, files)

	done, err := checks.Fix(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !anyContains(done, checks.ReadmeFile) {
		t.Fatalf("Fix reported no README.md action: %+v", done)
	}
	if _, err := os.Stat(filepath.Join(dir, checks.ReadmeFile)); err != nil {
		t.Fatalf("Fix reported an action but wrote nothing: %v", err)
	}
	if got := checks.CheckReadme(dir); len(got) != 1 {
		t.Fatalf("the --fix skeleton must stay red until a person writes the heading, got %+v", got)
	}

	// Second run is silent and never overwrites.
	again, err := checks.Fix(dir)
	if err != nil {
		t.Fatal(err)
	}
	if anyContains(again, checks.ReadmeFile) {
		t.Fatalf("Fix touched an existing README.md: %+v", again)
	}
}

func anyContains(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}
