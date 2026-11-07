package checks

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/values"
)

func testSpec() ScaffoldSpec {
	return ScaffoldSpec{
		Repo:    "widget",
		Owner:   "dkoosis",
		Module:  "github.com/dkoosis/widget",
		Prefix:  "wg",
		PlanDir: "~/Projects/dk/Project/widget/plans",
		LintPin: "v2.1.0",
		Profile: values.ProfileTool,
	}
}

// TestScaffoldPassesChecker is the bead's acceptance criterion: a scaffolded
// repo passes conform-to-sdlc unedited, on both profiles.
func TestScaffoldPassesChecker(t *testing.T) {
	for _, profile := range []values.Profile{values.ProfileTool, values.ProfileLib} {
		t.Run(string(profile), func(t *testing.T) {
			dir := t.TempDir()
			spec := testSpec()
			spec.Profile = profile
			if err := Scaffold(dir, spec); err != nil {
				t.Fatalf("Scaffold: %v", err)
			}
			findings := Run(dir)
			for _, f := range findings {
				t.Errorf("finding: %s", f)
			}
			if len(findings) != 0 {
				t.Fatalf("scaffolded repo has %d finding(s); want 0", len(findings))
			}
		})
	}
}

// TestScaffoldWritesOnlyInsideDir guards the temp-dir contract: every
// artifact path is repo-relative, so nothing can land outside the target.
func TestScaffoldWritesOnlyInsideDir(t *testing.T) {
	for _, a := range scaffoldArtifacts() {
		if filepath.IsAbs(a.path) || strings.Contains(a.path, "..") {
			t.Errorf("artifact escapes the target dir: %q", a.path)
		}
	}
}

// TestScaffoldEmitsNoRetiredFile: the emitter must never write a file the
// retired-files rule bans.
func TestScaffoldEmitsNoRetiredFile(t *testing.T) {
	dir := t.TempDir()
	if err := Scaffold(dir, testSpec()); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	for _, rel := range retiredFiles {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Errorf("scaffold emitted retired file %q", rel)
		}
	}
}

