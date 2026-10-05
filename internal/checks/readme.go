package checks

import (
	"os"
	"path/filepath"
	"strings"
)

// ReadmeFile is the one file the repo root must carry (decision d9cd0e20868b,
// dk 2026-09-02). Every other direction document moved under docs/ so that
// GitHub's repo page shows this file's first paragraph without scrolling —
// which is the legibility test the decision names, and it fails the same way
// whether the root is crowded or the README is absent.
//
// root-minimal is the deny half of that decision and this is the require half.
// They are separate rules because they fail for opposite reasons and their
// repairs point opposite ways: one says move a file out, the other says write
// one.
const ReadmeFile = "README.md"

// checkReadme verifies the repo carries a non-empty README.md opening with a
// heading (readme).
//
// Three failures, deliberately distinct. Absent means the repo page has no
// first paragraph at all. Present-but-empty is worse: the file exists, so
// every reader stops looking. Present with no opening heading is the shape
// GitHub renders as a wall of prose with nothing naming what it is looking at.
//
// What is NOT checked: whether the heading matches the repo's name. Nothing in
// a repo states its own canonical name — a worktree, a fork, a clone under
// another directory name and a submodule checkout all disagree with each
// other, and go.mod is absent under the lib and non-Go profiles. A checker
// that compared the heading to filepath.Base(dir) would fire on every
// worktree in the fleet, so this rule guards the artifact's existence and its
// one machine-read line.
func checkReadme(dir string) []Finding {
	data, err := os.ReadFile(filepath.Join(dir, ReadmeFile))
	if err != nil {
		return []Finding{{
			File:   ReadmeFile,
			Rule:   RuleReadme,
			Msg:    "no README.md — the one file the root must carry, and the first thing GitHub renders",
			Repair: "conform-to-sdlc --fix (writes a " + ReadmeFile + " skeleton to fill in)",
		}}
	}
	body := string(data)
	if strings.TrimSpace(body) == "" {
		return []Finding{{
			File:   ReadmeFile,
			Rule:   RuleReadme,
			Msg:    "empty README.md — the file exists, so every reader stops looking, and it answers nothing",
			Repair: "write a `# <repo>` heading and a paragraph saying what this repo is",
		}}
	}
	if !hasOpeningHeading(body) {
		return []Finding{{
			File:   ReadmeFile,
			Rule:   RuleReadme,
			Msg:    "no opening heading — the page renders as prose with nothing naming what it is",
			Repair: "make the first non-blank line `# <repo>`",
		}}
	}
	return nil
}

// hasOpeningHeading reports whether the first non-blank line (ignoring
// one-line HTML comments, which render as nothing) is a level-1
// ATX heading carrying text. Leading blank lines are skipped because Markdown
// renders identically with or without them; a heading further down the page is
// not the opening one, so scanning stops at the first line with content.
func hasOpeningHeading(body string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == "" || isHTMLComment(line) {
			continue
		}
		rest, ok := strings.CutPrefix(line, "# ")
		return ok && strings.TrimSpace(rest) != ""
	}
	return false
}

// ReadmeSkeleton renders a starting README.md for `conform-to-sdlc --fix` to drop into
// an existing repo. Its opening line is a prompt and NOT a heading, so a repo
// that runs --fix and stops still fails the readme rule and says why, rather
// than passing with a page nobody wrote.
func ReadmeSkeleton(repo string) string {
	return readmeDoc(repo, readmeFixOpening(repo))
}

// ReadmeScaffold renders the same page for `conform-to-sdlc init`, which promises a
// repo that passes the checker unedited — so this one opens with a real
// heading. The person running init is at the keyboard watching conform-to-sdlc list
// what it emitted; the body it gets is visibly unfinished, which is the honest
// state of a repo one command old.
//
// One body, two callers, so the page a scaffold emits and the shape the
// checker demands cannot drift.
func ReadmeScaffold(repo string) string {
	return readmeDoc(repo, "# "+repo)
}

// readmeFixOpening refuses to satisfy hasOpeningHeading: the first line with
// content is a comment, not a heading.
func readmeFixOpening(repo string) string {
	return "<!-- Replace this comment with `# " + repo + "` on the first line.\n" +
		"     Until you do, `conform-to-sdlc` stays red on the readme rule — a page that\n" +
		"     passed the gate would read as an introduction and carry none. -->"
}

// readmeDoc renders the page around whichever opening the caller wants.
// Everything below the opening is identical for both.
func readmeDoc(repo, opening string) string {
	return opening + `

<One paragraph on what ` + repo + ` is and who it is for. GitHub shows this
without scrolling — it is the repo's first impression, and the reason the root
stays minimal (decision d9cd0e20868b).>

## Install

` + "```" + `
go install github.com/dkoosis/` + repo + `/cmd/` + repo + `@latest
` + "```" + `

## Use

<The shortest command that does the thing, and what comes back.>

## Development

` + "```" + `
make check
` + "```" + `

The work lives in bd (` + "`bd ready`" + `).
`
}

// isHTMLComment reports whether line is a whole one-line HTML comment. It
// renders as nothing, so trixi's KG-publish banner on line 1 does not hide the
// heading under it.
func isHTMLComment(line string) bool {
	s := strings.TrimSpace(line)
	return strings.HasPrefix(s, "<!--") && strings.HasSuffix(s, "-->")
}
