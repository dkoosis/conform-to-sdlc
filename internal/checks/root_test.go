package checks_test

import (
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/checks"
)

// TestRootMinimal_CleanRootPasses: the reference repo keeps its declarations
// under docs/, so root-minimal has nothing to say.
func TestRootMinimal_CleanRootPasses(t *testing.T) {
	t.Parallel()
	dir := writeRepo(t, goodRepo())
	if got := checks.CheckRootMinimal(dir); len(got) != 0 {
		t.Fatalf("clean root: want no findings, got %+v", got)
	}
}

// TestRootMinimal_EachStrayIsAFinding: every file the decision names is
// flagged on its own, with a repair that names where it goes.
func TestRootMinimal_EachStrayIsAFinding(t *testing.T) {
	t.Parallel()
	for _, name := range checks.RootStrayNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			files := goodRepo()
			if name == "kg" {
				files[name+"/reference/x.md"] = "x\n"
			} else {
				files[name] = "x\n"
			}
			dir := writeRepo(t, files)
			got := checks.CheckRootMinimal(dir)
			if len(got) != 1 || got[0].File != name || got[0].Rule != checks.RuleRootMinimal {
				t.Fatalf("stray %s: want one root-minimal finding, got %+v", name, got)
			}
			if got[0].Repair == "" {
				t.Fatalf("stray %s: finding carries no repair", name)
			}
		})
	}
}

// TestRun_RootStrayFailsTheGate: a root ROADMAP.md fails Run in an otherwise
// conforming repo.
func TestRun_RootStrayFailsTheGate(t *testing.T) {
	t.Parallel()
	files := goodRepo()
	files["ROADMAP.md"] = "# repo\n"
	dir := writeRepo(t, files)
	var hit bool
	for _, f := range checks.Run(dir) {
		if f.Rule == checks.RuleRootMinimal && f.File == "ROADMAP.md" {
			hit = true
		}
	}
	if !hit {
		t.Fatal("root ROADMAP.md: want a root-minimal finding, got none")
	}
}
