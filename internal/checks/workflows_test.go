package checks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

func TestCIGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yml     string // "" = no file
		wantMsg string // "" = expect clean
	}{
		{
			name: "conforming: make check plus make race",
			yml:  goodCheckYML,
		},
		{
			name:    "missing workflow",
			wantMsg: "no check workflow",
		},
		{
			name:    "no make check step",
			yml:     strings.Replace(goodCheckYML, "run: make check", "run: make lint", 1),
			wantMsg: "no step runs",
		},
		{
			name:    "gate re-implemented in YAML",
			yml:     strings.Replace(goodCheckYML, "run: make race", "run: go test -race ./...", 1),
			wantMsg: "re-implements the gate",
		},
		{
			name:    "golangci-lint run in YAML",
			yml:     strings.Replace(goodCheckYML, "run: make race", "run: golangci-lint run ./...", 1),
			wantMsg: "re-implements the gate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := map[string]string{}
			if tt.yml != "" {
				files[".github/workflows/check.yml"] = tt.yml
			}
			dir := writeRepo(t, files)

			findings := checks.CheckCIGate(dir)
			assertOneOrClean(t, findings, checks.RuleCIGate, tt.wantMsg)
		})
	}
}

func TestCodexShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yml     string // "" = no file
		wantMsg string
	}{
		{
			name: "conforming: issue_comment trigger",
			yml:  goodCodexYML,
		},
		{
			name: "absent workflow is fine — only the shape is contractual",
		},
		{
			name:    "auto-fire pull_request trigger",
			yml:     strings.Replace(goodCodexYML, "issue_comment:\n    types: [created]", "pull_request:", 1),
			wantMsg: "surprise OpenAI spend",
		},
		{
			name:    "pull_request alongside issue_comment",
			yml:     strings.Replace(goodCodexYML, "on:\n", "on:\n  pull_request:\n", 1),
			wantMsg: "surprise OpenAI spend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := map[string]string{}
			if tt.yml != "" {
				files[".github/workflows/codex-review.yml"] = tt.yml
			}
			dir := writeRepo(t, files)

			findings := checks.CheckCodexShape(dir)
			assertOneOrClean(t, findings, checks.RuleCodexShape, tt.wantMsg)
		})
	}
}

// assertOneOrClean expects zero findings when wantMsg is empty, else at
// least one finding of rule containing wantMsg (with a repair).
func assertOneOrClean(t *testing.T, findings []checks.Finding, rule, wantMsg string) {
	t.Helper()
	if wantMsg == "" {
		if len(findings) != 0 {
			t.Errorf("want clean, got %v", findings)
		}
		return
	}
	for _, f := range findings {
		if f.Rule == rule && strings.Contains(f.Msg, wantMsg) {
			if f.Repair == "" {
				t.Error("finding carries no repair command")
			}
			return
		}
	}
	t.Errorf("no %s finding containing %q in %v", rule, wantMsg, findings)
}

// noDetectYML is the pre-cfm-ac8 shape: make check runs on every PR, docs or not.
const noDetectYML = `name: check
on:
  pull_request:
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: check
        run: make check
`

// hardcodedDetectYML (cfm-31t): a detect job that carries the canonical
// docs-only regex but never wires its match result to the output it writes —
// run_check is a fixed "false" literal, so a non-docs change can never flip
// the gate on.
const hardcodedDetectYML = `name: check
on:
  pull_request:
jobs:
  detect:
    runs-on: ubuntu-latest
    outputs:
      run_check: ${{ steps.diff.outputs.run_check }}
    steps:
      - id: diff
        run: |
          git diff --name-status "$base" "$head" | awk -F'\t' '
            $2 !~ /(\.md$|^docs\/|^\.beads\/|^\.claude\/|^\.gitignore$|^LICENSE$)/ { print "true"; exit }
          '
          echo "run_check=false" >> "$GITHUB_OUTPUT"
  check:
    needs: detect
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: check
        if: needs.detect.outputs.run_check == 'true'
        run: make check
`

