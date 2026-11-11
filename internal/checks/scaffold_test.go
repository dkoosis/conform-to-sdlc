package checks

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dkoosis/repo-standard/internal/values"
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
// repo passes repo-standard unedited, on both profiles.
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
			// The scaffold's .golangci.yml is the canonical config lint-set
			// reads; the floor is inside it (TestCanonicalGolangciHoldsTheFloor).
			name:     "lint set",
			artifact: ".golangci.yml",
			mutate: func(t *testing.T) string {
				t.Helper()
				old := CanonicalGolangci
				t.Cleanup(func() { CanonicalGolangci = old })
				CanonicalGolangci = strings.Replace(old, "  enable:\n", "  enable:\n    - sentinellint\n", 1)
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
// name, so `repo-standard init <repo>` is enough.
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

// TestScaffoldStandardRunsThisTool: a scaffolded repo's standard must call
// the binary by its current name. The rename to repo-standard missed this
// recipe once, and every new repo would have run a binary that no longer ships.
func TestScaffoldStandardRunsThisTool(t *testing.T) {
	mk := renderMakefile(testSpec())
	_, after, ok := strings.Cut(mk, "\nstandard:")
	if !ok {
		t.Fatalf("scaffolded Makefile has no standard target:\n%s", mk)
	}
	lines := strings.SplitN(after, "\n", 3)
	if len(lines) < 2 || lines[1] != "\trepo-standard" {
		t.Errorf("standard recipe = %q; want %q", lines[1], "\trepo-standard")
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
		"jscpd@$(JSCPD_VER) --threshold 3 --ignore '**/.git/**,**/.beads/**,**/.sandbox/**' .",
		"nilaway@$(NILAWAY_VER) -include-pkgs=$$(go list -m) ./...",
		"\nOMITEMPTYSCALAR_VER := v0.4.0\n",
		"go run github.com/dkoosis/repo-standard/cmd/omitemptyscalar@$(OMITEMPTYSCALAR_VER) ./...",
		"audit: check race fuzz dupe nilcheck deadcode omitempty vuln ",
	} {
		if !strings.Contains(mk, want) {
			t.Errorf("scaffolded Makefile lacks %q", want)
		}
	}
}