// TestEmitterDerivesFromCheckerVocabulary is the shared-renderer proof. Each
// case mutates a package variable the CHECKER reads, then asserts the
// EMITTER's output moved with it. A second templating path beside the
// checker's would not budge.
func TestEmitterDerivesFromCheckerVocabulary(t *testing.T) {
	tests := []struct {
		name     string
		artifact string
		mutate   func(t *testing.T) string // returns the token the output must carry
	}{
		{
			name:     "lint floor",
			artifact: ".golangci.yml",
			mutate: func(t *testing.T) string {
				t.Helper()
				old := floorEnable
				t.Cleanup(func() { floorEnable = old })
				floorEnable = slices.Concat(old, []string{"sentinellint"})
				return "sentinellint"
			},
		},
		{
			name:     "makefile check floor",
			artifact: "Makefile",
			mutate: func(t *testing.T) string {
				t.Helper()
				old := checkFloor
				t.Cleanup(func() { checkFloor = old })
				checkFloor = slices.Concat(old, []string{"sentineltarget"})
				return "sentineltarget"
			},
		},
		{
			name:     "tracked hook events",
			artifact: ".githooks/sentinel-event",
			mutate: func(t *testing.T) string {
				t.Helper()
				old := trackedHookEvents
				t.Cleanup(func() { trackedHookEvents = old })
				trackedHookEvents = slices.Concat(old, []string{"sentinel-event"})
				return delegationMarker
			},
		},
		{
			name:     "bd config keys",
			artifact: bdConfigFile,
			mutate: func(t *testing.T) string {
				t.Helper()
				old := bdKeys
				t.Cleanup(func() { bdKeys = old })
				bdKeys = append(slices.Clone(old), bdKey{
					key:   "custom.sentinel_key",
					paths: []string{"custom.sentinel_key"},
					why:   "sentinel",
				})
				return "custom.sentinel_key"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.mutate(t)
			dir := t.TempDir()
			if err := Scaffold(dir, testSpec()); err != nil {
				t.Fatalf("Scaffold: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(dir, tc.artifact))
			if err != nil {
				t.Fatalf("read %s: %v", tc.artifact, err)
			}
			if !strings.Contains(string(data), want) {
				t.Errorf("%s does not carry %q — the emitter is not reading the checker's vocabulary", tc.artifact, want)
			}
		})
	}
}

// TestScaffoldRefusesExistingFile: init scaffolds, it never overwrites.
func TestScaffoldRefusesExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Scaffold(dir, testSpec()); err == nil {
		t.Fatal("Scaffold overwrote an existing Makefile; want refusal")
	}
	data, err := os.ReadFile(filepath.Join(dir, "Makefile"))
	if err != nil || string(data) != "x" {
		t.Fatalf("existing Makefile was clobbered: %q, %v", data, err)
	}
}

func TestScaffoldRejectsBadSpec(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*ScaffoldSpec)
	}{
		{"no repo", func(s *ScaffoldSpec) { s.Repo = "" }},
		{"one-letter prefix", func(s *ScaffoldSpec) { s.Prefix = "w" }},
		{"undefaultable prefix", func(s *ScaffoldSpec) { s.Repo, s.Prefix = "9", "" }},
		{"long prefix", func(s *ScaffoldSpec) { s.Prefix = "widget" }},
		{"bad profile", func(s *ScaffoldSpec) { s.Profile = "app" }},
		{"path in repo name", func(s *ScaffoldSpec) { s.Repo = "../escape" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := testSpec()
			tc.mut(&spec)
			if err := Scaffold(t.TempDir(), spec); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

// TestScaffoldSpecDefaults: the spec fills its own blanks from the repo
// name, so `conform-to-sdlc init <repo>` is enough.
func TestScaffoldSpecDefaults(t *testing.T) {
	spec := ScaffoldSpec{Repo: "widget"}
	spec.applyDefaults()
	if spec.Profile != values.ProfileTool {
		t.Errorf("Profile = %q, want tool", spec.Profile)
	}
	if spec.Prefix != "wid" {
		t.Errorf("Prefix = %q, want wid", spec.Prefix)
	}
	if spec.Module != "github.com/"+defaultOwner+"/widget" {
		t.Errorf("Module = %q", spec.Module)
	}
	if !strings.HasSuffix(spec.PlanDir, "/widget/plans") {
		t.Errorf("PlanDir = %q", spec.PlanDir)
	}
	if spec.LintPin == "" {
		t.Error("LintPin empty — the pin rule has nothing to read")
	}
}

// TestScaffoldSelfcheckRunsThisTool: a scaffolded repo's selfcheck must call
// the binary by its current name. The rename to conform-to-sdlc missed this
// recipe once, and every new repo would have run a binary that no longer ships.
func TestScaffoldSelfcheckRunsThisTool(t *testing.T) {
	mk := renderMakefile(testSpec())
	_, after, ok := strings.Cut(mk, "\nselfcheck:")
	if !ok {
		t.Fatalf("scaffolded Makefile has no selfcheck target:\n%s", mk)
	}
	lines := strings.SplitN(after, "\n", 3)
	if len(lines) < 2 || lines[1] != "\tconform-to-sdlc" {
		t.Errorf("selfcheck recipe = %q; want %q", lines[1], "\tconform-to-sdlc")
	}
}

// TestScaffoldAuditGatesCheckSomething: each rigor gate the scaffold emits must
// be able to fail on the repo's own code (PR #57 review, cfm-60e). jscpd exits 0
// with no threshold, nilaway without -include-pkgs reports on dependencies, and
// a FUZZTIME the recipe ignores misleads `make help`.
func TestScaffoldAuditGatesCheckSomething(t *testing.T) {
	mk := renderMakefile(testSpec())
	for _, want := range []string{
		"\nFUZZTIME ?= 10s\n",
		"-fuzztime=$(FUZZTIME) ",
		"jscpd --threshold 3 --ignore '**/.git/**,**/.beads/**,**/.sandbox/**' .",
		"nilaway@latest -include-pkgs=$$(go list -m) ./...",
	} {
		if !strings.Contains(mk, want) {
			t.Errorf("scaffolded Makefile lacks %q", want)
		}
	}
}
