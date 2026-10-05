package checks

import (
	"os"
	"path/filepath"
)

// rootStrays are the entries that must not sit at the repo root (root-minimal).
// Three files, plus kg/: a vault directory inside a repo is
// never intended — it is what `trixi set` leaves when its --kg-root default
// (./kg) meets a repo cwd (sd-mzgy.6, mnemd and memorybench, 2026-09-02).
//
// The root is minimal and a root entry earns its place (decision d9cd0e20868b,
// dk 2026-09-02): README.md is the one file the root must carry, and other
// documents live under docs/. A file stays at the root when some tool reads it
// there by name: .golangci.yml, go.mod, and the two agent files, AGENTS.md and
// the CLAUDE.md that imports it, which is how sdlc keeps its own.
//
// This is a deny-list, not an allowlist
// of everything a root may hold — the latter is a judgment nobody has made, and
// a checker that flagged LICENSE or tools.go would be guessing. The test the
// decision gives is GitHub's repo page: the README's first paragraph visible
// without scrolling.
var rootStrays = []struct{ name, msg, repair string }{
	{
		name:   "ROADMAP.md",
		msg:    "no roadmap at the root — an epic's state lives on its bead (sdlc SPEC.md, R11), and the root is minimal",
		repair: "git rm ROADMAP.md, or git mv ROADMAP.md docs/ROADMAP.md while the repo still reads it",
	},
	{
		name:   "conform.json",
		msg:    "repo-level declarations live under docs/ — the root is minimal",
		repair: "git mv conform.json " + ValuesFile,
	},
	{
		name:   "NORTH_STAR.md",
		msg:    "a Publish-To reflection of the kg's page lives under docs/ — the root is minimal",
		repair: "git mv NORTH_STAR.md docs/NORTH_STAR.md (and repoint the publish target)",
	},
	{
		name:   "kg",
		msg:    "a vault inside the repo — a nug writer defaulted its root to ./kg (trixi set) instead of $MNEMD_NUGBASE",
		repair: "move the nugs into $MNEMD_NUGBASE with mnemd capture or mnemd index, then rm -r kg",
	},
}

// checkRootMinimal reports each rootStray present at the repo root.
func checkRootMinimal(dir string) []Finding {
	var findings []Finding
	for _, s := range rootStrays {
		if _, err := os.Stat(filepath.Join(dir, s.name)); err == nil {
			findings = append(findings, Finding{
				File:   s.name,
				Rule:   RuleRootMinimal,
				Msg:    s.msg,
				Repair: s.repair,
			})
		}
	}
	return findings
}
