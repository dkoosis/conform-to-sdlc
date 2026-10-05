package checks

import (
	"os"
	"path/filepath"
)

// rootStrays are the entries that must not sit at the repo root (root-minimal).
// Four files the decision names, plus kg/: a vault directory inside a repo is
// never intended — it is what `trixi set` leaves when its --kg-root default
// (./kg) meets a repo cwd (sd-mzgy.6, mnemd and memorybench, 2026-09-02).
//
// The root is minimal and a root entry earns its place (decision d9cd0e20868b,
// dk 2026-09-02): README.md is the one file the root must carry, other
// documents live under docs/, and .claude/rules/** is the whole project
// instruction set — there is no CLAUDE.md. A file stays at the root only when
// some tool reads it there by name and nothing else can serve it
// (.golangci.yml, go.mod). AGENTS.md is on that list for Codex and the other
// harnesses that read nothing else — NOT for Claude Code, which reads
// CLAUDE.md and never AGENTS.md (code.claude.com/docs/en/memory.md). Which
// harness reads it matters, because it is the whole reason the file is a stub
// pointer rather than content: see the agents-stub rule.
//
// This is a deny-list, not an allowlist
// of everything a root may hold — the latter is a judgment nobody has made, and
// a checker that flagged LICENSE or tools.go would be guessing. The test the
// decision gives is GitHub's repo page: the README's first paragraph visible
// without scrolling.
var rootStrays = []struct{ name, msg, repair string }{
	{
		name:   "CLAUDE.md",
		msg:    "no CLAUDE.md — .claude/rules/** is the whole project instruction set, loaded at launch with the same priority and scoped where CLAUDE.md cannot be",
		repair: "fold anything load-bearing into .claude/rules/<topic>.md, then git rm CLAUDE.md",
	},
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