func TestCIDocsSkip(t *testing.T) {
	t.Parallel()

	shape, err := os.ReadFile("testdata/fixtures/fleet-shape.check.yml")
	if err != nil {
		t.Fatal(err)
	}
	const canonical = `(\.md$|^docs\/|^\.beads\/|^\.claude\/|^\.gitignore$|^LICENSE$)`

	tests := []struct {
		name    string
		yml     string // "" = no file
		wantMsg string // "" = expect clean
	}{
		{
			name: "conforming: detect job guards make check",
			yml:  goodCheckYML,
		},
		{
			name: "fleet-shape check.yml",
			yml:  string(shape),
		},
		{
			name: "absent workflow is ci-gate's finding, not this rule's",
		},
		{
			name:    "no docs-only detect job",
			yml:     noDetectYML,
			wantMsg: "no job decides docs-only",
		},
		{
			name:    "paths-ignore on the gate workflow",
			yml:     strings.Replace(goodCheckYML, "  pull_request:\n", "  pull_request:\n    paths-ignore: ['**.md']\n", 1),
			wantMsg: "paths-ignore",
		},
		{
			name:    "paths on the gate workflow",
			yml:     strings.Replace(goodCheckYML, "  pull_request:\n", "  pull_request:\n    paths: ['**.go']\n", 1),
			wantMsg: "paths",
		},
		{
			name:    "detect job exists but make check is unguarded",
			yml:     strings.Replace(goodCheckYML, "        if: needs.detect.outputs.run_check == 'true'\n", "", 1),
			wantMsg: "no job decides docs-only",
		},
		{
			name:    "widening: detect ignores a path outside conform-to-sdlc's docs set",
			yml:     strings.Replace(goodCheckYML, canonical, `(\.md$|^docs\/|^scripts\/)`, 1),
			wantMsg: "widens",
		},
		{
			name: "narrowing: detect ignores a subset of conform-to-sdlc's docs set",
			yml:  strings.Replace(goodCheckYML, canonical, `(\.md$|^docs\/)`, 1),
		},
		{
			// cfm-31t: the gate step's condition is a literal, never naming
			// detect's output — the finding must name the gate job so dk
			// knows which step to fix.
			name:    "gate step condition is a literal, not detect's output",
			yml:     strings.Replace(goodCheckYML, "if: needs.detect.outputs.run_check == 'true'", "if: false", 1),
			wantMsg: `job "check"`,
		},
		{
			// cfm-31t: the gate job never declares needs: detect, so its if
			// condition (however it reads) can never resolve.
			name:    "gate job carries no needs: detect",
			yml:     strings.Replace(goodCheckYML, "  check:\n    needs: detect\n", "  check:\n", 1),
			wantMsg: `job "check"`,
		},
		{
			// cfm-31t: detect's regex is present and canonical, but the
			// output it writes never varies with the match — a non-docs
			// change can never flip run_check true.
			name:    "detect job hard-codes run_check regardless of the diff",
			yml:     hardcodedDetectYML,
			wantMsg: "never varies with the diff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := map[string]string{}
			if tt.yml != "" {
				files[".github/workflows/check.yml"] = tt.yml
			}
			dir := writeRepo(t, files)

			findings := checks.CheckCIDocsSkip(dir)
			assertOneOrClean(t, findings, checks.RuleCIDocsSkip, tt.wantMsg)
			for _, f := range findings {
				if f.File != ".github/workflows/check.yml" {
					t.Errorf("finding names %q, want the workflow file", f.File)
				}
			}
		})
	}
}

// TestRun_NoDetectJobFails: the full runner (what `conform-to-sdlc` exits on) carries
// the finding, rendered with file, rule and repair.
func TestRun_NoDetectJobFails(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	files[".github/workflows/check.yml"] = noDetectYML
	dir := writeRepo(t, files)

	var hit *checks.Finding
	for _, f := range checks.Run(dir) {
		if f.Rule == checks.RuleCIDocsSkip {
			hit = &f
			break
		}
	}
	if hit == nil {
		t.Fatal("Run reported no ci-docs-skip finding for a workflow with no detect job")
	}
	s := hit.String()
	for _, want := range []string{".github/workflows/check.yml", checks.RuleCIDocsSkip, "conform-to-sdlc --fix"} {
		if !strings.Contains(s, want) {
			t.Errorf("finding %q lacks %q", s, want)
		}
	}
}

// TestFix_WritesCheckWorkflowWithDetectJob: --fix creates an absent workflow
// that passes this rule, and never rewrites an existing one.
func TestFix_WritesCheckWorkflowWithDetectJob(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	delete(files, ".github/workflows/check.yml")
	dir := writeRepo(t, files)

	done, err := checks.Fix(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !anyContains(done, ".github/workflows/check.yml") {
		t.Fatalf("Fix reported no check.yml action: %+v", done)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "check.yml"))
	if err != nil {
		t.Fatalf("Fix wrote no workflow: %v", err)
	}
	if !strings.Contains(string(body), "detect:") {
		t.Errorf("written workflow carries no detect job:\n%s", body)
	}
	if got := checks.CheckCIDocsSkip(dir); len(got) != 0 {
		t.Errorf("the --fix workflow fails ci-docs-skip: %+v", got)
	}
	if got := checks.CheckCIGate(dir); len(got) != 0 {
		t.Errorf("the --fix workflow fails ci-gate: %+v", got)
	}
}

func TestFix_LeavesExistingCheckWorkflowBytes(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	files[".github/workflows/check.yml"] = noDetectYML
	dir := writeRepo(t, files)

	if _, err := checks.Fix(dir); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "check.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != noDetectYML {
		t.Errorf("Fix rewrote an existing workflow:\n%s", body)
	}
}

// TestCIDocsSkip_Budget: the rule sits on the in-check surface, <1s.
func TestCIDocsSkip_Budget(t *testing.T) {
	shape, err := os.ReadFile("testdata/fixtures/fleet-shape.check.yml")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeRepo(t, map[string]string{".github/workflows/check.yml": string(shape)})

	start := time.Now()
	checks.CheckCIDocsSkip(dir)
	if d := time.Since(start); d > time.Second {
		t.Errorf("ci-docs-skip took %v, budget is 1s", d)
	}
}
